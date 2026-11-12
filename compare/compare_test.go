package compare_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ivy/almanac/model"

	"github.com/technicalpickles/brined-almanac/brine/bugsinkissues"
	"github.com/technicalpickles/brined-almanac/brine/hoststate"
	"github.com/technicalpickles/brined-almanac/brine/securityalerts"
	"github.com/technicalpickles/brined-almanac/compare"
)

var models = []model.Workflow{hoststate.Model, securityalerts.Model, bugsinkissues.Model}

// recorded is what the Rust `--format json simulate` prints, for a recording.
func recorded(t *testing.T, ingest string, outcomes ...string) []byte {
	t.Helper()
	text, err := json.Marshal(map[string]any{"simulate": ingest, "why": nil, "outcomes": outcomes})
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// oneCase writes an event and its Rust recording into a fixtures directory of their own.
func oneCase(t *testing.T, kind, payload string, recording []byte) (string, compare.Case) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, kind), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, kind, "one.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(path, ".json")+".rust.json", recording, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, compare.Case{Name: kind + "/one", Kind: kind, Path: path}
}

func rustSide(t *testing.T, recording []byte) compare.Side {
	t.Helper()
	_, c := oneCase(t, "x.y", "{}", recording)
	side, err := compare.Rust{}.Side(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return side
}

func TestARustRunIsReadWithItsWorkflowEffectsAndStatuses(t *testing.T) {
	side := rustSide(t, recorded(t, "Queued",
		`Ran { run_id: "run-1", workflow: "host-state", effects: [File { title: Title("a \"quoted\" title\nwith é and \u{1f600}"), target: Slug("task:brined-almanac"), body: Untrusted(..) }], denied: [] } recorded as [WouldApply]`))
	want := compare.Side{Ingest: "queued", Runs: []compare.Run{{
		Workflow: "host-state", Result: "ran",
		Effects:  []compare.Effect{{Kind: "file", Title: "a \"quoted\" title\nwith é and \U0001f600", Target: "task:brined-almanac"}},
		Recorded: []string{"would-apply"},
	}}}
	if !reflect.DeepEqual(side, want) {
		t.Errorf("%+v", side)
	}
}

func TestARustRunThatEndedEarlyIsReadWithItsReason(t *testing.T) {
	side := rustSide(t, recorded(t, "Queued",
		`Stopped { run_id: "run-1", workflow: "host-state", reason: "nothing to do" }`,
		`Failed { run_id: "run-2", workflow: "bugsink-issues", error: "step failed: boom" }`,
		`BudgetTripped { workflow: Some("security-alerts"), held: 3 }`,
		`BudgetTripped { workflow: None, held: 1 }`))
	want := []compare.Run{
		{Workflow: "host-state", Result: "stopped", Reason: "nothing to do"},
		{Workflow: "bugsink-issues", Result: "failed", Reason: "step failed: boom"},
		{Workflow: "security-alerts", Result: "budget-tripped"},
		{Result: "budget-tripped"},
	}
	if !reflect.DeepEqual(side.Runs, want) {
		t.Errorf("%+v", side.Runs)
	}
}

func TestARecordingThatIsNotRustsOutputIsAnError(t *testing.T) {
	for _, bad := range [][]byte{[]byte("not json"), recorded(t, "Queued", `Ran { run_id: "run-1", workflow: "unfinished`)} {
		_, c := oneCase(t, "x.y", "{}", bad)
		if _, err := (compare.Rust{}).Side(context.Background(), c); err == nil {
			t.Errorf("%s read", bad)
		}
	}
}

func TestAMissingRecordingSaysHowToMakeOne(t *testing.T) {
	_, c := oneCase(t, "x.y", "{}", nil)
	if err := os.Remove(strings.TrimSuffix(c.Path, ".json") + ".rust.json"); err != nil {
		t.Fatal(err)
	}
	_, err := compare.Rust{}.Side(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "--rust BIN --record") {
		t.Errorf("%v", err)
	}
}

const stale = `{"kind": "stale", "project": "brined-almanac", "subject": "mac.stale", "host": "mac",
  "check": null, "status": null, "since": null, "body": "no report from mac"}`

// staleFiled is what the Go side runs stale to, written as the Rust binary would print it.
func staleFiled(t *testing.T, workflow, title string) []byte {
	return recorded(t, "Queued", `Ran { run_id: "run-1", workflow: "`+workflow+`", effects: [File { title: Title("`+title+`"), target: Slug("task:brined-almanac"), body: Untrusted(..) }], denied: [] } recorded as [WouldApply]`)
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "brined-almanac", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("format", "human", "")
	root.AddCommand(compare.Command(models))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestSidesThatMatchAgreeAndExitZero(t *testing.T) {
	dir, _ := oneCase(t, "host.state.finding", stale, staleFiled(t, "host_state", "host-state: no report from mac"))
	out, err := run(t, "compare", "--fixtures", dir)
	if err != nil || !strings.Contains(out, "agree     host.state.finding/one") || !strings.Contains(out, "0 of 1 case(s) disagree") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestEachDifferenceIsListedAndExitsNonZero(t *testing.T) {
	dir, _ := oneCase(t, "host.state.finding", stale, staleFiled(t, "host-state", "host-state: no report from the mac"))
	out, err := run(t, "compare", "--fixtures", dir)
	if err == nil || !strings.Contains(err.Error(), "1 of 1 case(s) disagree") {
		t.Errorf("err %v", err)
	}
	for _, line := range []string{
		"DISAGREE  host.state.finding/one",
		`run 1 workflow: rust "host-state", go "host_state"`,
		`run 1 effects: rust [file "host-state: no report from the mac" to task:brined-almanac], go [file "host-state: no report from mac" to task:brined-almanac]`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("no %q in\n%s", line, out)
		}
	}
}

func TestAnEventOnlyOneSideRefusesShowsWhatTheOtherRan(t *testing.T) {
	bad := strings.Replace(stale, `"brined-almanac"`, `"Bad"`, 1)
	dir, _ := oneCase(t, "host.state.finding", bad, recorded(t, "Rejected"))
	out, err := run(t, "--format", "json", "compare", "--fixtures", dir)
	if err == nil {
		t.Fatal("agreed")
	}
	var report compare.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := report.Cases[0].Differences
	if report.Disagreements != 1 || len(got) != 2 || got[0] != `ingest: rust "rejected", go "queued"` || !strings.HasPrefix(got[1], "runs: rust (no run), go host_state failed (") {
		t.Errorf("%+v", report)
	}
}

func TestRecordNeedsARustBinary(t *testing.T) {
	if _, err := run(t, "compare", "--record"); err == nil || !strings.Contains(err.Error(), "--rust BIN") {
		t.Errorf("%v", err)
	}
}

// TestTheCommittedFixturesAllHaveRecordings runs the comparison as `brined-almanac compare`
// does, so a case added without its recording fails here and not in front of a person.
func TestTheCommittedFixturesAllHaveRecordings(t *testing.T) {
	cases, err := compare.Cases("fixtures")
	if err != nil {
		t.Fatal(err)
	}
	golang, err := compare.NewGolang(models)
	if err != nil {
		t.Fatal(err)
	}
	report, err := compare.All(context.Background(), cases, compare.Rust{}, golang)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, c := range cases {
		kinds[c.Kind] = true
	}
	if len(report.Cases) != len(cases) || len(kinds) != 3 {
		t.Errorf("%d cases over %v", len(report.Cases), kinds)
	}
}
