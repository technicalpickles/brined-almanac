package bugsinkapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technicalpickles/brined-almanac/bugsinkapi"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../tests/fixtures/bugsink/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func page[T any](t *testing.T, name string) []T {
	t.Helper()
	var p struct {
		Results []T `json:"results"`
	}
	if err := json.Unmarshal([]byte(fixture(t, name)), &p); err != nil {
		t.Fatal(err)
	}
	return p.Results
}

func tag(key, value string, count uint64) bugsinkapi.Tag {
	return bugsinkapi.Tag{Key: key, Value: value, Count: count}
}

func turning(kind string) bugsinkapi.TurningPoint {
	return bugsinkapi.TurningPoint{Kind: kind, Timestamp: "2026-11-09T20:00:00Z"}
}

func TestAnIssueReadsTheFieldsTheConsumerNeeds(t *testing.T) {
	issues := page[bugsinkapi.Issue](t, "issues.json")
	if len(issues) != 2 {
		t.Fatalf("got %d issues", len(issues))
	}
	first := issues[0]
	if first.ID != "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01" ||
		first.FriendlyID != "BRENT-12" ||
		first.Count != 14 ||
		first.Reason != "MCP error -32001: Request timed out" {
		t.Errorf("first = %+v", first)
	}
	if first.Resolved || first.Muted {
		t.Errorf("first should be open and unmuted: %+v", first)
	}
	if !issues[1].Resolved || !issues[1].Muted {
		t.Errorf("second should be resolved and muted: %+v", issues[1])
	}
}

func TestTheLabelsComeFromTheTags(t *testing.T) {
	got, err := bugsinkapi.LabelsOf(page[bugsinkapi.Tag](t, "tags.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := bugsinkapi.Labels{
		Service:  "brineworks",
		Tool:     "triage_apply",
		Class:    "mcp-timeout",
		Surfaces: []bugsinkapi.Surface{{Name: "telegram", Count: 11}, {Name: "main", Count: 3}},
		Sessions: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAnIssueMissingATagIsNamedInTheError(t *testing.T) {
	_, err := bugsinkapi.LabelsOf([]bugsinkapi.Tag{tag("service", "brineworks", 1), tag("tool", "t", 1)})
	if err == nil || err.Error() != "no class tag" {
		t.Errorf("err = %v", err)
	}
}

func TestOneSessionAcrossTwoTagRowsCountsOnce(t *testing.T) {
	got, err := bugsinkapi.LabelsOf([]bugsinkapi.Tag{
		tag("service", "s", 2),
		tag("tool", "t", 2),
		tag("class", "other", 2),
		tag("session", "aaaaaaaaaaaa", 1),
		tag("session", "aaaaaaaaaaaa", 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions != 1 {
		t.Errorf("sessions = %d", got.Sessions)
	}
}

func TestGenerationCountsOnlyRegressionsAndReopenings(t *testing.T) {
	if got := bugsinkapi.Generation(page[bugsinkapi.TurningPoint](t, "history.json")); got != 2 {
		t.Errorf("fixture generation = %d", got)
	}
	if got := bugsinkapi.Generation([]bugsinkapi.TurningPoint{turning("first_seen")}); got != 0 {
		t.Errorf("first_seen generation = %d", got)
	}
	quiet := []bugsinkapi.TurningPoint{
		turning("first_seen"),
		turning("muted"),
		turning("unmuted"),
		turning("resolved"),
		turning("next_materialized"),
		turning("manual_annotation"),
	}
	if got := bugsinkapi.Generation(quiet); got != 0 {
		t.Errorf("quiet generation = %d", got)
	}
}

func TestTheLastUnmuteIsTheLatestOneWhoeverDidIt(t *testing.T) {
	at := func(kind, timestamp string) bugsinkapi.TurningPoint {
		return bugsinkapi.TurningPoint{Kind: kind, Timestamp: timestamp}
	}
	none, err := bugsinkapi.LastUnmuted([]bugsinkapi.TurningPoint{at("muted", "2026-11-09T20:00:00Z")})
	if err != nil || none != nil {
		t.Errorf("none = %v, %v", none, err)
	}
	got, err := bugsinkapi.LastUnmuted([]bugsinkapi.TurningPoint{
		at("unmuted", "2026-11-09T20:00:00.5Z"),
		at("muted", "2026-11-09T22:00:00Z"),
		at("unmuted", "2026-11-09T21:00:00Z"),
	})
	want := time.Date(2026, 11, 9, 21, 0, 0, 0, time.UTC)
	if err != nil || got == nil || !got.Equal(want) {
		t.Errorf("got %v, %v, want %v", got, err, want)
	}
	if _, err := bugsinkapi.LastUnmuted([]bugsinkapi.TurningPoint{at("unmuted", "yesterday")}); err == nil {
		t.Error("an unreadable unmute time should be an error")
	}
}

func eventAt(timestamp string) bugsinkapi.Event {
	return bugsinkapi.Event{Timestamp: timestamp}
}

func TestFirstAndLastSeenAreTheEarliestAndLatestEventWhateverOrderBugsinkListsThem(t *testing.T) {
	first, last, err := bugsinkapi.Seen(page[bugsinkapi.Event](t, "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first != "2026-11-04T10:00:00.250000Z" || last != "2026-11-09T21:14:03.482113Z" {
		t.Errorf("first = %s, last = %s", first, last)
	}
}

func TestOneEventIsBothTheFirstAndTheLast(t *testing.T) {
	at := "2026-10-30T15:54:40.045000Z"
	first, last, err := bugsinkapi.Seen([]bugsinkapi.Event{eventAt(at)})
	if err != nil || first != at || last != at {
		t.Errorf("first = %s, last = %s, err = %v", first, last, err)
	}
}

func TestNoEventsOrAnUnreadableTimeLeaveTheCallerToChooseAnotherDate(t *testing.T) {
	if _, _, err := bugsinkapi.Seen(nil); err == nil || err.Error() != "no events" {
		t.Errorf("err = %v", err)
	}
	_, _, err := bugsinkapi.Seen([]bugsinkapi.Event{eventAt("2026-11-04T10:00:00Z"), eventAt("yesterday")})
	if err == nil || !strings.Contains(err.Error(), "yesterday") {
		t.Errorf("err = %v", err)
	}
}

type seenRequest struct {
	target string
	auth   string
}

type api struct {
	origin string
	mu     sync.Mutex
	seen   []seenRequest
}

func (a *api) targets() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.seen))
	for i, s := range a.seen {
		out[i] = s.target
	}
	return out
}

// serve answers each route's request target with its JSON, replacing {base} with its own origin
// so a page can link to the next, and 404s any other target.
func serve(t *testing.T, routes map[string]string) *api {
	t.Helper()
	a := &api{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = append(a.seen, seenRequest{r.RequestURI, r.Header.Get("Authorization")})
		a.mu.Unlock()
		answer, ok := routes[r.RequestURI]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(answer, "{base}", a.origin)))
	}))
	t.Cleanup(srv.Close)
	a.origin = srv.URL
	return a
}

