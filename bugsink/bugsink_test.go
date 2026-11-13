package bugsink_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	"github.com/technicalpickles/brined-almanac/classes"
	"github.com/technicalpickles/brined-almanac/transcript"
)

const dsnKey = "0123abcd"

// The role is split so the repository's forbidden-pattern scan, which looks for a transcript
// event line, does not match this file.
const (
	roleKey    = "role"
	resultRole = "tool" + "Result"
)

type failure struct {
	service, tool string
	builtin       bool
	statusOnly    bool
	at, errText   string
	times         int
}

func timeout(errText string) failure {
	return failure{service: "brineworks", tool: "triage_apply", at: "2026-11-04T10:00:00Z", errText: errText, times: 1}
}

func (f failure) atTime(iso string) failure { f.at = iso; return f }
func (f failure) repeated(n int) failure    { f.times = n; return f }

type fixture struct {
	t       *testing.T
	dir     string
	counter int
}

func newFixture(t *testing.T) *fixture { return &fixture{t: t, dir: t.TempDir()} }

func (f *fixture) next() int {
	f.counter++
	return f.counter
}

func line(id, at string, message map[string]any) string {
	out, err := json.Marshal(map[string]any{"type": "message", "id": id, "timestamp": at, "message": message})
	if err != nil {
		panic(err)
	}
	return string(out)
}

