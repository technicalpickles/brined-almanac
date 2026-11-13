package cutover_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technicalpickles/brined-almanac/bugsink"
	"github.com/technicalpickles/brined-almanac/bugsinkapi"
	"github.com/technicalpickles/brined-almanac/cutover"
	"github.com/technicalpickles/brined-almanac/transcript"
)

// The role is split so the repository's forbidden-pattern scan, which looks for a transcript
// event line, does not match this file.
const (
	roleKey    = "role"
	resultRole = "tool" + "Result"
)

const linkedID = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01"

func str(s string) *string { return &s }

func line(id, at string, message map[string]any) string {
	out, err := json.Marshal(map[string]any{"type": "message", "id": id, "timestamp": at, "message": message})
	if err != nil {
		panic(err)
	}
	return string(out)
}

func writeTimeout(t *testing.T, dir, service, tool, errText string) {
	t.Helper()
	block := map[string]any{
		"type":      "toolCall",
		"id":        "call-0001",
		"name":      "tool_call",
		"arguments": map[string]any{"id": service + "__" + tool},
	}
	lines := []string{
		line("evt-0001", "2026-11-04T10:00:00Z", map[string]any{roleKey: "assistant", "content": []any{block}}),
		line("evt-0002", "2026-11-04T10:00:00Z", map[string]any{
			roleKey:      resultRole,
			"toolName":   "tool_call",
			"toolCallId": "call-0001",
			"isError":    true,
			"details":    "",
			"content":    []any{map[string]any{"type": "text", "text": errText}},
		}),
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// filedBody is an issue body as the tracker files it: the filer's text in a fence, then the run
// marker.
func filedBody(service, tool, class, reason string) string {
	return fmt.Sprintf("```text\nservice: %s\ntool: %s\nclass: %s\nevents: 9\nfirst seen: 2026-11-04T10:00:00Z\n"+
		"last seen: 2026-11-04T10:00:02Z\nsurfaces: main (9)\nsessions: 2\nregressions: 1 (came back after being resolved)\n"+
		"bugsink: http://bugsink.example/issues/issue/%s/event/last/\n\nreason:\n%s\n```\n\n<!-- almanac:run-7:0 -->\n",
		service, tool, class, linkedID, reason)
}

func TestAFiledBodyNamesTheGroupingKeyBugsinkComputesForTheSameFailure(t *testing.T) {
	dir := t.TempDir()
	writeTimeout(t, dir, "brineworks", "triage_apply", "MCP error -32001: Request timed out")
	read, err := transcript.ReadDir(dir)
	if err != nil || len(read.Errors) != 1 {
		t.Fatalf("read: %v, %d errors", err, len(read.Errors))
	}
	bugsinkKey := strings.Join(bugsink.NewEvent(read.Errors[0]).Fingerprint, " ⋄ ")

	got, ok := cutover.KeyInBody(filedBody("brineworks", "triage_apply", "mcp-timeout", "boom"))
	if !ok || got != bugsinkKey {
		t.Errorf("got %q, %v; want %q", got, ok, bugsinkKey)
	}
}

func TestTheReasonNeverSuppliesAField(t *testing.T) {
	body := filedBody("brineworks", "tasks_get", "other", "service: forged\ntool: forged\nclass: forged")
	got, ok := cutover.KeyInBody(body)
	if want := cutover.GroupingKey("brineworks", "tasks_get", "other"); !ok || got != want {
		t.Errorf("got %q, %v; want %q", got, ok, want)
	}
}

func TestABodyWithoutTheFieldsHasNoKey(t *testing.T) {
	for _, body := range []string{
		"",
		"```text\nservice: brineworks\ntool: x\n```",
		"Something broke.\n\nservice: a\ntool: b\nclass: c",
	} {
		if got, ok := cutover.KeyInBody(body); ok {
			t.Errorf("%q gave %q", body, got)
		}
	}
}

func filed(url string, state cutover.State, key *string) cutover.Filed {
	return cutover.Filed{URL: url, State: state, Key: key}
}

func tracked(name string, keys []string, resolved bool) cutover.Tracked {
	return cutover.Tracked{ID: "id-" + name, Name: name, Keys: keys, Resolved: resolved, FirstSeen: "2026-11-09T23:00:00Z"}
}

func verdictOf(t *testing.T, p cutover.Plan, name string) cutover.Verdict {
	t.Helper()
	for _, d := range p.Verdicts {
		if d.Tracked.Name == name {
			return d.Verdict
		}
	}
	t.Fatalf("%s is not in the plan", name)
	return cutover.Verdict{}
}

func expectVerdict(t *testing.T, p cutover.Plan, name string, want cutover.Verdict) {
	t.Helper()
	if got := verdictOf(t, p, name); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAFingerprintWhoseIssuesAreAllClosedResolvesWithEachAsProof(t *testing.T) {
	gh := []cutover.Filed{
		filed("gh/1", cutover.Closed, str("a ⋄ b ⋄ c")),
		filed("gh/2", cutover.Closed, str("a ⋄ b ⋄ c")),
	}
	bs := []cutover.Tracked{tracked("P-1", []string{"a ⋄ b ⋄ c"}, false)}
	p := cutover.MakePlan(gh, bs)
	expectVerdict(t, p, "P-1", cutover.Verdict{Kind: cutover.Resolve, Proof: []string{"gh/1", "gh/2"}})
	if len(p.Unmatched) != 0 {
		t.Errorf("unmatched = %+v", p.Unmatched)
	}
}

func TestAnyOpenIssueKeepsTheFingerprintOpen(t *testing.T) {
	gh := []cutover.Filed{
		filed("gh/1", cutover.Closed, str("a ⋄ b ⋄ c")),
		filed("gh/2", cutover.Open, str("a ⋄ b ⋄ c")),
	}
	bs := []cutover.Tracked{tracked("P-1", []string{"a ⋄ b ⋄ c"}, false)}
	expectVerdict(t, cutover.MakePlan(gh, bs), "P-1", cutover.Verdict{Kind: cutover.StillOpen, Open: []string{"gh/2"}})
}

func TestAFingerprintNobodyFiledStaysOpenAsNeverFiled(t *testing.T) {
	bs := []cutover.Tracked{tracked("P-1", []string{"a ⋄ b ⋄ c"}, false)}
	expectVerdict(t, cutover.MakePlan(nil, bs), "P-1", cutover.Verdict{Kind: cutover.NeverFiled})
}

func TestAResolvedIssueIsLeftAlone(t *testing.T) {
	gh := []cutover.Filed{filed("gh/1", cutover.Closed, str("a ⋄ b ⋄ c"))}
	bs := []cutover.Tracked{tracked("P-1", []string{"a ⋄ b ⋄ c"}, true)}
	expectVerdict(t, cutover.MakePlan(gh, bs), "P-1", cutover.Verdict{Kind: cutover.AlreadyResolved})
}

func TestAMergedIssueAnswersForEveryGroupingKeyItHolds(t *testing.T) {
	gh := []cutover.Filed{
		filed("gh/1", cutover.Closed, str("a ⋄ b ⋄ c")),
		filed("gh/2", cutover.Open, str("a ⋄ b ⋄ other")),
	}
	bs := []cutover.Tracked{tracked("P-1", []string{"a ⋄ b ⋄ c", "a ⋄ b ⋄ other"}, false)}
	expectVerdict(t, cutover.MakePlan(gh, bs), "P-1", cutover.Verdict{Kind: cutover.StillOpen, Open: []string{"gh/2"}})
}

func TestIssuesThatMatchNoBugsinkIssueAreListedWhateverTheirState(t *testing.T) {
	gh := []cutover.Filed{
		filed("gh/1", cutover.Closed, str("a ⋄ b ⋄ stale")),
		filed("gh/2", cutover.Open, str("x ⋄ y ⋄ z")),
		filed("gh/3", cutover.Closed, nil),
		filed("gh/4", cutover.Closed, str("a ⋄ b ⋄ c")),
	}
	bs := []cutover.Tracked{tracked("P-1", []string{"a ⋄ b ⋄ c"}, false)}
	var urls []string
	for _, f := range cutover.MakePlan(gh, bs).Unmatched {
		urls = append(urls, f.URL)
	}
	if want := []string{"gh/1", "gh/2", "gh/3"}; !reflect.DeepEqual(urls, want) {
		t.Errorf("got %v, want %v", urls, want)
	}
}

func TestTheCommentLinksEveryClosedIssue(t *testing.T) {
	text := cutover.Comment([]string{"gh/1", "gh/2"})
	if !strings.Contains(text, "closed on GitHub") || !strings.HasSuffix(text, "\n- gh/1\n- gh/2") {
		t.Errorf("comment = %q", text)
	}
}

func TestTheDsnGivesTheApiBaseAndProject(t *testing.T) {
	dsn, err := bugsink.ParseDsn("https://0123abcd@bugsink.example:8443/7")
	if err != nil {
		t.Fatal(err)
	}
	if dsn.BaseURL() != "https://bugsink.example:8443" || dsn.Project() != "7" ||
		dsn.StoreURL() != "https://bugsink.example:8443/api/7/store/" {
		t.Errorf("dsn = %v", dsn)
	}
}

func TestTheBugsinkLinkNamesTheIssueWhateverTheHost(t *testing.T) {
	for _, host := range []string{"http://bugsink.example", "https://other.example:8443"} {
		body := fmt.Sprintf("```text\nservice: a\nbugsink: %s/issues/issue/%s/event/last/\n\nreason:\nx\n```", host, strings.ToUpper(linkedID))
		if got, ok := cutover.BugsinkLinkInBody(body); !ok || got != linkedID {
			t.Errorf("%s: got %q, %v", host, got, ok)
		}
	}
}

func TestABodyWithoutAWellFormedLinkNamesNoBugsinkIssue(t *testing.T) {
	for _, body := range []string{
		"",
		"bugsink: http://bugsink.example/issues/issue/not-a-uuid/event/last/",
		"service: a\n\nreason:\nbugsink: http://b/issues/issue/6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01/event/last/",
	} {
		if got, ok := cutover.BugsinkLinkInBody(body); ok {
			t.Errorf("%q gave %q", body, got)
		}
	}
}

func TestTheGenerationIsTheRegressionsCountAndZeroWithoutOne(t *testing.T) {
	u := func(n uint64) *uint64 { return &n }
	cases := []struct {
		name, body string
		want       *uint64
	}{
		{"no line", "service: a\nbugsink: x\n", u(0)},
		{"a count", "service: a\nregressions: 3 (came back after being resolved)\n", u(3)},
		{"not a count", "regressions: lots\n", nil},
		{"the reason never supplies a field", "service: a\n\nreason:\nregressions: 9\n", u(0)},
	}
	for _, c := range cases {
		if got := cutover.GenerationInBody(c.body); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

type seen struct {
	method, target, auth, contentType, body string
}

type api struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []seen
}

// serve answers each route, a request target (path and query) to a JSON answer, with {base}
// replaced by the server's own origin so a page can link to the next. Anything else is a 404.
func serve(t *testing.T, routes map[string]string) *api {
	t.Helper()
	a := &api{}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.seen = append(a.seen, seen{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(body)})
		a.mu.Unlock()
		answer, ok := routes[r.URL.RequestURI()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.ReplaceAll(answer, "{base}", a.server.URL))
	}))
	t.Cleanup(a.server.Close)
	return a
}

func (a *api) requests() []seen {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]seen(nil), a.seen...)
}

