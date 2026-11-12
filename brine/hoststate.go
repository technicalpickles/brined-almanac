package brine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/vocab"
	"github.com/ivy/almanac/wire"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/hoststate"
	"github.com/technicalpickles/brined-almanac/routing"
)

// ReportsEnv names the directory host state reports are read from.
const ReportsEnv = "HOST_STATE_REPORTS"

// HostReports is the reports directory, empty to skip scanning, plus when this process last
// scanned and the outages it saw. It is always provided, because start-up checks a scan's
// providers.
type HostReports struct {
	Dir string

	mu       sync.Mutex
	previous *time.Time
	episodes hoststate.Episodes
}

// HostReportsFrom reads ReportsEnv through getenv. Unset and empty both skip.
func HostReportsFrom(getenv func(string) string) *HostReports {
	return &HostReports{Dir: getenv(ReportsEnv)}
}

// FindingKind is whether a finding is a failing check or a host gone quiet.
type FindingKind struct {
	hostile.Closed
	name string
}

var (
	KindCheck = FindingKind{name: "check"}
	KindStale = FindingKind{name: "stale"}
)

func (k FindingKind) String() string { return k.name }

func (k FindingKind) MarshalText() ([]byte, error) { return []byte(k.name), nil }

// UnmarshalJSON refuses any kind but check and stale, so the payload is malformed at /kind.
func (k *FindingKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	switch name {
	case KindCheck.name:
		*k = KindCheck
	case KindStale.name:
		*k = KindStale
	default:
		return errors.New("not a finding kind")
	}
	return nil
}

// HostFinding is one host-state finding. Body is a check's output line, which the host wrote,
// so it stays hostile. Since is YYYY-MM-DDTHH:MMZ, and a stale finding may have none.
type HostFinding struct {
	Type    FindingKind        `json:"kind"`
	Project hostile.Slug       `json:"project"`
	Subject hostile.Slug       `json:"subject"`
	Host    hostile.Slug       `json:"host"`
	Check   *hostile.Slug      `json:"check"`
	Status  *hostile.Slug      `json:"status"`
	Since   *hostile.Slug      `json:"since"`
	Body    hostile.Of[string] `json:"body"`
}

func (HostFinding) Kind() string        { return "host.state.finding" }
func (HostFinding) AlsoKinds() []string { return nil }

// Parse requires check, status and since on a check finding. That a project is a taskwarrior
// project and a since splits at T is the filing observer's to refuse: a parser here can only
// refuse a field whose type fails to decode.
func (e *HostFinding) Parse(p *wire.Payload) {
	e.Type = wire.Member[FindingKind](p, "/kind")
	e.Project = wire.Field[hostile.Slug](p, "/project")
	if e.Type == KindCheck {
		check := wire.Field[hostile.Slug](p, "/check")
		status := wire.Field[hostile.Slug](p, "/status")
		since := wire.Field[hostile.Slug](p, "/since")
		e.Check, e.Status, e.Since = &check, &status, &since
	} else {
		e.Check = wire.OptionalField[hostile.Slug](p, "/check")
		e.Status = wire.OptionalField[hostile.Slug](p, "/status")
		e.Since = wire.OptionalField[hostile.Slug](p, "/since")
	}
	e.Subject = wire.Field[hostile.Slug](p, "/subject")
	e.Host = wire.Field[hostile.Slug](p, "/host")
	e.Body = p.Text("/body")
}

// Since is a finding's since split for a title: 2026-11-10 and 14:50Z.
type Since struct {
	Date hostile.Slug `json:"date"`
	Time hostile.Slug `json:"time"`
}

// Filing is where a finding files and when it started.
type Filing struct {
	Target hostile.Slug `json:"target"`
	Since  *Since       `json:"since"`
}

func splitSince(since hostile.Slug) (Since, error) {
	date, clock, ok := strings.Cut(since.String(), "T")
	if !ok {
		return Since{}, errors.New("since has no T")
	}
	d, err := hostile.ParseSlug(date)
	if err != nil {
		return Since{}, fmt.Errorf("since's date: %w", err)
	}
	t, err := hostile.ParseSlug(clock)
	if err != nil {
		return Since{}, fmt.Errorf("since's time: %w", err)
	}
	return Since{Date: d, Time: t}, nil
}

// FilingOf is the filing for a finding, refused when its project is not a taskwarrior project or
// its since does not split at T.
func FilingOf(e HostFinding) (Filing, error) {
	project, err := routing.ParseProject(e.Project.String())
	if err != nil {
		return Filing{}, err
	}
	target, err := hostile.ParseSlug(project.Target())
	if err != nil {
		return Filing{}, fmt.Errorf("target for %s: %w", project, err)
	}
	filing := Filing{Target: target}
	if e.Since != nil {
		since, err := splitSince(*e.Since)
		if err != nil {
			return Filing{}, err
		}
		filing.Since = &since
	}
	return filing, nil
}

