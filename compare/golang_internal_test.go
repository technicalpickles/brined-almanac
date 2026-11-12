package compare

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/engine"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/brine/hoststate"
)

// attesting is a workflow that also decides an Attest it did not declare, which the runtime
// denies. None of the binary's models can, so this is the only way to see the Go side deny.
type attesting struct{ workflow.Registered }

func (w attesting) Execute(ctx context.Context, cx workflow.Cx, payload json.RawMessage) (workflow.Decided, error) {
	decided, err := w.Registered.Execute(ctx, cx, payload)
	decided.Effects = append(decided.Effects, effect.Attest{Context: "brined-almanac", State: effect.Pending})
	return decided, err
}

func TestTheGoSidesDenialsAreComparedWithRusts(t *testing.T) {
	golang, err := NewGolang(nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := engine.New(brine.Vocabulary, hoststate.Model)
	if err != nil {
		t.Fatal(err)
	}
	golang.workflows = []workflow.Registered{attesting{w}}

	dir := filepath.Join(t.TempDir(), "host.state.finding")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := Case{Name: "host.state.finding/stale", Kind: "host.state.finding", Path: filepath.Join(dir, "stale.json")}
	stale := `{"kind": "stale", "project": "brined-almanac", "subject": "mac.stale", "host": "mac",
  "check": null, "status": null, "since": null, "body": "no report from mac"}`
	if err := os.WriteFile(c.Path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := golang.Side(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Runs) != 1 || !reflect.DeepEqual(g.Runs[0].Denied, []string{"attest pending"}) {
		t.Fatalf("%+v", g)
	}

	rustSaying := func(denied string) Side {
		t.Helper()
		text, err := json.Marshal(map[string]any{"simulate": "Queued", "outcomes": []string{
			`Ran { run_id: "run-1", workflow: "host_state", effects: [File { title: Title("host-state: no report from mac"), target: Slug("task:brined-almanac"), body: Untrusted(..) }], denied: [` + denied + `] } recorded as [Applied]`,
		}})
		if err != nil {
			t.Fatal(err)
		}
		filed := "FILE key=run-1:0 target=task:brined-almanac title=\"host-state: no report from mac\"\nno report from mac\n"
		side, err := parseSimulate(append([]byte(filed), text...))
		if err != nil {
			t.Fatal(err)
		}
		return side
	}
	if got := Compare(c, rustSaying(`ActionSummary { kind: Attest, attests: Some(Pending) }`), g).Differences; len(got) != 0 {
		t.Errorf("the same denial disagrees: %q", got)
	}
	got := Compare(c, rustSaying(`ActionSummary { kind: Attest, attests: Some(Failure) }`), g).Differences
	if len(got) != 1 || got[0] != `run 1 denied: rust "attest failure", go "attest pending"` {
		t.Errorf("%q", got)
	}
}