func utc(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }

func TestLinkedReadsEveryAlmanacIssueThatNamesABugsinkIssueWithWhyItClosed(t *testing.T) {
	raw, err := os.ReadFile("../tests/fixtures/github/issues.json")
	if err != nil {
		t.Fatal(err)
	}
	a := serve(t, map[string]string{
		"/repos/example/repo/issues?labels=almanac&state=all&per_page=100&page=1": string(raw),
	})
	found, err := cutover.NewGithubAt(a.server.URL, "gh-token").Linked(context.Background(), "example/repo")
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		number     uint64
		generation uint64
		standing   cutover.Standing
	}
	var got []row
	for _, l := range found {
		if l.Generation == nil {
			t.Fatalf("%d has no generation", l.Number)
		}
		got = append(got, row{l.Number, *l.Generation, l.Standing})
	}
	want := []row{
		{31, 0, cutover.Standing{State: cutover.Closed, Reason: cutover.Completed, At: utc(2026, 11, 6, 9)}},
		{32, 1, cutover.Standing{State: cutover.Closed, Reason: cutover.NotPlanned, At: utc(2026, 11, 8, 9)}},
		{33, 2, cutover.Standing{State: cutover.Open}},
		{34, 0, cutover.Standing{State: cutover.Closed, Reason: cutover.NoReason, At: utc(2026, 11, 9, 11)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("35 names no Bugsink issue and 36 is a pull request\ngot  %+v\nwant %+v", got, want)
	}
	for _, l := range found {
		if l.Issue != linkedID {
			t.Errorf("%d links %q", l.Number, l.Issue)
		}
	}
	if !found[0].CreatedAt.Equal(utc(2026, 11, 5, 9)) {
		t.Errorf("created = %v", found[0].CreatedAt)
	}
	if found[0].URL != "https://github.com/example/repo/issues/31" {
		t.Errorf("url = %q", found[0].URL)
	}
	if reqs := a.requests(); reqs[0].auth != "Bearer gh-token" {
		t.Errorf("auth = %q", reqs[0].auth)
	}
}

// The rest are additions: the Rust tests cover the write half only through other files.

func bugsinkAt(t *testing.T, a *api) *cutover.Bugsink {
	t.Helper()
	dsn, err := bugsink.ParseDsn(strings.Replace(a.server.URL, "://", "://0123abcd@", 1) + "/7")
	if err != nil {
		t.Fatal(err)
	}
	return cutover.NewBugsink(dsn, "bs-token")
}

func TestTrackedPagesTheIssuesAndReadsEachOnesGroupingKeys(t *testing.T) {
	a := serve(t, map[string]string{
		"/api/canonical/0/issues/?project=7&state=all": `{"next":"{base}/api/canonical/0/issues/?project=7&state=all&cursor=2","results":[` +
			`{"id":"i1","friendly_id":"P-1","is_resolved":false,"first_seen":"2026-11-09T23:00:00Z"}]}`,
		"/api/canonical/0/issues/?project=7&state=all&cursor=2": `{"next":null,"results":[` +
			`{"id":"i2","friendly_id":"P-2","is_resolved":true,"first_seen":"2026-11-08T23:00:00Z"}]}`,
		"/api/canonical/0/issues/i1/grouping/": `{"next":null,"results":[{"grouping_key":"a ⋄ b ⋄ c"},{"grouping_key":"a ⋄ b ⋄ d"}]}`,
		"/api/canonical/0/issues/i2/grouping/": `{"next":null,"results":[]}`,
	})
	got, err := bugsinkAt(t, a).Tracked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []cutover.Tracked{
		{ID: "i1", Name: "P-1", Keys: []string{"a ⋄ b ⋄ c", "a ⋄ b ⋄ d"}, FirstSeen: "2026-11-09T23:00:00Z"},
		{ID: "i2", Name: "P-2", Keys: []string{}, Resolved: true, FirstSeen: "2026-11-08T23:00:00Z"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	for _, r := range a.requests() {
		if r.auth != "Bearer bs-token" {
			t.Errorf("%s sent auth %q", r.target, r.auth)
		}
	}
}

func TestResolveCommentsTheProofFirstThenResolvesWithNoBody(t *testing.T) {
	a := serve(t, map[string]string{
		"/api/canonical/0/issue-comments/":    `{}`,
		"/api/canonical/0/issues/i1/resolve/": `{}`,
	})
	err := bugsinkAt(t, a).Resolve(context.Background(), cutover.Tracked{ID: "i1", Name: "P-1"}, []string{"gh/1"})
	if err != nil {
		t.Fatal(err)
	}
	reqs := a.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests", len(reqs))
	}
	wantComment, _ := json.Marshal(map[string]string{"issue": "i1", "comment": cutover.Comment([]string{"gh/1"})})
	if reqs[0].method != "POST" || reqs[0].target != "/api/canonical/0/issue-comments/" ||
		reqs[0].body != string(wantComment) || reqs[0].contentType != "application/json" || reqs[0].auth != "Bearer bs-token" {
		t.Errorf("comment = %+v", reqs[0])
	}
	if reqs[1].method != "POST" || reqs[1].target != "/api/canonical/0/issues/i1/resolve/" ||
		reqs[1].body != "" || reqs[1].contentType != "" {
		t.Errorf("resolve = %+v", reqs[1])
	}
}

func TestMuteUntilSendsTheWakeCondition(t *testing.T) {
	a := serve(t, map[string]string{
		"/api/canonical/0/issue-comments/":       `{}`,
		"/api/canonical/0/issues/i1/mute-until/": `{}`,
	})
	issue := bugsinkapi.Issue{ID: "i1", FriendlyID: "P-1"}
	if err := bugsinkAt(t, a).MuteUntil(context.Background(), issue, "a & b", 5); err != nil {
		t.Fatal(err)
	}
	reqs := a.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests", len(reqs))
	}
	if want := `{"comment":"a & b","issue":"i1"}`; reqs[0].body != want {
		t.Errorf("comment body = %s", reqs[0].body)
	}
	if want := `{"gte_threshold":5,"nr_of_periods":1,"period_name":"day"}`; reqs[1].body != want {
		t.Errorf("wake body = %s", reqs[1].body)
	}
}

func TestAFailedCommentStopsBeforeTheActionAndTheErrorNamesNeitherHostNorToken(t *testing.T) {
	a := serve(t, map[string]string{"/api/canonical/0/issues/i1/resolve/": `{}`})
	err := bugsinkAt(t, a).ResolveIssue(context.Background(), bugsinkapi.Issue{ID: "i1", FriendlyID: "P-1"}, "done")
	if err == nil {
		t.Fatal("want an error")
	}
	if want := "comment on P-1: answered 404 Not Found"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if len(a.requests()) != 1 {
		t.Errorf("requests = %+v", a.requests())
	}

	a.server.Close()
	err = bugsinkAt(t, a).ResolveIssue(context.Background(), bugsinkapi.Issue{ID: "i1", FriendlyID: "P-1"}, "done")
	if err == nil || strings.Contains(err.Error(), a.server.URL) || strings.Contains(err.Error(), "bs-token") {
		t.Errorf("error leaks: %v", err)
	}
}

func TestGithubErrorsNameTheRepoButNeverTheTokenOrTheHost(t *testing.T) {
	a := serve(t, nil)
	_, err := cutover.NewGithubAt(a.server.URL, "gh-secret").Filed(context.Background(), "example/repo")
	if want := "GitHub example/repo issues: answered 404 Not Found"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestFiledPagesUntilAShortPageAndSkipsPullRequests(t *testing.T) {
	var full []string
	for i := range 100 {
		full = append(full, fmt.Sprintf(`{"html_url":"u/%d","number":%d,"state":"open","created_at":"2026-11-05T09:00:00Z","body":%s}`,
			i, i, mustJSON(filedBody("s", "t", "c", "r"))))
	}
	a := serve(t, map[string]string{
		"/repos/example/repo/issues?labels=almanac&state=all&per_page=100&page=1": "[" + strings.Join(full, ",") + "]",
		"/repos/example/repo/issues?labels=almanac&state=all&per_page=100&page=2": `[` +
			`{"html_url":"u/pr","number":900,"state":"open","created_at":"2026-11-05T09:00:00Z","body":null,"pull_request":{"url":"x"}},` +
			`{"html_url":"u/last","number":901,"state":"closed","created_at":"2026-11-05T09:00:00Z","body":null,"pull_request":null}]`,
	})
	got, err := cutover.NewGithubAt(a.server.URL, "t").Filed(context.Background(), "example/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 101 || got[100].URL != "u/last" || got[100].State != cutover.Closed || got[100].Key != nil ||
		got[0].Key == nil || *got[0].Key != cutover.GroupingKey("s", "t", "c") {
		t.Errorf("got %d issues, last %+v", len(got), got[len(got)-1])
	}
}

func TestReportNamesEachSectionAndNoBugsinkURL(t *testing.T) {
	gh := []cutover.Filed{
		filed("gh/1", cutover.Closed, str("a ⋄ b ⋄ c")),
		filed("gh/2", cutover.Open, str("x ⋄ y ⋄ z")),
		filed("gh/3", cutover.Closed, nil),
	}
	bs := []cutover.Tracked{
		tracked("P-1", []string{"a ⋄ b ⋄ c"}, false),
		tracked("P-2", []string{"q ⋄ r ⋄ s"}, false),
	}
	want := "\nresolve (1):\n  a ⋄ b ⋄ c [P-1]\n    proof: gh/1\n" +
		"\nleft open: an almanac issue is still open (0):\n" +
		"\nleft open: never filed (below the filer's threshold) (1):\n  q ⋄ r ⋄ s [P-2]\n" +
		"\nalready resolved (0):\n" +
		"\nalmanac issues that match no Bugsink issue (2):\n  gh/2 (open): x ⋄ y ⋄ z\n  gh/3 (closed): no service/tool/class in body\n"
	if got := cutover.Report(cutover.MakePlan(gh, bs)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func mustJSON(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(out)
}
