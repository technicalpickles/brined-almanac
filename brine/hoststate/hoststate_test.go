package hoststate_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivy/almanac/agent"
	"github.com/ivy/almanac/check"
	"github.com/ivy/almanac/cooled"
	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/engine"
	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/brine/hoststate"
	rules "github.com/technicalpickles/brined-almanac/hoststate"
)

// walking is a workflow.Cx for one walk of the model outside a runtime. host_state only
// observes and decides, so everything else is refused.
type walking struct{}

func (walking) Record(ctx context.Context, _ string, read func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return read(ctx)
}
func (walking) Attempts(context.Context) (workflow.Attempts, error) { return workflow.Attempts{}, nil }
func (walking) Age(context.Context, time.Duration, func(context.Context) (cooled.Published, error)) (cooled.Age, cooled.Cooled, error) {
	return 0, cooled.Cooled{}, errors.New("host_state measures nothing")
}
func (walking) Hold(context.Context, string, hostile.Slug, hostile.Sha) error {
	return errors.New("host_state holds nothing")
}
func (walking) Screen(context.Context, string, hostile.Of[string], float64) error {
	return errors.New("host_state screens nothing")
}
func (walking) Agent(context.Context, agent.Call) (hostile.Of[json.RawMessage], error) {
	return hostile.Of[json.RawMessage]{}, errors.New("host_state calls no agent")
}
func (walking) Provider(need providers.Requirement) (any, error) {
	return nil, errors.New("host_state reads no provider in a run: " + need.Name())
}

var now = time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)

func critical(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../tests/fixtures/host_state/mac.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hosts(t *testing.T) rules.Hosts {
	t.Helper()
	h, err := rules.ParseHosts("[[host]]\nname = \"mac\"\nproject = \"brined-almanac\"\n")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// scanned is the one payload a scan of files finds.
func scanned(t *testing.T, files map[string]string) json.RawMessage {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	previous := now.Add(-15 * time.Minute)
	found := brine.ScanFindings(hosts(t), dir, now, &previous, &rules.Episodes{})
	if len(found) != 1 {
		t.Fatalf("want one finding, got %+v", found)
	}
	return found[0].Payload
}

func walk(t *testing.T, payload json.RawMessage) (engine.Ended, effect.File) {
	t.Helper()
	w, err := hoststate.New()
	if err != nil {
		t.Fatal(err)
	}
	ended, err := w.Walk(context.Background(), walking{}, payload)
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
	return ended, file
}

func bodyJSON(t *testing.T, body hostile.Of[string]) string {
	t.Helper()
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func untrusted(t *testing.T, text string) string {
	t.Helper()
	return bodyJSON(t, hostile.New(text))
}

func TestTheModelChecks(t *testing.T) {
	if err := brine.Vocabulary.Err(); err != nil {
		t.Fatal(err)
	}
	if err := check.Check(brine.Vocabulary, hoststate.Model); err != nil {
		t.Fatal(err)
	}
}

func TestTheModelDeclaresWhatRustDoes(t *testing.T) {
	w, err := hoststate.New()
	if err != nil {
		t.Fatal(err)
	}
	if w.RunsPerHour() != 1000 || !slices.Equal(w.May(), []effect.Kind{effect.KindFile}) || w.Kind() != "host.state.finding" {
		t.Fatalf("runs %d, may %v, kind %s", w.RunsPerHour(), w.May(), w.Kind())
	}
	if w.Intent() != "File one task for each host check that has been failing long enough, and one when a host's report stops arriving." {
		t.Fatal(w.Intent())
	}
	identity, err := w.Identify(scanned(t, map[string]string{"mac.json": critical(t)}))
	if err != nil {
		t.Fatal(err)
	}
	if identity.Key != "mac.gh-auth.2026-11-10T14:50Z" {
		t.Fatalf("key %q", identity.Key)
	}
}

func TestEveryEndingHasAWorldThatReachesIt(t *testing.T) {
	stale := strings.Replace(critical(t), `"generated_at": "2026-11-10T15:00:00Z"`, `"generated_at": "2026-11-10T14:29:00Z"`, 1)
	worlds := map[string]struct {
		files map[string]string
		title string
		body  string
	}{
		"file/check": {map[string]string{"mac.json": critical(t)},
			(&rules.CheckFinding{Host: "mac", Check: "gh-auth", Status: rules.StatusCritical, Since: time.Date(2026, 11, 10, 14, 50, 0, 0, time.UTC)}).Title(),
			"GH CRITICAL - gh auth status timed out after 20s\nstatus: critical\nsince: 2026-11-10T14:50:00Z"},
		"file/stale_since": {map[string]string{"mac.json": stale},
			"host-state: no report from mac since 2026-11-10 14:29Z",
			"last report for mac was generated 2026-11-10 14:29Z"},
		"file/stale": {nil, "host-state: no report from mac", "no report file for mac"},
	}
	var endings []string
	for _, e := range hoststate.Model.Endings() {
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
		t.Run(end, func(t *testing.T) {
			ended, file := walk(t, scanned(t, world.files))
			if got := ended.Step + "/" + ended.Rule; got != end {
				t.Errorf("ended at %s", got)
			}
			if file.Title.String() != world.title {
				t.Errorf("title %q, want %q", file.Title, world.title)
			}
			if file.Target.String() != "task:brined-almanac" {
				t.Errorf("target %q", file.Target)
			}
			if got := bodyJSON(t, file.Body); got != untrusted(t, world.body) {
				t.Errorf("body %s", got)
			}
		})
	}
}

func TestTheBodyStaysUntrusted(t *testing.T) {
	hostileReport := strings.Replace(critical(t), "GH CRITICAL - gh auth status timed out after 20s", "`rm` @someone", 1)
	_, file := walk(t, scanned(t, map[string]string{"mac.json": hostileReport}))
	if got := bodyJSON(t, file.Body); got != untrusted(t, "`rm` @someone\nstatus: critical\nsince: 2026-11-10T14:50:00Z") {
		t.Fatal(got)
	}
}

func TestARunWhoseFilingIsRefusedFails(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal(scanned(t, map[string]string{"mac.json": critical(t)}), &payload); err != nil {
		t.Fatal(err)
	}
	payload["since"] = "T14:50Z"
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	w, err := hoststate.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Walk(context.Background(), walking{}, raw); err == nil {
		t.Fatal("the run filed a finding whose since does not split")
	}
}

func TestAStaleReportFilesOnTheSecondScanInAProcess(t *testing.T) {
	dir := t.TempDir()
	old := strings.Replace(critical(t), "2026-11-10T15:00:00Z", "2000-01-01T00:00:00Z", 1)
	if err := os.WriteFile(filepath.Join(dir, "mac.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	set := providers.New()
	providers.Provide(set, hosts(t))
	providers.Provide(set, &brine.HostReports{Dir: dir})
	w, err := hoststate.New()
	if err != nil {
		t.Fatal(err)
	}
	scanner := workflow.NewScanner(set)
	for i, want := range []int{0, 1} {
		found, err := w.Scan(context.Background(), scanner)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != want {
			t.Fatalf("scan %d found %+v", i+1, found)
		}
	}
}
