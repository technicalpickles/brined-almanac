package brine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/vocab"
	"github.com/ivy/almanac/wire"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/bugsink"
	"github.com/technicalpickles/brined-almanac/bugsinkapi"
	"github.com/technicalpickles/brined-almanac/classes"
	"github.com/technicalpickles/brined-almanac/cutover"
	"github.com/technicalpickles/brined-almanac/resolveonclose"
	"github.com/technicalpickles/brined-almanac/transcript"
)

// Transcripts is the directory Brent's transcripts are read from.
type Transcripts struct{ Dir string }

// BugsinkClient ships the failed tool calls of the transcripts. It is always provided, because
// start-up checks a scan's providers. A nil Sender means BUGSINK_DSN is not set.
type BugsinkClient struct {
	Sender *bugsink.Bugsink
	// Live false is a dry run, which counts what is due and sends nothing.
	Live bool
}

// BugsinkReader is always provided, because start-up checks a scan's providers. A nil Reading
// means no API token or no DSN.
type BugsinkReader struct{ Reading *Reading }

// Reading is what a scan of Bugsink reads and, when live, changes.
type Reading struct {
	Bugsink *cutover.Bugsink
	// Github is nil without GITHUB_TOKEN, which turns off acting on closed almanac issues.
	Github *cutover.Github
	// Live false is a dry run, which reads and says what it would do, and changes nothing in
	// Bugsink.
	Live bool
}

var (
	needsTranscripts   = providers.Require[Transcripts]()
	needsBugsinkClient = providers.Require[BugsinkClient]()
	needsBugsinkReader = providers.Require[BugsinkReader]()
)

// Surface is where an issue's events came from, and how many.
type Surface struct {
	Name  hostile.Slug `json:"name"`
	Count uint64       `json:"count"`
}

// Unaddressed is a Bugsink issue that should have a GitHub issue. Generation counts the times
// it came back after being resolved. Reason is transcript text, so it stays hostile.
type Unaddressed struct {
	// Source is constant. A key is only event fields, so the "bugsink:" in the Rust key has to
	// be a field.
	Source      hostile.Slug       `json:"source"`
	Issue       hostile.Slug       `json:"issue"`
	Generation  uint64             `json:"generation"`
	Service     hostile.Slug       `json:"service"`
	Tool        hostile.Slug       `json:"tool"`
	Class       hostile.Slug       `json:"class"`
	Count       uint64             `json:"count"`
	FirstSeen   hostile.Slug       `json:"first_seen"`
	LastSeen    hostile.Slug       `json:"last_seen"`
	IngestDated bool               `json:"ingest_dated"`
	Surfaces    []Surface          `json:"surfaces"`
	Sessions    uint64             `json:"sessions"`
	Link        hostile.Slug       `json:"link"`
	Reason      hostile.Of[string] `json:"reason"`
}

func (Unaddressed) Kind() string        { return "bugsink.issue.unaddressed" }
func (Unaddressed) AlsoKinds() []string { return nil }

func (e *Unaddressed) Parse(p *wire.Payload) {
	e.Source = hostile.MustParseSlug("bugsink")
	e.Issue = wire.Field[hostile.Slug](p, "/issue")
	e.Generation = wire.Field[uint64](p, "/generation")
	e.Service = wire.Field[hostile.Slug](p, "/service")
	e.Tool = wire.Field[hostile.Slug](p, "/tool")
	e.Class = wire.Field[hostile.Slug](p, "/class")
	e.Count = wire.Field[uint64](p, "/count")
	e.FirstSeen = wire.Field[hostile.Slug](p, "/first_seen")
	e.LastSeen = wire.Field[hostile.Slug](p, "/last_seen")
	e.IngestDated = wire.Field[bool](p, "/ingest_dated")
	_ = wire.Field[aList](p, "/surfaces")
	names := wire.List[hostile.Slug](p, "/surfaces", "/name")
	counts := wire.List[uint64](p, "/surfaces", "/count")
	for i := range min(len(names), len(counts)) {
		e.Surfaces = append(e.Surfaces, Surface{Name: names[i], Count: counts[i]})
	}
	e.Sessions = wire.Field[uint64](p, "/sessions")
	e.Link = wire.Field[hostile.Slug](p, "/link")
	e.Reason = p.Text("/reason")
}

