package compare_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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

var update = flag.Bool("update", false, "rewrite testdata/compare.golden from the committed fixtures")

// recorded is what the Rust `--format json simulate` prints, for a recording.
func recorded(t *testing.T, ingest string, outcomes ...string) []byte {
	t.Helper()
	text, err := json.Marshal(map[string]any{"simulate": ingest, "why": nil, "outcomes": outcomes})
	if err != nil {
		t.Fatal(err)
	}
	return append(text, '\n')
}

// printed is a recording with what the Rust LogTracker printed before simulate's JSON.
func printed(filed string, recording []byte) []byte {
	return append([]byte(filed), recording...)
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
	if err := os.WriteFile(strings.TrimSuffix(path, ".json")+".rust.out", recording, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, compare.Case{Name: kind + "/one", Kind: kind, Path: path}
}

// sideOf is what r made of the one case c.
func sideOf(r compare.Rust, c compare.Case) (compare.Side, error) {
	sides, err := r.Sides(context.Background(), []compare.Case{c})
	if err != nil {
		return compare.Side{}, err
	}
	return sides[0], nil
}

func rustSide(t *testing.T, recording []byte) compare.Side {
	t.Helper()
	_, c := oneCase(t, "x.y", "{}", recording)
	side, err := sideOf(compare.Rust{}, c)
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
		if _, err := sideOf(compare.Rust{}, c); err == nil {
			t.Errorf("%s read", bad)
		}
	}
}

func TestAMissingRecordingSaysHowToMakeOne(t *testing.T) {
	_, c := oneCase(t, "x.y", "{}", nil)
	if err := os.Remove(strings.TrimSuffix(c.Path, ".json") + ".rust.out"); err != nil {
		t.Fatal(err)
	}
	_, err := sideOf(compare.Rust{}, c)
	if err == nil || !strings.Contains(err.Error(), "--rust BIN --record") {
		t.Errorf("%v", err)
	}
}

const stale = `{"kind": "stale", "project": "brined-almanac", "subject": "mac.stale", "host": "mac",
  "check": null, "status": null, "since": null, "body": "no report from mac"}`

// staleFiled is what the Go side runs stale to, written as the Rust binary would print it.
func staleFiled(t *testing.T, workflow, title string) []byte {
	return printedStale(t, workflow, title, "run-1:0", "no report from mac")
}

// printedStale is a Rust recording of stale filing body under key.
func printedStale(t *testing.T, workflow, title, key, body string) []byte {
	return printed("FILE key="+key+" target=task:brined-almanac title=\""+title+"\"\n"+body+"\n",
		recorded(t, "Queued", `Ran { run_id: "run-1", workflow: "`+workflow+`", effects: [File { title: Title("`+title+`"), target: Slug("task:brined-almanac"), body: Untrusted(..) }], denied: [] } recorded as [Applied]`))
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
		`run 1 effects: rust [file "host-state: no report from the mac" to "task:brined-almanac"], go [file "host-state: no report from mac" to "task:brined-almanac"]`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("no %q in\n%s", line, out)
		}
	}
}

