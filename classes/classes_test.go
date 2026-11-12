package classes_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/technicalpickles/brined-almanac/classes"
)

var table = classes.Default()

type tc struct{ text, class string }

func check(t *testing.T, cases []tc) {
	t.Helper()
	for _, c := range cases {
		if got := table.Classify(c.text).Name; got != c.class {
			t.Errorf("%q: got %s, want %s", c.text, got, c.class)
		}
	}
}

func TestClassifyMapsEachTableTextToItsClass(t *testing.T) {
	check(t, []tc{
		{"Skipped due to queued user message", "queued-skip"},
		{"MCP error -32001: Request timed out", "mcp-timeout"},
		{`bundle-mcp server "brineworks" is not connected`, "mcp-disconnected"},
		{"FATAL: sorry, too many clients already", "pg-too-many-clients"},
		{"Approval timed out", "approval-timeout"},
		{"Approval card expired with no answer", "approval-timeout"},
		{"Plugin approval unavailable (no approval route)", "approval-unavailable"},
		{"no approval route for this surface", "approval-unavailable"},
		{`<HttpError 403 when requesting https://example.test/x returned "Rate Limit Exceeded". Details: reason: rateLimitExceeded>`, "rate-limit"},
		{"Quota exceeded for quota metric 'Queries' and limit 'per minute'", "rate-limit"},
		{"Unknown tool id: triage_aply", "unknown-tool-id"},
		{"1 validation error for Args", "arg-validation"},
		{"Validation failed for field x", "arg-validation"},
		{"Missing required argument: repo", "arg-validation"},
		{`Invalid arguments for tool "openclaw:automations": job.owner`, "arg-validation"},
		{"invalid cron.add params: schedule", "arg-validation"},
		{"invalid cron.update params: id", "arg-validation"},
		{"Error: unknown flag: --frm", "arg-validation"},
		{`Error: unexpected argument "x" found`, "arg-validation"},
		{"accepts at most one arg(s), received 2", "arg-validation"},
		{"--a cannot be used with --b", "arg-validation"},
		{"Could not find the exact text in notes.md", "model-error"},
		{"Could not find edits[0] in notes.md. The old text", "model-error"},
		{"Failed to find expected lines in notes.md", "model-error"},
		{"Failed to find context in notes.md", "model-error"},
		{"Invalid patch hunk on line 3", "model-error"},
		{"The first line of the patch must be '*** Begin Patch'", "model-error"},
		{"Found 3 occurrences of the text", "model-error"},
		{"Tool browser not found", "model-error"},
		{"File not found: notes.md", "model-error"},
		{"EACCES: permission denied, open 'x'", "model-error"},
		{"sh: 1: jq: not found", "model-error"},
		{"Command not found", "model-error"},
		{"elevated is not available here", "model-error"},
		{"memory_search timed out after 15s", "timeout"},
		{"request timed out", "timeout"},
		{"Blocked: resolves to private/internal/special-use IP address", "ssrf-blocked"},
		{"Web fetch failed (403): Forbidden", "upstream-http"},
		{"Web fetch extraction failed: Readability returned nothing", "extract-empty"},
		{"provider_error: Search failed", "provider-error"},
	})
}

func TestEarlierRowsBeatLaterOnes(t *testing.T) {
	check(t, []tc{
		{"MCP error -32001: Request timed out", "mcp-timeout"},
		{"Approval timed out after 30s", "approval-timeout"},
		{"Skipped due to queued user message: Tool x not found", "queued-skip"},
		{"provider_error: Search failed (HTTP 429)", "provider-error"},
	})
}

var slug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestTheTableIsWellFormed(t *testing.T) {
	rows := table.Rows()
	last := rows[len(rows)-1]
	if last.Name != "other" || last.Threshold == nil {
		t.Fatalf("last row = %+v, want other with a threshold", last)
	}
	if last.HasPattern() {
		t.Error("other must have no pattern")
	}
	seen := map[string]bool{}
	for _, c := range rows {
		if !slug.MatchString(c.Name) {
			t.Errorf("%q is not a slug", c.Name)
		}
		if seen[c.Name] {
			t.Errorf("%s twice", c.Name)
		}
		seen[c.Name] = true
		if strings.TrimSpace(c.Why) == "" {
			t.Errorf("%s has no why", c.Name)
		}
	}
	for _, c := range rows[:len(rows)-1] {
		if !c.HasPattern() {
			t.Errorf("%s lacks a pattern; only other may", c.Name)
		}
	}
}

func TestClassifyMapsUnknownTextToOther(t *testing.T) {
	check(t, []tc{{"the widget exploded", "other"}, {"", "other"}})
}

func TestClassifyReadsPast500Chars(t *testing.T) {
	check(t, []tc{{strings.Repeat("x", 600) + "Approval timed out", "approval-timeout"}})
}

func TestNamedFallsBackToOther(t *testing.T) {
	gone := table.Named("gone")
	if gone.Name != "other" || gone.Threshold == nil || *gone.Threshold != 3 {
		t.Errorf("gone = %+v, want other with threshold 3", gone)
	}
	if th := table.Named("mcp-timeout").Threshold; th == nil || *th != 1 {
		t.Errorf("mcp-timeout threshold = %v, want 1", th)
	}
	if th := table.Named("queued-skip").Threshold; th != nil {
		t.Errorf("queued-skip threshold = %v, want none", *th)
	}
}

func TestOwnerOf(t *testing.T) {
	if got := classes.OwnerOf("brineworks"); got != "technicalpickles/brineworks" {
		t.Errorf("brineworks -> %s", got)
	}
	if got := classes.OwnerOf("telegram"); got != "technicalpickles/pickleclaw" {
		t.Errorf("telegram -> %s", got)
	}
}

func TestParseRejectsOtherNotLast(t *testing.T) {
	_, err := classes.Parse([]byte("[[class]]\nname = \"other\"\nwhy = \"x\"\n\n[[class]]\nname = \"a\"\npattern = \"a\"\nwhy = \"x\"\n"))
	if err == nil {
		t.Fatal("want an error when other is not last")
	}
}