func fileFinding(_ context.Context, _ workflow.Lookup, e HostFinding) (Filing, error) {
	return FilingOf(e)
}

func isCheck(e HostFinding) bool  { return e.Type == KindCheck }
func hasSince(e HostFinding) bool { return e.Since != nil }

var (
	HostStateFinding = vocab.Event[HostFinding](Vocabulary, "brine.host_finding",
		"a host check has been failing, or a host's report stopped arriving")
	FileFinding = vocab.Observer(Vocabulary, "brine.file_finding",
		"the project a host finding files to, and when it started", fileFinding)
	IsCheck = vocab.Predicate(Vocabulary, "brine.is_check",
		"the finding is a failing check", isCheck)
	HasSince = vocab.Predicate(Vocabulary, "brine.has_since",
		"the finding says when it started", hasSince)
	ScanHostState = vocab.Scan(Vocabulary, "brine.host_state",
		"host checks failing long enough to file, and hosts whose reports stopped",
		scanHostState, needsHosts, needsHostReports)
)

func scanHostState(_ context.Context, scanner *workflow.Scanner) ([]workflow.Found, error) {
	now := time.Now().UTC()
	reports, err := workflow.Get[*HostReports](scanner)
	if err != nil {
		return nil, err
	}
	reports.mu.Lock()
	defer reports.mu.Unlock()
	previous := reports.previous
	reports.previous = &now
	if reports.Dir == "" {
		fmt.Fprintf(os.Stderr, "host-state: %s is not set, skipping\n", ReportsEnv)
		return nil, nil
	}
	hosts, err := workflow.Get[hoststate.Hosts](scanner)
	if err != nil {
		return nil, err
	}
	return ScanFindings(hosts, reports.Dir, now, previous, &reports.episodes), nil
}

// ScanFindings is one event per due finding, its delivery ID the finding's key. A file in dir
// that no declared host owns is logged and files nothing. previous is when this process last
// scanned.
func ScanFindings(hosts hoststate.Hosts, dir string, now time.Time, previous *time.Time, episodes *hoststate.Episodes) []workflow.Found {
	trusted := hoststate.ScanGapIsTrusted(previous, now)
	var out []workflow.Found
	for _, host := range hosts.All() {
		read := hoststate.ReadHost(dir, host)
		if invalid, ok := read.(hoststate.ReadInvalid); ok {
			fmt.Fprintf(os.Stderr, "host-state: %s\n", invalid.Error)
		}
		if !trusted && hoststate.IsOldReport(read, now) {
			fmt.Fprintf(os.Stderr, "host-state: %s's report is over 30 minutes old, but %s; not filing it as stale until the next scan\n", host.Name, untrustedGap(previous, now))
		}
		found := hoststate.Candidates(host, read, now, trusted)
		episodes.ContinueOutages(found, now)
		for _, finding := range found {
			if finding.IsDue(now) {
				out = append(out, workflow.Scanned[HostFinding](finding.Key(), payloadOf(host, finding)))
			}
		}
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if !declared(hosts, entry.Name()) {
				fmt.Fprintf(os.Stderr, "host-state: %s is not a declared host's report, ignoring\n", entry.Name())
			}
		}
	}
	return out
}

func untrustedGap(previous *time.Time, now time.Time) string {
	switch {
	case previous == nil:
		return "this is the first scan since almanac started"
	case previous.After(now):
		return fmt.Sprintf("the previous scan at %s is later than now", hoststate.IsoMinute(*previous))
	default:
		return fmt.Sprintf("the previous scan was at %s, so the host likely slept", hoststate.IsoMinute(*previous))
	}
}

func declared(hosts hoststate.Hosts, file string) bool {
	for _, h := range hosts.All() {
		if file == h.Name+".json" {
			return true
		}
	}
	return false
}

func payloadOf(host hoststate.Host, finding hoststate.Finding) json.RawMessage {
	payload := map[string]any{
		"kind":    "stale",
		"project": host.Project.String(),
		"subject": strings.ReplaceAll(finding.Key(), "/", "."),
		"host":    host.Name,
		"check":   nil,
		"status":  nil,
		"since":   nil,
		"body":    finding.Body(),
	}
	switch f := finding.(type) {
	case *hoststate.CheckFinding:
		payload["kind"] = "check"
		payload["check"] = f.Check
		payload["status"] = f.Status.String()
		payload["since"] = hoststate.IsoMinute(f.Since)
	case *hoststate.StaleFinding:
		if f.Since != nil {
			payload["since"] = hoststate.IsoMinute(*f.Since)
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return out
}