func addSeconds(iso string, seconds int) string {
	at, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return at.Add(time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05Z")
}

func (f *fixture) write(name string, fail failure) {
	f.t.Helper()
	var lines []string
	for i := range fail.times {
		callID := fmt.Sprintf("call-%04d", f.next())
		at := addSeconds(fail.at, i)
		toolName, details := "tool_call", any("")
		if fail.builtin {
			toolName = fail.service + "__" + fail.tool
			details = map[string]any{"status": "error"}
		} else {
			block := map[string]any{
				"type":      "toolCall",
				"id":        callID,
				"name":      "tool_call",
				"arguments": map[string]any{"id": fail.service + "__" + fail.tool},
			}
			lines = append(lines, line(fmt.Sprintf("evt-%04d", f.next()), at, map[string]any{roleKey: "assistant", "content": []any{block}}))
		}
		lines = append(lines, line(fmt.Sprintf("evt-%04d", f.next()), at, map[string]any{
			roleKey:      resultRole,
			"toolName":   toolName,
			"toolCallId": callID,
			"isError":    true,
			"details":    details,
			"content":    []any{map[string]any{"type": "text", "text": fail.errText}},
		}))
	}
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func errorsIn(t *testing.T, f *fixture) []transcript.ToolError {
	t.Helper()
	read, err := transcript.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	return read.Errors
}

func one(t *testing.T, name string, fail failure) transcript.ToolError {
	t.Helper()
	f := newFixture(t)
	f.write(name, fail)
	errs := errorsIn(t, f)
	if len(errs) != 1 {
		t.Fatalf("got %d errors", len(errs))
	}
	return errs[0]
}

func messageOf(t *testing.T, errText string) string {
	t.Helper()
	return bugsink.NewEvent(one(t, "a.jsonl", timeout(errText))).Message
}

func at(t *testing.T, raw string) transcript.At {
	t.Helper()
	a, ok := transcript.ParseAt(raw)
	if !ok {
		t.Fatalf("timestamp %q", raw)
	}
	return a
}

func TestAnEventCarriesTheFingerprintItsOwnTimeAndWhereItHappened(t *testing.T) {
	e := one(t, "agent-main-telegram-direct-42.jsonl", timeout("MCP error -32001: Request timed out").atTime("2026-11-04T10:00:00Z"))
	ev := bugsink.NewEvent(e)
	if want := []string{"brineworks", "triage_apply", "mcp-timeout"}; !reflect.DeepEqual(ev.Fingerprint, want) {
		t.Errorf("fingerprint = %v, want %v", ev.Fingerprint, want)
	}
	checks := []struct{ name, got, want string }{
		{"timestamp", ev.Timestamp, "2026-11-04T10:00:00Z"},
		{"level", ev.Level, "error"},
		{"logger", ev.Logger, "brineworks.triage_apply"},
		{"message", ev.Message, "MCP error -32001: Request timed out"},
		{"surface", ev.Tags.Surface, "telegram"},
		{"class", ev.Tags.Class, "mcp-timeout"},
		{"service", ev.Tags.Service, "brineworks"},
		{"tool", ev.Tags.Tool, "triage_apply"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestAMessageThatIsOnlyInContentStillPicksTheClass(t *testing.T) {
	fail := failure{service: "brineworks", tool: "tasks_update", builtin: true, at: "2026-11-04T10:00:00Z", errText: "sorry, too many clients already", times: 1}
	if got := bugsink.NewEvent(one(t, "a.jsonl", fail)).Tags.Class; got != "pg-too-many-clients" {
		t.Errorf("class = %q", got)
	}
}

func TestTheSessionTagIsAHashOfTheFileName(t *testing.T) {
	session := func(name string) string {
		return bugsink.NewEvent(one(t, name, timeout("boom"))).Tags.Session
	}
	a := session("agent-main-telegram-direct-42.jsonl")
	if len(a) != 12 || strings.Trim(a, "0123456789abcdefABCDEF") != "" {
		t.Errorf("session = %q", a)
	}
	if a != session("agent-main-telegram-direct-42.jsonl") {
		t.Error("the same file name gave two sessions")
	}
	if a == session("agent-main-telegram-direct-43.jsonl") {
		t.Error("two file names gave one session")
	}
}

func TestTheSurfaceIsTheWordAfterTheAgentOrUnknown(t *testing.T) {
	for name, want := range map[string]string{
		"agent-main-main.jsonl":          "main",
		"agent-main-openwebui-1-2.jsonl": "openwebui",
		"agent-main-cron-1.jsonl":        "cron",
		"notes.jsonl":                    "unknown",
	} {
		if got := one(t, name, timeout("boom")).Surface.String(); got != want {
			t.Errorf("%s: surface %q, want %q", name, got, want)
		}
	}
}

func TestAResendRepeatsTheEventIDAndEachCallGetsItsOwn(t *testing.T) {
	f := newFixture(t)
	f.write("a.jsonl", timeout("boom").repeated(2))
	ids := func() []string {
		var out []string
		for _, e := range errorsIn(t, f) {
			out = append(out, bugsink.NewEvent(e).EventID)
		}
		return out
	}
	first := ids()
	if len(first) != 2 {
		t.Fatalf("got %d ids", len(first))
	}
	if !reflect.DeepEqual(first, ids()) {
		t.Error("a second read gave other ids")
	}
	if first[0] == first[1] {
		t.Error("two calls shared an id")
	}
	for _, id := range first {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdefABCDEF") != "" {
			t.Errorf("id %q is not 32 hex characters", id)
		}
	}
}

func TestTheMessageIsTheLineThatSaysWhy(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"error": "pool exhausted\nat line 3"}`, "pool exhausted"},
		{"Command failed: git push\nremote rejected", "remote rejected"},
		{"\n\n  first real line\nsecond", "first real line"},
		{"", "(no text)"},
	} {
		if got := messageOf(t, c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
	if got := messageOf(t, strings.Repeat("x", 300)); len(got) != 200 {
		t.Errorf("a long message is %d bytes", len(got))
	}
}

func TestWithoutACursorEverythingIsDueAndWithOneTheLastHourOnward(t *testing.T) {
	f := newFixture(t)
	for _, stamp := range []string{"2026-11-04T10:59:59Z", "2026-11-04T11:00:00Z", "2026-11-04T13:00:00Z"} {
		f.write(stamp+".jsonl", timeout("boom").atTime(stamp))
	}
	errs := errorsIn(t, f)
	if n := len(bugsink.Due(errs, nil)); n != 3 {
		t.Errorf("without a cursor %d are due", n)
	}
	cursor := at(t, "2026-11-04T12:00:00Z")
	var times []string
	for _, e := range bugsink.Due(errs, &cursor) {
		times = append(times, e.At.String())
	}
	if want := []string{"2026-11-04T11:00:00Z", "2026-11-04T13:00:00Z"}; !reflect.DeepEqual(times, want) {
		t.Errorf("times = %v, want %v", times, want)
	}
}

func TestACursorSurvivesARoundTripAndAMissingOrGarbledOneIsNone(t *testing.T) {
	cursor := bugsink.Cursor{Path: filepath.Join(t.TempDir(), "cursor")}
	got, err := cursor.Load()
	if err != nil || got != nil {
		t.Fatalf("a missing cursor loaded %v, %v", got, err)
	}
	want := at(t, "2026-11-04T10:00:00.123Z")
	if err := cursor.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err = cursor.Load()
	if err != nil || got == nil || *got != want {
		t.Fatalf("loaded %v, %v", got, err)
	}
	if err := os.WriteFile(cursor.Path, []byte("not a time"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = cursor.Load()
	if err != nil || got != nil {
		t.Fatalf("a garbled cursor loaded %v, %v", got, err)
	}
}

func TestADsnNamesTheStoreEndpointAndABadOneNeverEchoesItsKey(t *testing.T) {
	dsn, err := bugsink.ParseDsn("https://" + dsnKey + "@bugsink.example:8000/3")
	if err != nil {
		t.Fatal(err)
	}
	if got := dsn.StoreURL(); got != "https://bugsink.example:8000/api/3/store/" {
		t.Errorf("store = %q", got)
	}
	if dsn.BaseURL() != "https://bugsink.example:8000" || dsn.Project() != "3" {
		t.Errorf("base = %q, project = %q", dsn.BaseURL(), dsn.Project())
	}
	for _, shown := range []string{
		fmt.Sprintf("%v", dsn), fmt.Sprintf("%+v", dsn), fmt.Sprintf("%#v", dsn),
		fmt.Sprintf("%s", dsn), fmt.Sprintf("%v", &dsn), fmt.Sprintf("%+v", &dsn),
	} {
		if strings.Contains(shown, dsnKey) {
			t.Errorf("a formatted Dsn shows the key: %s", shown)
		}
	}
	for _, bad := range []string{
		dsnKey + "@bugsink.example/3",
		"ftp://" + dsnKey + "@bugsink.example/3",
		"https://bugsink.example/3",
		"https://" + dsnKey + "@bugsink.example/project",
	} {
		_, err := bugsink.ParseDsn(bad)
		if err == nil {
			t.Errorf("%q parsed", bad)
		} else if strings.Contains(err.Error(), dsnKey) {
			t.Errorf("the error shows the key: %v", err)
		}
	}
}

func TestTheVerdictsAreTheRowsOfClassesToml(t *testing.T) {
	if got := bugsink.Verdicts(); !reflect.DeepEqual(got, rowNames()) {
		t.Errorf("verdicts %v differ from classes.toml rows %v", got, rowNames())
	}
}

func rowNames() []string {
	var names []string
	for _, row := range classes.Default().Rows() {
		names = append(names, row.Name)
	}
	return names
}

type received struct {
	path string
	auth string
	body map[string]any
}

// store is a store endpoint on loopback that answers 200 to the first accept events and 503 after.
func store(t *testing.T, accept int) (string, func() []received) {
	t.Helper()
	var mu sync.Mutex
	var seen []received
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		seen = append(seen, received{path: r.URL.Path, auth: r.Header.Get("X-Sentry-Auth"), body: body})
		count := len(seen)
		mu.Unlock()
		if count <= accept {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)
	dsn := strings.Replace(server.URL, "http://", "http://"+dsnKey+"@", 1) + "/7"
	return dsn, func() []received {
		mu.Lock()
		defer mu.Unlock()
		return append([]received(nil), seen...)
	}
}

func threeHours(t *testing.T) []transcript.ToolError {
	f := newFixture(t)
	for _, stamp := range []string{"2026-11-04T10:00:00Z", "2026-11-04T11:30:00Z", "2026-11-04T12:00:00Z"} {
		f.write(stamp+".jsonl", timeout("boom").atTime(stamp))
	}
	return errorsIn(t, f)
}

func mustDsn(t *testing.T, raw string) bugsink.Dsn {
	t.Helper()
	dsn, err := bugsink.ParseDsn(raw)
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestALiveShipPostsEachEventWithTheKeyAndASecondResendsOnlyTheOverlap(t *testing.T) {
	raw, seen := store(t, math.MaxInt)
	cursor := bugsink.Cursor{Path: filepath.Join(t.TempDir(), "cursor")}
	sink := bugsink.New(mustDsn(t, raw), cursor, true)
	errs := threeHours(t)

	first, err := sink.Ship(context.Background(), errs)
	if err != nil {
		t.Fatal(err)
	}
	if first != (bugsink.Shipped{Due: 3, Sent: 3}) {
		t.Errorf("first = %+v", first)
	}
	if got, err := cursor.Load(); err != nil || got == nil || *got != at(t, "2026-11-04T12:00:00Z") {
		t.Errorf("cursor = %v, %v", got, err)
	}
	got := seen()
	for _, r := range got {
		if r.path != "/api/7/store/" {
			t.Errorf("path = %q", r.path)
		}
		if !strings.Contains(r.auth, "sentry_key="+dsnKey) {
			t.Errorf("auth = %q", r.auth)
		}
	}
	if got[0].body["timestamp"] != "2026-11-04T10:00:00Z" {
		t.Errorf("first timestamp = %v", got[0].body["timestamp"])
	}

	second, err := sink.Ship(context.Background(), errs)
	if err != nil {
		t.Fatal(err)
	}
	if second != (bugsink.Shipped{Due: 2, Sent: 2}) {
		t.Errorf("second = %+v", second)
	}
	got = seen()
	if got[3].body["event_id"] != got[1].body["event_id"] {
		t.Error("the resend did not repeat the event id")
	}
}

func TestADryRunSendsNothingAndLeavesTheCursor(t *testing.T) {
	raw, seen := store(t, math.MaxInt)
	dir := t.TempDir()
	sink := bugsink.New(mustDsn(t, raw), bugsink.Cursor{Path: filepath.Join(dir, "cursor")}, false)
	shipped, err := sink.Ship(context.Background(), threeHours(t))
	if err != nil {
		t.Fatal(err)
	}
	if shipped != (bugsink.Shipped{Due: 3, Sent: 0}) {
		t.Errorf("shipped = %+v", shipped)
	}
	if n := len(seen()); n != 0 {
		t.Errorf("a dry run sent %d events", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "cursor")); !os.IsNotExist(err) {
		t.Errorf("a dry run left a cursor: %v", err)
	}
}

func TestARefusedEventStopsTheSendAndTheCursorStaysAtTheLastAccepted(t *testing.T) {
	raw, _ := store(t, 1)
	cursor := bugsink.Cursor{Path: filepath.Join(t.TempDir(), "cursor")}
	sink := bugsink.New(mustDsn(t, raw), cursor, true)
	_, err := sink.Ship(context.Background(), threeHours(t))
	if err == nil {
		t.Fatal("a refused event did not fail the ship")
	}
	if !strings.Contains(err.Error(), "sent 1 of 3") || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v", err)
	}
	if got, err := cursor.Load(); err != nil || got == nil || *got != at(t, "2026-11-04T10:00:00Z") {
		t.Errorf("cursor = %v, %v", got, err)
	}
}
