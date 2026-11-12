package bugsinkissues_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ivy/almanac/check"
	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/wire"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/brine/bugsinkissues"
	"github.com/technicalpickles/brined-almanac/bugsink"
	"github.com/technicalpickles/brined-almanac/bugsinkapi"
	"github.com/technicalpickles/brined-almanac/classes"
	"github.com/technicalpickles/brined-almanac/cutover"
	"github.com/technicalpickles/brined-almanac/internal/fakeapi"
	"github.com/technicalpickles/brined-almanac/internal/walktest"
)

const id = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01"

func event(t *testing.T, edit func(map[string]any)) json.RawMessage {
	t.Helper()
	p := map[string]any{
		"issue":        id,
		"generation":   0,
		"service":      "brineworks",
		"tool":         "triage_apply",
		"class":        "mcp-timeout",
		"count":        14,
		"first_seen":   "2026-11-04T10:00:00.250000Z",
		"last_seen":    "2026-11-09T21:14:03.482113Z",
		"ingest_dated": false,
		"surfaces":     []any{map[string]any{"name": "telegram", "count": 11}, map[string]any{"name": "main", "count": 3}},
		"sessions":     2,
		"link":         "http://bugsink.example/issues/issue/" + id + "/event/last/",
		"reason":       "MCP error -32001: Request timed out",
	}
	if edit != nil {
		edit(p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func withGeneration(n int) func(map[string]any) {
	return func(p map[string]any) { p["generation"] = n }
}

func walk(t *testing.T, raw json.RawMessage) (effect.File, string) {
	t.Helper()
	w, err := bugsinkissues.New()
	if err != nil {
		t.Fatal(err)
	}
	ended, err := w.Walk(context.Background(), walktest.Cx{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ended.Decided.Effects) != 1 {
		t.Fatalf("want one effect, got %+v", ended.Decided.Effects)
	}
	file, ok := ended.Decided.Effects[0].(effect.File)
	if !ok {
		t.Fatalf("want a File, got %T", ended.Decided.Effects[0])
	}
	return file, ended.Step + "/" + ended.Rule
}

func filing(t *testing.T, raw json.RawMessage) effect.File {
	t.Helper()
	file, _ := walk(t, raw)
	return file
}

// textOf reads hostile text by position, because the repository forbids the key it is stored
// under in source.
func textOf(t *testing.T, body hostile.Of[string]) string {
	t.Helper()
	stored, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(stored, &fields); err != nil || len(fields) != 1 {
		t.Fatalf("body %s: %v", stored, err)
	}
	for _, text := range fields {
		return text
	}
	return ""
}

func TestTheModelChecks(t *testing.T) {
	if err := brine.Vocabulary.Err(); err != nil {
		t.Fatal(err)
	}
	if err := check.Check(brine.Vocabulary, bugsinkissues.Model); err != nil {
		t.Fatal(err)
	}
}

func TestTheModelDeclaresWhatRustDoes(t *testing.T) {
	w, err := bugsinkissues.New()
	if err != nil {
		t.Fatal(err)
	}
	if w.RunsPerHour() != 1000 || !slices.Equal(w.May(), []effect.Kind{effect.KindFile}) || w.Kind() != "bugsink.issue.unaddressed" {
		t.Fatalf("runs %d, may %v, kind %s", w.RunsPerHour(), w.May(), w.Kind())
	}
	if w.Intent() != "File one issue per Bugsink issue of failed tool calls that nobody has resolved or muted." {
		t.Fatal(w.Intent())
	}
}

func TestTheWorkflowKeyIsTheIssueAndItsGeneration(t *testing.T) {
	w, err := bugsinkissues.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, generation := range []int{0, 3} {
		identity, err := w.Identify(event(t, withGeneration(generation)))
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("bugsink:%s:%d", id, generation); identity.Key != want {
			t.Errorf("key %q, want %q", identity.Key, want)
		}
	}
}

func TestTheBodyListsTheHeaderThenABlankLineThenTheReason(t *testing.T) {
	got := textOf(t, filing(t, event(t, nil)).Body)
	want := "service: brineworks\ntool: triage_apply\nclass: mcp-timeout\nevents: 14\nfirst seen: 2026-11-04T10:00:00.250000Z\nlast seen: 2026-11-09T21:14:03.482113Z\nsurfaces: telegram (11), main (3)\nsessions: 2\nbugsink: http://bugsink.example/issues/issue/" + id + "/event/last/\n\nreason:\nMCP error -32001: Request timed out"
	if got != want {
		t.Errorf("body %q, want %q", got, want)
	}
}

func TestARegressedIssueSaysSoInTheHeader(t *testing.T) {
	body := textOf(t, filing(t, event(t, withGeneration(2))).Body)
	header, _, _ := strings.Cut(body, "\n\n")
	if !strings.Contains(header, "\nregressions: 2 (came back after being resolved)\n") {
		t.Errorf("header %q", header)
	}
	if strings.Contains(textOf(t, filing(t, event(t, nil)).Body), "regressions") {
		t.Error("a first filing mentions regressions")
	}
}

func TestAnIssueDatedByIngestSaysSoOnBothDates(t *testing.T) {
	body := textOf(t, filing(t, event(t, func(p map[string]any) { p["ingest_dated"] = true })).Body)
	if !strings.Contains(body, "\nfirst seen: 2026-11-04T10:00:00.250000Z (Bugsink ingest time)\nlast seen: 2026-11-09T21:14:03.482113Z (Bugsink ingest time)\n") {
		t.Error(body)
	}
}

func TestAnIssueWithNoSurfacesHasAnEmptySurfacesLine(t *testing.T) {
	body := textOf(t, filing(t, event(t, func(p map[string]any) { p["surfaces"] = []any{} })).Body)
	if !strings.Contains(body, "\nsurfaces: \nsessions: 2\n") {
		t.Error(body)
	}
}

func TestTheReasonStaysAfterTheHeaderWhateverItSays(t *testing.T) {
	forged := "service: forged\n\nreason:\n`rm` @someone"
	file := filing(t, event(t, func(p map[string]any) { p["reason"] = forged }))
	body := textOf(t, file.Body)
	if !strings.HasSuffix(body, "\n\nreason:\n"+forged) || !strings.HasPrefix(body, "service: brineworks\n") {
		t.Error(body)
	}
	if strings.Contains(file.Title.String(), "rm") {
		t.Error(file.Title)
	}
}

func TestTheTitleNamesTheFailureAndItsEventsAndFlagsARegression(t *testing.T) {
	plain, rule := walk(t, event(t, nil))
	if plain.Title.String() != "Brent: brineworks/triage_apply mcp-timeout (14 events)" || rule != "file/first" {
		t.Errorf("%q at %s", plain.Title, rule)
	}
	regressed, rule := walk(t, event(t, withGeneration(1)))
	if regressed.Title.String() != "Brent: brineworks/triage_apply mcp-timeout regressed (14 events)" || rule != "file/regressed" {
		t.Errorf("%q at %s", regressed.Title, rule)
	}
}

func TestBrineworksAndGogRouteToTheirRepositories(t *testing.T) {
	for service, repo := range map[string]string{"brineworks": "technicalpickles/brineworks", "gog": "technicalpickles/pickleclaw"} {
		file := filing(t, event(t, func(p map[string]any) { p["service"] = service }))
		if file.Target.String() != repo {
			t.Errorf("%s files to %s, want %s", service, file.Target, repo)
		}
	}
}

func TestEveryEndingHasAWorldThatReachesIt(t *testing.T) {
	worlds := map[string]json.RawMessage{"file/first": event(t, nil), "file/regressed": event(t, withGeneration(1))}
	var endings []string
	for _, e := range bugsinkissues.Model.Endings() {
		endings = append(endings, e.Step+"/"+e.Rule)
	}
	if len(endings) != len(worlds) {
		t.Fatalf("model endings %v", endings)
	}
	for _, end := range endings {
		world, ok := worlds[end]
		if !ok {
			t.Fatalf("no world for %s", end)
		}
		if _, reached := walk(t, world); reached != end {
			t.Errorf("world for %s ended at %s", end, reached)
		}
	}
}

func malformedAt(t *testing.T, raw json.RawMessage, pointer string) {
	t.Helper()
	_, err := wire.Parse[brine.Unaddressed](raw)
	var malformed *wire.Malformed
	if !errors.As(err, &malformed) || malformed.Pointer != pointer {
		t.Fatalf("want malformed at %s, got %v", pointer, err)
	}
}

func TestAPayloadMissingAFieldIsMalformedAtIt(t *testing.T) {
	for _, field := range []string{"issue", "generation", "service", "tool", "class", "count", "first_seen", "last_seen", "ingest_dated", "sessions", "link", "reason"} {
		malformedAt(t, event(t, func(p map[string]any) { delete(p, field) }), "/"+field)
	}
}

func TestAPayloadWithAFieldOfTheWrongShapeIsMalformedAtIt(t *testing.T) {
	malformedAt(t, event(t, func(p map[string]any) { p["service"] = "has a space" }), "/service")
	malformedAt(t, event(t, func(p map[string]any) { p["count"] = "many" }), "/count")
	malformedAt(t, event(t, func(p map[string]any) {
		p["surfaces"] = []any{map[string]any{"name": "has a space", "count": 1}}
	}), "/surfaces[]/name")
	malformedAt(t, event(t, func(p map[string]any) {
		p["surfaces"] = []any{map[string]any{"name": "main"}}
	}), "/surfaces[]/count")
}

func TestAPayloadReadsBackAsTheEventItWas(t *testing.T) {
	e, err := wire.Parse[brine.Unaddressed](event(t, withGeneration(1)))
	if err != nil {
		t.Fatal(err)
	}
	if e.Issue.String() != id || e.Generation != 1 || e.Count != 14 || e.Sessions != 2 || e.IngestDated ||
		len(e.Surfaces) != 2 || e.Surfaces[0].Name.String() != "telegram" || e.Surfaces[1].Count != 3 {
		t.Fatalf("%+v", e)
	}
}

func issue(count uint64, resolved, muted bool) bugsinkapi.Issue {
	return bugsinkapi.Issue{ID: id, FriendlyID: "BRENT-12", Count: count, Resolved: resolved, Muted: muted}
}

func TestAnIssueWantsFilingAtItsClassThresholdAndNotOneBelow(t *testing.T) {
	for _, class := range classes.Default().Rows() {
		if class.Threshold == nil {
			continue
		}
		if !brine.WantsFiling(issue(*class.Threshold, false, false), class.Name) {
			t.Errorf("%s at threshold", class.Name)
		}
		if brine.WantsFiling(issue(*class.Threshold-1, false, false), class.Name) {
			t.Errorf("%s one below", class.Name)
		}
	}
}

func TestAClassWithNoThresholdNeverFilesHoweverLoud(t *testing.T) {
	silent := 0
	for _, class := range classes.Default().Rows() {
		if class.Threshold != nil {
			continue
		}
		silent++
		if brine.WantsFiling(issue(100_000, false, false), class.Name) {
			t.Errorf("%s", class.Name)
		}
	}
	if silent == 0 {
		t.Fatal("no class without a threshold")
	}
}

func TestAResolvedOrMutedIssueWantsNothingHoweverLoud(t *testing.T) {
	for _, held := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		if brine.WantsFiling(issue(1000, held[0], held[1]), "mcp-timeout") {
			t.Errorf("resolved %v, muted %v", held[0], held[1])
		}
	}
}

func TestAnUnknownClassGetsTheThresholdOfOther(t *testing.T) {
	if brine.WantsFiling(issue(2, false, false), "gone") {
		t.Error("two events")
	}
	if !brine.WantsFiling(issue(3, false, false), "gone") {
		t.Error("three events")
	}
}

const (
	loud     = "11111111-aaaa-4aaa-8aaa-000000000001"
	quiet    = "11111111-aaaa-4aaa-8aaa-000000000002"
	muted    = "11111111-aaaa-4aaa-8aaa-000000000003"
	resolved = "11111111-aaaa-4aaa-8aaa-000000000004"
	untagged = "11111111-aaaa-4aaa-8aaa-000000000005"
	second   = "11111111-aaaa-4aaa-8aaa-000000000006"

	ingested = "2026-11-10T03:12:13.710632Z"
	list     = "/api/canonical/0/issues/?project=7&state=open"
)

type obj = map[string]any

func listed(id string, count int, resolved, muted bool) obj {
	return obj{
		"id": id, "friendly_id": "BRENT-" + id[len(id)-2:], "first_seen": ingested, "last_seen": ingested,
		"digested_event_count": count, "calculated_value": "MCP error -32001: Request timed out",
		"is_resolved": resolved, "is_muted": muted,
	}
}

func page(results ...any) obj {
	if results == nil {
		results = []any{}
	}
	return obj{"next": nil, "results": results}
}

func tagged(class string) obj {
	return page(
		obj{"key": "service", "value": "brineworks", "count": 5},
		obj{"key": "tool", "value": "triage_apply", "count": 5},
		obj{"key": "class", "value": class, "count": 5},
		obj{"key": "surface", "value": "telegram", "count": 5},
		obj{"key": "session", "value": "a1b2c3d4e5f6", "count": 5},
	)
}

func history(kinds ...string) obj {
	rows := []any{}
	for _, kind := range kinds {
		rows = append(rows, obj{"kind": kind, "timestamp": "2026-11-09T20:00:00Z"})
	}
	return page(rows...)
}

func events(times ...string) obj {
	rows := []any{}
	for _, at := range times {
		rows = append(rows, obj{"timestamp": at, "ingested_at": ingested})
	}
	return page(rows...)
}

func historyAt(id string) string { return "/api/canonical/0/issue-history/?issue=" + id }
func eventsAt(id string) string  { return "/api/canonical/0/events/?issue=" + id }
func tagsAt(id string) string    { return "/api/canonical/0/issues/" + id + "/tags/" }

// bugsinkApi has five open issues: one that files, one a count short, one muted, one resolved,
// and one the shipper never tagged.
func bugsinkApi(t *testing.T) *fakeapi.Api {
	t.Helper()
	api := fakeapi.Serve(t)
	api.Route(list, page(
		listed(loud, 5, false, false),
		listed(quiet, 2, false, false),
		listed(muted, 50, false, true),
		listed(resolved, 50, true, false),
		listed(untagged, 50, false, false),
	))
	for id, class := range map[string]string{loud: "approval-timeout", quiet: "approval-timeout", muted: "mcp-timeout", resolved: "mcp-timeout"} {
		api.Route(tagsAt(id), tagged(class))
	}
	api.Route(tagsAt(untagged), page())
	api.Route(historyAt(loud), history("first_seen"))
	api.Route(eventsAt(loud), events("2026-10-30T15:54:40.045000Z", "2026-09-03T01:48:25.567000Z", "2026-10-19T14:04:13.275000Z"))
	return api
}

// scanner is what a scan reads: an empty transcript directory, no shipper, and Bugsink and
// maybe GitHub at api.
type scanner struct {
	api    *fakeapi.Api
	github bool
	live   bool
	nodsn  bool
}

func (s scanner) providers(t *testing.T) (*providers.Set, error) {
	t.Helper()
	set := providers.New()
	providers.Provide(set, brine.Transcripts{Dir: t.TempDir()})
	providers.Provide(set, brine.BugsinkClient{})
	reader := brine.BugsinkReader{}
	if !s.nodsn {
		dsn, err := bugsink.ParseDsn("http://0123abcd@" + strings.TrimPrefix(s.api.Origin, "http://") + "/7")
		if err != nil {
			return nil, err
		}
		reading := &brine.Reading{Bugsink: cutover.NewBugsink(dsn, "api-token"), Live: s.live}
		if s.github {
			reading.Github = cutover.NewGithubAt(s.api.Origin, "gh-token")
		}
		reader.Reading = reading
	}
	providers.Provide(set, reader)
	return set, nil
}

func (s scanner) scan(t *testing.T) ([]workflow.Found, error) {
	t.Helper()
	set, err := s.providers(t)
	if err != nil {
		t.Fatal(err)
	}
	w, err := bugsinkissues.New()
	if err != nil {
		t.Fatal(err)
	}
	return w.Scan(context.Background(), workflow.NewScanner(set))
}

func (s scanner) mustScan(t *testing.T) []workflow.Found {
	t.Helper()
	found, err := s.scan(t)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func files(t *testing.T, found []workflow.Found) []effect.File {
	t.Helper()
	var out []effect.File
	for _, f := range found {
		if f.Kind != "bugsink.issue.unaddressed" {
			t.Fatalf("kind %q", f.Kind)
		}
		out = append(out, filing(t, f.Payload))
	}
	return out
}

func TestAScanFindsOnlyTheIssueThatIsLoudUnresolvedAndUnmuted(t *testing.T) {
	found := scanner{api: bugsinkApi(t)}.mustScan(t)
	filed := files(t, found)
	if len(filed) != 1 {
		t.Fatalf("%+v", found)
	}
	if filed[0].Target.String() != "technicalpickles/brineworks" || filed[0].Title.String() != "Brent: brineworks/triage_apply approval-timeout (5 events)" {
		t.Errorf("%s: %q", filed[0].Target, filed[0].Title)
	}
}

func TestTheFiledBodyDatesTheFailuresByTheirEventsAndNotByIngest(t *testing.T) {
	body := textOf(t, files(t, scanner{api: bugsinkApi(t)}.mustScan(t))[0].Body)
	if !strings.Contains(body, "\nevents: 5\nfirst seen: 2026-09-03T01:48:25.567000Z\nlast seen: 2026-10-30T15:54:40.045000Z\n") {
		t.Error(body)
	}
	if strings.Contains(body, ingested) {
		t.Error(body)
	}
}

// twoLoud has two issues over the threshold: loud, whose events are fine, and second, whose
// events are whatever the test says. It returns the bodies filed for second and for loud.
func twoLoud(t *testing.T, secondEvents obj) (string, string) {
	t.Helper()
	api := fakeapi.Serve(t)
	api.Route(list, page(listed(second, 5, false, false), listed(loud, 5, false, false)))
	for _, id := range []string{second, loud} {
		api.Route(tagsAt(id), tagged("approval-timeout"))
		api.Route(historyAt(id), history("first_seen"))
	}
	api.Route(eventsAt(second), secondEvents)
	api.Route(eventsAt(loud), events("2026-09-03T01:48:25.567000Z", "2026-10-30T15:54:40.045000Z"))
	filed := files(t, scanner{api: api}.mustScan(t))
	if len(filed) != 2 {
		t.Fatalf("filed %d", len(filed))
	}
	return textOf(t, filed[0].Body), textOf(t, filed[1].Body)
}

const byIngest = "\nfirst seen: 2026-11-10T03:12:13.710632Z (Bugsink ingest time)\nlast seen: 2026-11-10T03:12:13.710632Z (Bugsink ingest time)\n"

func TestAnIssueWhoseEventsWereEvictedIsFiledByIngestTimeAndDoesNotStopTheScan(t *testing.T) {
	evicted, normal := twoLoud(t, events())
	if !strings.Contains(evicted, byIngest) {
		t.Error(evicted)
	}
	if !strings.Contains(normal, "\nfirst seen: 2026-09-03T01:48:25.567000Z\nlast seen: 2026-10-30T15:54:40.045000Z\n") || strings.Contains(normal, "ingest time") {
		t.Error(normal)
	}
}

func TestAnIssueWithAnUnreadableEventTimeIsFiledByIngestTimeAndDoesNotStopTheScan(t *testing.T) {
	garbled, normal := twoLoud(t, events("2026-10-01T00:00:00Z", "yesterday"))
	if !strings.Contains(garbled, byIngest) {
		t.Error(garbled)
	}
	if strings.Contains(normal, "ingest time") {
		t.Error(normal)
	}
}

func TestHistoryIsReadOnlyForIssuesThatPassTheThreshold(t *testing.T) {
	api := bugsinkApi(t)
	scanner{api: api}.mustScan(t)
	var histories []string
	for _, target := range api.Targets() {
		if strings.Contains(target, "issue-history") {
			histories = append(histories, target)
		}
	}
	if len(histories) != 1 || !strings.HasSuffix(histories[0], loud) {
		t.Fatalf("%v", histories)
	}
}

func TestWithoutATokenOrDsnTheScanFindsNothing(t *testing.T) {
	if found := (scanner{api: fakeapi.Serve(t), nodsn: true}).mustScan(t); len(found) != 0 {
		t.Fatalf("%+v", found)
	}
}

func TestAFailedReadOfBugsinkFailsTheScan(t *testing.T) {
	if _, err := (scanner{api: fakeapi.Serve(t)}).scan(t); err == nil {
		t.Fatal("the scan passed with no Bugsink to read")
	}
}

const (
	brineworksIssues = "/repos/technicalpickles/brineworks/issues?labels=almanac&state=all&per_page=100&page=1"
	pickleclawIssues = "/repos/technicalpickles/pickleclaw/issues?labels=almanac&state=all&per_page=100&page=1"
)

type closure struct {
	at     string
	reason any
}

var (
	completed  = &closure{"2026-11-06T09:00:00Z", "completed"}
	notPlanned = &closure{"2026-11-06T09:00:00Z", "not_planned"}
)

// almanacIssue is one almanac issue as GitHub lists it, whose body links the Bugsink issue id.
func almanacIssue(number int, id string, generation int, created string, closed *closure) obj {
	regressions := ""
	if generation > 0 {
		regressions = fmt.Sprintf("regressions: %d (came back after being resolved)\n", generation)
	}
	issue := obj{
		"html_url":     fmt.Sprintf("https://github.com/example/repo/issues/%d", number),
		"number":       number,
		"state":        "open",
		"state_reason": nil,
		"created_at":   created,
		"closed_at":    nil,
		"body": "```text\nservice: brineworks\ntool: triage_apply\nclass: approval-timeout\n" + regressions +
			"bugsink: http://bugsink.example/issues/issue/" + id + "/event/last/\n\nreason:\nboom\n```\n",
	}
	if closed != nil {
		issue["state"], issue["closed_at"], issue["state_reason"] = "closed", closed.at, closed.reason
	}
	return issue
}

// onGithub serves the almanac issues of the first repository and none for the second.
func onGithub(api *fakeapi.Api, issues ...obj) {
	rows := make([]any, len(issues))
	for i, issue := range issues {
		rows[i] = issue
	}
	api.Route(brineworksIssues, rows)
	api.Route(pickleclawIssues, []any{})
}

func acceptChanges(api *fakeapi.Api, id string) {
	api.Route("/api/canonical/0/issue-comments/", obj{})
	for _, action := range []string{"resolve", "mute-until"} {
		api.Route("/api/canonical/0/issues/"+id+"/"+action+"/", obj{})
	}
}

func changes(api *fakeapi.Api) []string {
	var targets []string
	for _, post := range api.Posts() {
		targets = append(targets, post.Target)
	}
	return targets
}

func liveScan(t *testing.T, api *fakeapi.Api) []workflow.Found {
	t.Helper()
	return scanner{api: api, github: true, live: true}.mustScan(t)
}

func TestClosingAsCompletedCommentsThenResolvesAndFilesNothing(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	onGithub(api, almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", completed))

	if found := liveScan(t, api); len(found) != 0 {
		t.Fatalf("%+v", found)
	}

	want := []string{"/api/canonical/0/issue-comments/", "/api/canonical/0/issues/" + loud + "/resolve/"}
	if got := changes(api); !slices.Equal(got, want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
	comment := api.Posts()[0]
	if !strings.Contains(comment.Body, loud) || !strings.Contains(comment.Body, "https://github.com/example/repo/issues/7") {
		t.Error(comment.Body)
	}
	if comment.Auth != "Bearer api-token" {
		t.Error(comment.Auth)
	}
}

func TestClosingAsNotPlannedMutesUntilABurstOfEvents(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	onGithub(api, almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", notPlanned))

	if found := liveScan(t, api); len(found) != 0 {
		t.Fatalf("%+v", found)
	}

	want := []string{"/api/canonical/0/issue-comments/", "/api/canonical/0/issues/" + loud + "/mute-until/"}
	if got := changes(api); !slices.Equal(got, want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
	var wake map[string]any
	if err := json.Unmarshal([]byte(api.Posts()[1].Body), &wake); err != nil {
		t.Fatal(err)
	}
	if wake["period_name"] != "day" || wake["nr_of_periods"] != 1.0 || wake["gte_threshold"] != 50.0 || len(wake) != 3 {
		t.Errorf("%v", wake)
	}
}

func TestADryRunSaysWhatItWouldDoAndChangesNothing(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	onGithub(api, almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", completed))
	if found := (scanner{api: api, github: true}).mustScan(t); len(found) != 0 {
		t.Fatalf("%+v", found)
	}
	if got := changes(api); len(got) != 0 {
		t.Fatalf("a dry run changed %v", got)
	}
}

func TestARegressionIsFiledAndNotResolvedAgainByTheOldClose(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	first := almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", completed)
	onGithub(api, first)
	liveScan(t, api)
	if got := len(api.Posts()); got != 2 {
		t.Fatalf("%d changes", got)
	}

	api.Route(historyAt(loud), history("regressed", "resolved", "first_seen"))
	found := liveScan(t, api)
	if got := len(api.Posts()); got != 2 {
		t.Fatalf("the closed issue is at generation 0, but %d changes", got)
	}
	filed := files(t, found)
	if len(filed) != 1 || filed[0].Title.String() != "Brent: brineworks/triage_apply approval-timeout regressed (5 events)" {
		t.Fatalf("%+v", filed)
	}

	onGithub(api, first, almanacIssue(8, loud, 1, "2026-11-09T09:00:00Z", nil))
	liveScan(t, api)
	if got := len(api.Posts()); got != 2 {
		t.Fatalf("the newest almanac issue is open, but %d changes", got)
	}

	onGithub(api, first, almanacIssue(8, loud, 1, "2026-11-09T09:00:00Z", completed))
	liveScan(t, api)
	if got := len(api.Posts()); got != 4 {
		t.Fatalf("closing the regression resolves it, but %d changes", got)
	}
}

func TestACloseWithNoReasonIsLeftToFileAsBefore(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	onGithub(api, almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", &closure{at: "2026-11-06T09:00:00Z"}))
	if found := liveScan(t, api); len(found) != 1 {
		t.Fatalf("%+v", found)
	}
	if got := changes(api); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestAnIssueThatWokeAfterItsNotPlannedCloseIsNotMutedAgain(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	onGithub(api, almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", notPlanned))
	api.Route(historyAt(loud), page(
		obj{"kind": "unmuted", "timestamp": "2026-11-09T21:00:00Z"},
		obj{"kind": "muted", "timestamp": "2026-11-06T10:00:00Z"},
		obj{"kind": "first_seen", "timestamp": "2026-11-04T10:00:00Z"},
	))
	liveScan(t, api)
	if got := changes(api); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestAnIssueNobodyListsAsOpenIsNeverActedOn(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, resolved)
	onGithub(api, almanacIssue(7, resolved, 0, "2026-11-05T09:00:00Z", completed))
	liveScan(t, api)
	if got := changes(api); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	for _, target := range api.Targets() {
		if strings.Contains(target, "issue-history") && strings.HasSuffix(target, resolved) {
			t.Fatalf("read the history of an issue nobody lists as open: %s", target)
		}
	}
}

func TestWithoutGithubTheScanStillFilesAndChangesNothing(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	if found := (scanner{api: api, live: true}).mustScan(t); len(found) != 1 {
		t.Fatalf("%+v", found)
	}
	if got := changes(api); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestAFailedReadOfGithubSkipsResolvingAndTheScanStillFiles(t *testing.T) {
	api := bugsinkApi(t)
	acceptChanges(api, loud)
	if found := liveScan(t, api); len(found) != 1 {
		t.Fatalf("%+v", found)
	}
	if got := changes(api); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestAFailedResolveIsReportedAndTheScanGoesOn(t *testing.T) {
	api := bugsinkApi(t)
	api.Route("/api/canonical/0/issue-comments/", obj{})
	onGithub(api, almanacIssue(7, loud, 0, "2026-11-05T09:00:00Z", completed))
	if _, err := (scanner{api: api, github: true, live: true}).scan(t); err != nil {
		t.Fatal(err)
	}
	if got := len(api.Posts()); got != 2 {
		t.Fatalf("the resolve was tried after the comment, but %d changes", got)
	}
}