// aList decodes only a JSON list, empty or not. [wire.Field] already refuses a missing or null
// value, so reading /surfaces as an aList refuses anything but a list, as Rust's parse does,
// where [wire.List] would read it as empty.
type aList bool

func (l *aList) UnmarshalJSON(raw []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return errors.New("not a list")
	}
	*l = true
	return nil
}

// IssueFiling is where an unaddressed issue files, and what its issue says.
type IssueFiling struct {
	Target hostile.Slug `json:"target"`
	// Body is a clean header joined to the reason, so it is as hostile as the reason.
	Body hostile.Of[string] `json:"body"`
}

// header is the clean part of the issue body, ending where the reason starts.
func header(e Unaddressed) string {
	surfaces := make([]string, len(e.Surfaces))
	for i, s := range e.Surfaces {
		surfaces[i] = s.Name.String() + " (" + strconv.FormatUint(s.Count, 10) + ")"
	}
	basis := ""
	if e.IngestDated {
		basis = " (Bugsink ingest time)"
	}
	var b strings.Builder
	b.WriteString("service: " + e.Service.String() + "\n")
	b.WriteString("tool: " + e.Tool.String() + "\n")
	b.WriteString("class: " + e.Class.String() + "\n")
	b.WriteString("events: " + strconv.FormatUint(e.Count, 10) + "\n")
	b.WriteString("first seen: " + e.FirstSeen.String() + basis + "\n")
	b.WriteString("last seen: " + e.LastSeen.String() + basis + "\n")
	b.WriteString("surfaces: " + strings.Join(surfaces, ", ") + "\n")
	b.WriteString("sessions: " + strconv.FormatUint(e.Sessions, 10) + "\n")
	if e.Generation > 0 {
		b.WriteString("regressions: " + strconv.FormatUint(e.Generation, 10) + " (came back after being resolved)\n")
	}
	b.WriteString("bugsink: " + e.Link.String() + "\n\nreason:\n")
	return b.String()
}

func fileIssue(_ context.Context, _ workflow.Lookup, e Unaddressed) (IssueFiling, error) {
	target, err := hostile.ParseSlug(classes.OwnerOf(e.Service.String()))
	if err != nil {
		return IssueFiling{}, err
	}
	return IssueFiling{Target: target, Body: hostile.Join(header(e), e.Reason)}, nil
}

func isRegression(e Unaddressed) bool { return e.Generation > 0 }

var (
	BugsinkUnaddressed = vocab.Event[Unaddressed](Vocabulary, "brine.bugsink_unaddressed",
		"a Bugsink issue of failed tool calls that nobody has resolved or muted")
	FileIssue = vocab.Observer(Vocabulary, "brine.file_issue",
		"the repository an unaddressed Bugsink issue files to, and the issue body", fileIssue)
	IsRegression = vocab.Predicate(Vocabulary, "brine.is_regression",
		"the Bugsink issue came back after being resolved", isRegression)
	ScanBugsinkIssues = vocab.Scan(Vocabulary, "brine.bugsink_issues",
		"Bugsink issues of failed tool calls that nobody has resolved or muted, after settling those whose almanac issue was closed",
		scanBugsinkIssues, needsTranscripts, needsBugsinkClient, needsBugsinkReader)
)

var classTable = classes.Default()

