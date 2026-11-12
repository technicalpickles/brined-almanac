// Package hoststate holds the rules for host state reports: parsing a report, deciding which
// checks are due to file, and keeping one key across a long outage. It is the Go port of
// src/host_state.rs. The workflow that files these findings waits for the Go engine.
package hoststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	brined "github.com/technicalpickles/brined-almanac"
	"github.com/technicalpickles/brined-almanac/routing"
)

const (
	CriticalAfter = 10 * time.Minute
	WarningAfter  = 24 * time.Hour
	StaleAfter    = 30 * time.Minute
	// WakeGap: a longer gap since the previous scan means the Mac likely slept, so an old report
	// may only be waiting on launchd's next collector run. It must stay above the scan interval
	// (ALMANAC_SCAN_INTERVAL_MINUTES, 15 by default), or an old report never files.
	WakeGap = 20 * time.Minute
	// RecoveryHold: a finding that comes back within this long of the last scan that saw it
	// continues the same outage, so a flapping check or an unreadable report rewritten every run
	// files one task.
	RecoveryHold = time.Hour
)

const maxOutputBytes = 500

type Status int

const (
	StatusOK Status = iota
	StatusWarning
	StatusCritical
	StatusUnknown
)

// group: critical and unknown both mean broken, so moving between them is one outage.
func (s Status) group() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarning:
		return "warning"
	default:
		return "broken"
	}
}

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarning:
		return "warning"
	case StatusCritical:
		return "critical"
	default:
		return "unknown"
	}
}

type CheckResult struct {
	Name   string
	Status Status
	Since  time.Time
	Output string
}

type Report struct {
	Host            string
	GeneratedAt     time.Time
	IntervalSeconds uint64
	Checks          []CheckResult
}

type rawReport struct {
	Host            string     `json:"host"`
	GeneratedAt     string     `json:"generated_at"`
	IntervalSeconds uint64     `json:"interval_seconds"`
	Checks          []rawCheck `json:"checks"`
}

type rawCheck struct {
	Name   string `json:"name"`
	Status int64  `json:"status"`
	Since  string `json:"since"`
	Output string `json:"output"`
}

func nameIsValid(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}

func parseTime(field, text string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %q is not an RFC 3339 time: %w", field, text, err)
	}
	return t.UTC(), nil
}

func cutOutput(output string) string {
	end := min(len(output), maxOutputBytes)
	for end > 0 && end < len(output) && !utf8.RuneStart(output[end]) {
		end--
	}
	return output[:end]
}

// ParseReport reads a report and refuses unknown fields, bad names, duplicate names, a check
// named stale, a status outside 0 to 3 and times without an offset.
func ParseReport(text string) (Report, error) {
	var raw rawReport
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Report{}, fmt.Errorf("host-state report: %w", err)
	}
	generatedAt, err := parseTime("generated_at", raw.GeneratedAt)
	if err != nil {
		return Report{}, err
	}
	seen := map[string]bool{}
	checks := make([]CheckResult, 0, len(raw.Checks))
	for _, c := range raw.Checks {
		if !nameIsValid(c.Name) {
			return Report{}, fmt.Errorf("check %q is outside [a-z0-9-]", c.Name)
		}
		if c.Name == "stale" {
			return Report{}, errors.New(`check "stale" is reserved for the stale finding's key`)
		}
		if seen[c.Name] {
			return Report{}, fmt.Errorf("check %s appears twice", c.Name)
		}
		seen[c.Name] = true
		if c.Status < 0 || c.Status > 3 {
			return Report{}, fmt.Errorf("check %s: status %d is outside 0 to 3", c.Name, c.Status)
		}
		since, err := parseTime("since", c.Since)
		if err != nil {
			return Report{}, fmt.Errorf("check %s: %w", c.Name, err)
		}
		checks = append(checks, CheckResult{Name: c.Name, Status: Status(c.Status), Since: since, Output: cutOutput(c.Output)})
	}
	return Report{Host: raw.Host, GeneratedAt: generatedAt, IntervalSeconds: raw.IntervalSeconds, Checks: checks}, nil
}

type Host struct {
	Name    string
	Project routing.Project
}

type rawHost struct {
	Name    string `toml:"name"`
	Project string `toml:"project"`
}