func client(origin string) *bugsinkapi.Client {
	return bugsinkapi.New(origin, "7", "api-token")
}

const (
	issueID    = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01"
	openIssues = "/api/canonical/0/issues/?project=7&state=open"
)

func TestOpenIssuesAsksForTheProjectsOpenStateWithTheBearerToken(t *testing.T) {
	a := serve(t, map[string]string{openIssues: fixture(t, "issues.json")})
	issues, err := client(a.origin).OpenIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Errorf("got %d issues", len(issues))
	}
	if got := a.targets(); !reflect.DeepEqual(got, []string{openIssues}) {
		t.Errorf("targets = %v", got)
	}
	if got := a.seen[0].auth; got != "Bearer api-token" {
		t.Errorf("auth = %q", got)
	}
}

func TestAListFollowsTheNextLinkToTheLastPage(t *testing.T) {
	var issues struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "issues.json")), &issues); err != nil {
		t.Fatal(err)
	}
	cursor := openIssues + "&cursor=abc"
	pageOne, _ := json.Marshal(map[string]any{"next": "{base}" + cursor, "results": issues.Results[:1]})
	pageTwo, _ := json.Marshal(map[string]any{"next": nil, "results": issues.Results[1:2]})
	a := serve(t, map[string]string{openIssues: string(pageOne), cursor: string(pageTwo)})
	found, err := client(a.origin).OpenIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range found {
		names = append(names, i.FriendlyID)
	}
	if !reflect.DeepEqual(names, []string{"BRENT-12", "BRENT-13"}) {
		t.Errorf("names = %v", names)
	}
	if len(a.targets()) != 2 {
		t.Errorf("targets = %v", a.targets())
	}
}

func TestTagsAndHistoryAreReadByTheIssueID(t *testing.T) {
	issue := page[bugsinkapi.Issue](t, "issues.json")[0]
	a := serve(t, map[string]string{
		"/api/canonical/0/issues/" + issueID + "/tags/":    fixture(t, "tags.json"),
		"/api/canonical/0/issue-history/?issue=" + issueID: fixture(t, "history.json"),
	})
	c := client(a.origin)
	tags, err := c.Tags(context.Background(), issue)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 7 {
		t.Errorf("got %d tags", len(tags))
	}
	history, err := c.History(context.Background(), issue)
	if err != nil {
		t.Fatal(err)
	}
	if got := bugsinkapi.Generation(history); got != 2 {
		t.Errorf("generation = %d", got)
	}
	if len(a.targets()) != 2 {
		t.Errorf("targets = %v", a.targets())
	}
}

func TestEventsAreReadByTheIssueID(t *testing.T) {
	issue := page[bugsinkapi.Issue](t, "issues.json")[0]
	a := serve(t, map[string]string{
		"/api/canonical/0/events/?issue=" + issueID: fixture(t, "events.json"),
	})
	events, err := client(a.origin).Events(context.Background(), issue)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events", len(events))
	}
}

func TestARefusalIsAnErrorThatNamesTheReadAndNotTheHost(t *testing.T) {
	a := serve(t, nil)
	_, err := client(a.origin).OpenIssues(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Bugsink issue list") || !strings.Contains(msg, "404") || strings.Contains(msg, a.origin) {
		t.Errorf("err = %s", msg)
	}
}

func TestAnUnreachableHostIsNotNamedInTheError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	origin := srv.URL
	srv.Close()
	_, err := client(origin).OpenIssues(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Bugsink issue list") || strings.Contains(msg, origin) || strings.Contains(msg, "api-token") {
		t.Errorf("err = %s", msg)
	}
}

func TestTheIssueLinkIsTheUIsLastEventPage(t *testing.T) {
	issue := page[bugsinkapi.Issue](t, "issues.json")[0]
	c := bugsinkapi.New("https://bugsink.example:8443", "7", "t")
	want := "https://bugsink.example:8443/issues/issue/" + issueID + "/event/last/"
	if got := c.IssueURL(issue); got != want {
		t.Errorf("got %s", got)
	}
}