// WantsFiling is whether an issue wants a GitHub issue: nobody has resolved or muted it, and it
// has as many events as its class needs. A class with no threshold never files, and a class the
// table does not name gets other's.
func WantsFiling(issue bugsinkapi.Issue, class string) bool {
	if issue.Resolved || issue.Muted {
		return false
	}
	needed := classTable.Named(class).Threshold
	return needed != nil && issue.Count >= *needed
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

// shipFrom ships errs if a DSN is set. A failure is printed, not returned, so Bugsink being
// down never stops the scan that called this.
func shipFrom(ctx context.Context, scanner *workflow.Scanner, errs []transcript.ToolError) error {
	client, err := workflow.Get[BugsinkClient](scanner)
	if err != nil {
		return err
	}
	if client.Sender == nil {
		logf("transcript-shipper: BUGSINK_DSN is not set, skipping")
		return nil
	}
	shipped, err := client.Sender.Ship(ctx, errs)
	switch {
	case err != nil:
		logf("transcript-shipper: %v", err)
	case !client.Live:
		logf("transcript-shipper: dry run, %d event(s) due, sent none", shipped.Due)
	default:
		logf("transcript-shipper: sent %d of %d event(s)", shipped.Sent, shipped.Due)
	}
	return nil
}

// settleClosed resolves or mutes each open Bugsink issue whose almanac issue was closed, so
// that the close never hides the failure's next event. It returns the issues it settled, which
// the scan must not report again. Every failure is printed and skipped: GitHub or Bugsink being
// down must not stop the filing that follows.
func settleClosed(ctx context.Context, reading *Reading, open []bugsinkapi.Issue) map[string]bool {
	settled := map[string]bool{}
	if reading.Github == nil {
		logf("bugsink-issues: GITHUB_TOKEN is not set, not resolving closed issues")
		return settled
	}
	var linked []cutover.Linked
	for _, repo := range cutover.Repos {
		found, err := reading.Github.Linked(ctx, repo)
		if err != nil {
			logf("bugsink-issues: not resolving closed issues: %v", err)
			return settled
		}
		linked = append(linked, found...)
	}
	named := map[string]bool{}
	for _, l := range linked {
		named[l.Issue] = true
	}
	for _, issue := range open {
		if !named[issue.ID] {
			continue
		}
		history, err := reading.Bugsink.History(ctx, issue)
		if err != nil {
			logf("bugsink-issues: %v", err)
			continue
		}
		unmutedAt, err := bugsinkapi.LastUnmuted(history)
		if err != nil {
			logf("bugsink-issues: %s: %v", issue.FriendlyID, err)
			continue
		}
		verdict := resolveonclose.Decide(resolveonclose.Open{
			ID:         issue.ID,
			Generation: bugsinkapi.Generation(history),
			UnmutedAt:  unmutedAt,
		}, linked)
		name := issue.FriendlyID
		var done error
		switch {
		case verdict.Kind == resolveonclose.Leave:
			continue
		case verdict.Kind == resolveonclose.Resolve && !reading.Live:
			logf("bugsink-issues: dry run, would resolve %s for %s", name, verdict.Proof)
		case verdict.Kind == resolveonclose.Mute && !reading.Live:
			logf("bugsink-issues: dry run, would mute %s for %s", name, verdict.Proof)
		case verdict.Kind == resolveonclose.Resolve:
			done = reading.Bugsink.ResolveIssue(ctx, issue, resolveonclose.ResolveComment(verdict.Proof))
		default:
			done = reading.Bugsink.MuteUntil(ctx, issue, resolveonclose.MuteComment(verdict.Proof), resolveonclose.MuteWakeEvents)
		}
		settled[issue.ID] = true
		switch {
		case done != nil:
			logf("bugsink-issues: %v", done)
		case reading.Live:
			logf("bugsink-issues: settled %s", name)
		}
	}
	return settled
}

type scannedSurface struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

type scannedIssue struct {
	Issue       string           `json:"issue"`
	Generation  uint64           `json:"generation"`
	Service     string           `json:"service"`
	Tool        string           `json:"tool"`
	Class       string           `json:"class"`
	Count       uint64           `json:"count"`
	FirstSeen   string           `json:"first_seen"`
	LastSeen    string           `json:"last_seen"`
	IngestDated bool             `json:"ingest_dated"`
	Surfaces    []scannedSurface `json:"surfaces"`
	Sessions    uint64           `json:"sessions"`
	Link        string           `json:"link"`
	Reason      string           `json:"reason"`
}

func scannedOf(issue bugsinkapi.Issue, labels bugsinkapi.Labels, generation uint64, firstSeen, lastSeen string, ingestDated bool, link string) (workflow.Found, error) {
	surfaces := make([]scannedSurface, 0, len(labels.Surfaces))
	for _, s := range labels.Surfaces {
		surfaces = append(surfaces, scannedSurface{Name: s.Name, Count: s.Count})
	}
	payload, err := json.Marshal(scannedIssue{
		Issue: issue.ID, Generation: generation, Service: labels.Service, Tool: labels.Tool, Class: labels.Class,
		Count: issue.Count, FirstSeen: firstSeen, LastSeen: lastSeen, IngestDated: ingestDated,
		Surfaces: surfaces, Sessions: labels.Sessions, Link: link, Reason: issue.Reason,
	})
	if err != nil {
		return workflow.Found{}, err
	}
	found := workflow.Scanned[Unaddressed]("bugsink:"+issue.ID, payload)
	return found.AtState(strconv.FormatUint(generation, 10)), nil
}

func scanBugsinkIssues(ctx context.Context, scanner *workflow.Scanner) ([]workflow.Found, error) {
	transcripts, err := workflow.Get[Transcripts](scanner)
	if err != nil {
		return nil, err
	}
	read, err := transcript.ReadDir(transcripts.Dir)
	if err != nil {
		return nil, err
	}
	if read.Skipped.NotJSON > 0 {
		logf("%s: skipped %d lines that were not JSON", transcripts.Dir, read.Skipped.NotJSON)
	}
	if read.Skipped.Undated > 0 {
		logf("%s: skipped %d lines with no usable timestamp", transcripts.Dir, read.Skipped.Undated)
	}
	if err := shipFrom(ctx, scanner, read.Errors); err != nil {
		return nil, err
	}

	reader, err := workflow.Get[BugsinkReader](scanner)
	if err != nil {
		return nil, err
	}
	reading := reader.Reading
	if reading == nil {
		logf("bugsink-issues: BUGSINK_API_TOKEN or BUGSINK_DSN is not set, skipping")
		return nil, nil
	}
	bugsink := reading.Bugsink
	listed, err := bugsink.OpenIssues(ctx)
	if err != nil {
		return nil, err
	}
	var open []bugsinkapi.Issue
	for _, issue := range listed {
		if !issue.Resolved && !issue.Muted {
			open = append(open, issue)
		}
	}
	settled := settleClosed(ctx, reading, open)
	var found []workflow.Found
	for _, issue := range open {
		if settled[issue.ID] {
			continue
		}
		tags, err := bugsink.Tags(ctx, issue)
		if err != nil {
			return nil, err
		}
		labels, err := bugsinkapi.LabelsOf(tags)
		if err != nil {
			logf("bugsink-issues: skipping %s: %v", issue.FriendlyID, err)
			continue
		}
		if !WantsFiling(issue, labels.Class) {
			continue
		}
		history, err := bugsink.History(ctx, issue)
		if err != nil {
			return nil, err
		}
		events, err := bugsink.Events(ctx, issue)
		if err != nil {
			return nil, err
		}
		first, last, ingestDated := issue.FirstSeen, issue.LastSeen, true
		if f, l, err := bugsinkapi.Seen(events); err != nil {
			logf("bugsink-issues: %s has no event times (%v), dating it by Bugsink ingest time", issue.FriendlyID, err)
		} else {
			first, last, ingestDated = f, l, false
		}
		one, err := scannedOf(issue, labels, bugsinkapi.Generation(history), first, last, ingestDated, bugsink.IssueURL(issue))
		if err != nil {
			return nil, err
		}
		found = append(found, one)
	}
	return found, nil
}