type hostsFile struct {
	Host []rawHost `toml:"host"`
}

type Hosts struct{ hosts []Host }

func ParseHosts(text string) (Hosts, error) {
	var file hostsFile
	md, err := toml.Decode(text, &file)
	if err != nil {
		return Hosts{}, fmt.Errorf("hosts: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return Hosts{}, fmt.Errorf("hosts: unknown field %s", u[0])
	}
	var hosts []Host
	for _, raw := range file.Host {
		// The name becomes a file name under the reports directory, so it can't be a path.
		if !nameIsValid(raw.Name) {
			return Hosts{}, fmt.Errorf("hosts: %q is outside [a-z0-9-]", raw.Name)
		}
		project, err := routing.ParseProject(raw.Project)
		if err != nil {
			return Hosts{}, fmt.Errorf("hosts: %s: %w", raw.Name, err)
		}
		for _, h := range hosts {
			if h.Name == raw.Name {
				return Hosts{}, fmt.Errorf("hosts: %s is declared twice", raw.Name)
			}
		}
		hosts = append(hosts, Host{Name: raw.Name, Project: project})
	}
	return Hosts{hosts: hosts}, nil
}

// LoadHosts reads the committed hosts.toml.
func LoadHosts() (Hosts, error) { return ParseHosts(string(brined.HostsTOML)) }

func (h Hosts) All() []Host { return h.hosts }

// Read is what the scan found at <dir>/<host>.json: ReadReport, ReadMissing or ReadInvalid.
type Read interface{ isRead() }

type ReadReport struct{ Report Report }
type ReadMissing struct{}
type ReadInvalid struct {
	Modified time.Time
	Error    string
}

func (ReadReport) isRead()  {}
func (ReadMissing) isRead() {}
func (ReadInvalid) isRead() {}

func ReadHost(dir string, host Host) Read {
	path := filepath.Join(dir, host.Name+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ReadMissing{}
	}
	if err != nil {
		return ReadInvalid{Modified: modified(path), Error: fmt.Sprintf("reading %s: %v", path, err)}
	}
	report, err := ParseReport(string(data))
	if err != nil {
		return ReadInvalid{Modified: modified(path), Error: err.Error()}
	}
	if report.Host != host.Name {
		return ReadInvalid{Modified: modified(path), Error: fmt.Sprintf("%s names host %q, expected %q", path, report.Host, host.Name)}
	}
	return ReadReport{Report: report}
}

func modified(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return info.ModTime().UTC()
}

// Finding is a CheckFinding or a StaleFinding.
type Finding interface {
	Key() string
	IsDue(now time.Time) bool
	Title() string
	Body() string
	episode() string
	sinceRef() *time.Time
}

type CheckFinding struct {
	Host   string
	Check  string
	Status Status
	Since  time.Time
	Output string
}

// StaleFinding has a nil Since when no report file exists.
type StaleFinding struct {
	Host   string
	Since  *time.Time
	Reason string
}

// IsoMinute is 2026-11-10T14:50Z, the form of a delivery ID and of the event's since.
func IsoMinute(t time.Time) string { return t.UTC().Format("2006-01-02T15:04Z") }

func utcMinute(t time.Time) string { return t.UTC().Format("2006-01-02 15:04Z") }

func (f *CheckFinding) Key() string {
	return fmt.Sprintf("%s/%s/%s", f.Host, f.Check, IsoMinute(f.Since))
}

func (f *StaleFinding) Key() string {
	if f.Since == nil {
		return f.Host + "/stale/missing"
	}
	return fmt.Sprintf("%s/stale/%s", f.Host, IsoMinute(*f.Since))
}

// episode is what stays the same across one outage, whatever its since.
func (f *CheckFinding) episode() string {
	return fmt.Sprintf("%s/%s/%s", f.Host, f.Check, f.Status.group())
}
func (f *StaleFinding) episode() string { return f.Host + "/stale" }

func (f *CheckFinding) sinceRef() *time.Time { return &f.Since }
func (f *StaleFinding) sinceRef() *time.Time { return f.Since }

// IsDue reports whether it has been failing long enough to file. A stale finding files at once.
func (f *CheckFinding) IsDue(now time.Time) bool {
	var threshold time.Duration
	switch f.Status {
	case StatusOK:
		return false
	case StatusWarning:
		threshold = WarningAfter
	default:
		threshold = CriticalAfter
	}
	return age(f.Since, now) >= threshold
}

func (f *StaleFinding) IsDue(time.Time) bool { return true }

func (f *CheckFinding) Title() string {
	return fmt.Sprintf("host-state: %s %s on %s since %s", f.Check, f.Status, f.Host, utcMinute(f.Since))
}

func (f *StaleFinding) Title() string {
	if f.Since == nil {
		return "host-state: no report from " + f.Host
	}
	return fmt.Sprintf("host-state: no report from %s since %s", f.Host, utcMinute(*f.Since))
}

func (f *CheckFinding) Body() string {
	return fmt.Sprintf("%s\nstatus: %s\nsince: %s", f.Output, f.Status, f.Since.UTC().Format(time.RFC3339))
}

func (f *StaleFinding) Body() string { return f.Reason }

// ScanGapIsTrusted reports whether the previous scan in this process ran recently enough that an
// old report means the collector stopped. A previous scan later than now means the clock stepped
// back.
func ScanGapIsTrusted(previous *time.Time, now time.Time) bool {
	return previous != nil && !previous.After(now) && now.Sub(*previous) <= WakeGap
}

// age counts a timestamp ahead of now (clock skew) as zero.
func age(t, now time.Time) time.Duration { return max(now.Sub(t), 0) }

// IsOldReport reports whether read is a report that parsed but was generated more than
// StaleAfter ago.
func IsOldReport(read Read, now time.Time) bool {
	r, ok := read.(ReadReport)
	return ok && age(r.Report.GeneratedAt, now) > StaleAfter
}

// Findings returns the findings that are due now, for a caller with no memory of earlier scans.
func Findings(host Host, read Read, now time.Time, oldReportMayFile bool) []Finding {
	var due []Finding
	for _, f := range Candidates(host, read, now, oldReportMayFile) {
		if f.IsDue(now) {
			due = append(due, f)
		}
	}
	return due
}

// Candidates returns every finding, due or not: a failing check under its threshold still counts
// toward an outage. oldReportMayFile is false when an old report should file nothing for now;
// missing and invalid reports file regardless.
func Candidates(host Host, read Read, now time.Time, oldReportMayFile bool) []Finding {
	stale := func(since *time.Time, reason string) []Finding {
		return []Finding{&StaleFinding{Host: host.Name, Since: since, Reason: reason}}
	}
	switch r := read.(type) {
	case ReadMissing:
		return stale(nil, "no report file for "+host.Name)
	case ReadInvalid:
		since := r.Modified
		return stale(&since, r.Error)
	case ReadReport:
		if IsOldReport(read, now) {
			if !oldReportMayFile {
				return nil
			}
			generated := r.Report.GeneratedAt
			return stale(&generated, fmt.Sprintf("last report for %s was generated %s", host.Name, utcMinute(generated)))
		}
		var out []Finding
		for _, c := range r.Report.Checks {
			if c.Status != StatusOK {
				out = append(out, &CheckFinding{Host: host.Name, Check: c.Name, Status: c.Status, Since: c.Since, Output: c.Output})
			}
		}
		return out
	}
	return nil
}

type episodeSpan struct{ start, lastSeen time.Time }

// Episodes holds the outages recent scans saw, so that a finding back within RecoveryHold keeps
// its first since, and with it its key. It lives in memory, so a restart mid-flap files once more.
type Episodes struct{ spans map[string]episodeSpan }

// ContinueOutages rewrites each finding's since to the start of the outage it continues.
func (e *Episodes) ContinueOutages(findings []Finding, now time.Time) {
	if e.spans == nil {
		e.spans = map[string]episodeSpan{}
	}
	for id, span := range e.spans {
		if now.Sub(span.lastSeen) > RecoveryHold {
			delete(e.spans, id)
		}
	}
	for _, f := range findings {
		since := f.sinceRef()
		if since == nil {
			continue
		}
		id := f.episode()
		span, ok := e.spans[id]
		if !ok {
			span = episodeSpan{start: *since}
		}
		span.lastSeen = now
		e.spans[id] = span
		*since = span.start
	}
}