func TestAnEventOnlyOneSideRefusesShowsWhatTheOtherRan(t *testing.T) {
	dir, _ := oneCase(t, "host.state.finding", stale, recorded(t, "Rejected"))
	out, err := run(t, "--format", "json", "compare", "--fixtures", dir)
	if err == nil {
		t.Fatal("agreed")
	}
	var report struct {
		Cases []struct {
			Differences []string `json:"differences"`
		} `json:"cases"`
		Disagreements int `json:"disagreements"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := report.Cases[0].Differences
	if report.Disagreements != 1 || len(got) != 2 || got[0] != `ingest: rust "rejected", go "queued"` ||
		got[1] != `runs: rust (no run), go "host_state" ran [file "host-state: no report from mac" to "task:brined-almanac"]` {
		t.Errorf("%+v", report)
	}
}

func TestRecordNeedsARustBinary(t *testing.T) {
	if _, err := run(t, "compare", "--record"); err == nil || !strings.Contains(err.Error(), "--rust BIN") {
		t.Errorf("%v", err)
	}
}

// TestTheCommittedFixturesCompareAsPinned runs the comparison as `brined-almanac compare` does
// and holds its whole report to testdata/compare.golden, so a case added without its recording,
// and any disagreement that appears or goes away, fails here. After a change that means to move
// the report, rerun with -update and read the diff.
func TestTheCommittedFixturesCompareAsPinned(t *testing.T) {
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
	if len(kinds) != 3 {
		t.Errorf("cases over %v", kinds)
	}
	got := compare.Render(report) + "\n"
	const golden = "testdata/compare.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("the report is not %s (rerun with -update if the change is meant):\n%s", golden, got)
	}
}

func TestDeniedEffectsAreReadAndCompared(t *testing.T) {
	side := rustSide(t, recorded(t, "Queued",
		`Ran { run_id: "run-1", workflow: "host-state", effects: [], denied: [ActionSummary { kind: File, attests: None }, ActionSummary { kind: Attest, attests: Some(Pending) }] } recorded as []`))
	if want := []string{"file", "attest pending"}; !reflect.DeepEqual(side.Runs[0].Denied, want) {
		t.Errorf("%+v", side.Runs[0])
	}
	golang := side
	golang.Runs = []compare.Run{side.Runs[0]}
	golang.Runs[0].Denied = []string{"file"}
	got := compare.Compare(compare.Case{Name: "x"}, side, golang).Differences
	if len(got) != 1 || got[0] != `run 1 denied: rust "file, attest pending", go "file"` {
		t.Errorf("%q", got)
	}
}

func TestAParseFailureAndAnUnroutedEventDisagree(t *testing.T) {
	got := compare.Compare(compare.Case{Name: "x"}, compare.Side{Ingest: "rejected"}, compare.Side{Ingest: "unrouted"}).Differences
	if len(got) != 1 || got[0] != `ingest: rust "rejected", go "unrouted"` {
		t.Errorf("%q", got)
	}
}

func TestRunsAreComparedInWorkflowOrder(t *testing.T) {
	a := compare.Run{Workflow: "a", Result: "ran"}
	b := compare.Run{Workflow: "b", Result: "stopped", Reason: "nothing to do"}
	got := compare.Compare(compare.Case{Name: "x"}, compare.Side{Ingest: "queued", Runs: []compare.Run{b, a}}, compare.Side{Ingest: "queued", Runs: []compare.Run{a, b}})
	if len(got.Differences) != 0 {
		t.Errorf("%q", got.Differences)
	}
}

func TestARunsReasonAndWorkflowAreQuoted(t *testing.T) {
	got := compare.Compare(compare.Case{Name: "x"},
		compare.Side{Ingest: "queued"},
		compare.Side{Ingest: "queued", Runs: []compare.Run{{Workflow: "w\x1b[2J", Result: "failed", Reason: "bad\nDISAGREE  forged"}}}).Differences
	if len(got) != 1 || got[0] != `runs: rust (no run), go "w\x1b[2J" failed ("bad\nDISAGREE  forged")` {
		t.Errorf("%q", got)
	}
}

// fakeRust is a Rust binary that prints recording whatever it is asked.
func fakeRust(t *testing.T, recording []byte) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, recording, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "brined-almanac")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestRecordRefusesToTurnARoutedCaseUnrouted(t *testing.T) {
	was := staleFiled(t, "host-state", "host-state: no report from mac")
	_, c := oneCase(t, "host.state.finding", stale, was)
	_, err := sideOf(compare.Rust{Binary: fakeRust(t, recorded(t, "Unrouted")), Record: true}, c)
	if err == nil || !strings.Contains(err.Error(), "leaves it unrouted, but its recording has it queued") || !strings.Contains(err.Error(), "stale") {
		t.Errorf("%v", err)
	}
	if now, _ := os.ReadFile(c.Recording()); !bytes.Equal(now, was) {
		t.Errorf("the recording changed to %s", now)
	}
}

func TestRecordWritesWhatTheBinaryPrinted(t *testing.T) {
	_, c := oneCase(t, "host.state.finding", stale, recorded(t, "Queued"))
	now := staleFiled(t, "host-state", "host-state: no report from mac")
	side, err := sideOf(compare.Rust{Binary: fakeRust(t, now), Record: true}, c)
	if err != nil || side.Ingest != "queued" {
		t.Fatalf("%v %+v", err, side)
	}
	if got, _ := os.ReadFile(c.Recording()); !bytes.Equal(got, now) {
		t.Errorf("recorded %s", got)
	}
}

func TestHyphenAndUnderscoreNamesPairUpAcrossTheDigits(t *testing.T) {
	rust := compare.Side{Ingest: "queued", Runs: []compare.Run{{Workflow: "a-b", Result: "ran"}, {Workflow: "a0", Result: "stopped", Reason: "nothing to do"}}}
	golang := compare.Side{Ingest: "queued", Runs: []compare.Run{{Workflow: "a0", Result: "stopped", Reason: "nothing to do"}, {Workflow: "a_b", Result: "ran"}}}
	got := compare.Compare(compare.Case{Name: "x"}, rust, golang).Differences
	if len(got) != 1 || got[0] != `run 2 workflow: rust "a-b", go "a_b"` {
		t.Errorf("%q", got)
	}
}

func TestAnEffectsTargetIsQuoted(t *testing.T) {
	file := compare.Effect{Kind: "file", Title: "t", Target: "task:x\nDISAGREE  forged"}
	got := compare.Compare(compare.Case{Name: "x"},
		compare.Side{Ingest: "queued", Runs: []compare.Run{{Workflow: "w", Result: "ran"}}},
		compare.Side{Ingest: "queued", Runs: []compare.Run{{Workflow: "w", Result: "ran", Effects: []compare.Effect{file}}}}).Differences
	if len(got) != 1 || got[0] != `run 1 effects: rust (none), go [file "t" to "task:x\nDISAGREE  forged"]` {
		t.Errorf("%q", got)
	}
}

func TestARefusedRecordWritesNoRecording(t *testing.T) {
	dir := t.TempDir()
	kind := filepath.Join(dir, "host.state.finding")
	if err := os.MkdirAll(kind, 0o755); err != nil {
		t.Fatal(err)
	}
	was := map[string][]byte{
		"a.rust.out": []byte(`{"simulate": "Unrouted", "outcomes": []}`),
		"b.rust.out": staleFiled(t, "host-state", "host-state: no report from mac"),
	}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(kind, name+".json"), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(kind, name+".rust.out"), was[name+".rust.out"], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := run(t, "compare", "--fixtures", dir, "--rust", fakeRust(t, recorded(t, "Unrouted")), "--record")
	if err == nil || !strings.Contains(err.Error(), "not recording host.state.finding/b") {
		t.Errorf("%v", err)
	}
	for name, text := range was {
		if now, _ := os.ReadFile(filepath.Join(kind, name)); !bytes.Equal(now, text) {
			t.Errorf("%s changed to %s", name, now)
		}
	}
}

func TestAFiledBodyThatDiffersShowsLineNumbersAndHashesNotText(t *testing.T) {
	recording := printedStale(t, "host_state", "host-state: no report from mac", "run-1:0", "no report from mac\nDISAGREE  forged")
	dir, _ := oneCase(t, "host.state.finding", stale, recording)
	out, err := run(t, "compare", "--fixtures", dir)
	if err == nil {
		t.Fatalf("agreed:\n%s", out)
	}
	if strings.Contains(out, "forged") {
		t.Errorf("the body's text is in the report:\n%s", out)
	}
	if !regexp.MustCompile(`run 1 filing 1 body: line\(s\) 2 differ \(rust 2 line\(s\) [0-9a-f]{8}, go 1 line\(s\) [0-9a-f]{8}\)`).MatchString(out) {
		t.Errorf("no line-numbered body difference in\n%s", out)
	}
}

func TestAFilingsKeyIsComparedWithTheRunsOwnIdTakenOut(t *testing.T) {
	recording := printedStale(t, "host_state", "host-state: no report from mac", "run-1:1", "no report from mac")
	dir, _ := oneCase(t, "host.state.finding", stale, recording)
	out, err := run(t, "compare", "--fixtures", dir)
	if err == nil || !strings.Contains(out, `run 1 filing 1 key: rust "{run}:1", go "{run}:0"`) {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestARunThatFiledOnOnlyOneSideDisagrees(t *testing.T) {
	recording := recorded(t, "Queued", `Ran { run_id: "run-1", workflow: "host_state", effects: [File { title: Title("host-state: no report from mac"), target: Slug("task:brined-almanac"), body: Untrusted(..) }], denied: [] } recorded as [Applied]`)
	dir, _ := oneCase(t, "host.state.finding", stale, recording)
	out, err := run(t, "compare", "--fixtures", dir)
	if err == nil || !strings.Contains(out, "run 1 filed: rust 0, go 1") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestAJSONReportCarriesABodysHashNotItsText(t *testing.T) {
	dir, _ := oneCase(t, "host.state.finding", stale, staleFiled(t, "host_state", "host-state: no report from mac"))
	out, err := run(t, "--format", "json", "compare", "--fixtures", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, `"no report from mac"`) || !strings.Contains(out, `"filed":[{"key":"{run}:0","body":{"lines":1,"sha256":"`) {
		t.Errorf("%s", out)
	}
}

func TestAFilingUnderNoRunsKeyIsAnError(t *testing.T) {
	recording := printedStale(t, "host_state", "host-state: no report from mac", "run-9:0", "no report from mac")
	_, c := oneCase(t, "host.state.finding", stale, recording)
	if _, err := sideOf(compare.Rust{}, c); err == nil || !strings.Contains(err.Error(), "no run") {
		t.Errorf("%v", err)
	}
}
