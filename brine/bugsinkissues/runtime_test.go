package bugsinkissues_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine/bugsinkissues"
	"github.com/technicalpickles/brined-almanac/internal/fakeapi"
)

type delivered struct {
	key    string
	target string
	title  string
	body   string
}

// tracker keeps what Ivy's runtime delivers to a tracker, bodies included.
type tracker struct {
	t     *testing.T
	mu    sync.Mutex
	calls int
	filed []delivered
}

func (r *tracker) File(_ context.Context, key string, issue effect.Issue) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.filed = append(r.filed, delivered{key, issue.Target.String(), issue.Title, textOf(r.t, issue.Body)})
	return nil
}

func (r *tracker) Ask(context.Context, string, effect.Question) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *tracker) called() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *tracker) delivered() []delivered {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delivered(nil), r.filed...)
}

func started(t *testing.T, api *fakeapi.Api, mode effect.Mode) (*runtime.Runtime, *tracker) {
	t.Helper()
	w, err := bugsinkissues.New()
	if err != nil {
		t.Fatal(err)
	}
	log := &tracker{t: t}
	set, err := scanner{api: api, live: mode == effect.Live}.providers(t)
	if err != nil {
		t.Fatal(err)
	}
	providers.Provide[effect.Tracker](set, log)
	rt := runtime.New([]workflow.Registered{w}, set, runtime.InMode(mode))
	if err := rt.Validate(); err != nil {
		t.Fatal(err)
	}
	return rt, log
}

func backfill(t *testing.T, rt *runtime.Runtime) runtime.Backfill {
	t.Helper()
	report, err := rt.Backfill(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func settle(t *testing.T, rt *runtime.Runtime) {
	t.Helper()
	if _, err := rt.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestALiveScanFilesOnlyTheIssueThatIsLoudUnresolvedAndUnmuted(t *testing.T) {
	api := bugsinkApi(t)
	rt, log := started(t, api, effect.Live)
	if got := backfill(t, rt).Found; got != 1 {
		t.Fatalf("found %d", got)
	}
	settle(t, rt)

	filed := log.delivered()
	if len(filed) != 1 {
		t.Fatalf("%+v", filed)
	}
	want := "service: brineworks\ntool: triage_apply\nclass: approval-timeout\nevents: 5\nfirst seen: 2026-09-03T01:48:25.567000Z\nlast seen: 2026-10-30T15:54:40.045000Z\nsurfaces: telegram (5)\nsessions: 1\nbugsink: " + api.Origin + "/issues/issue/" + loud + "/event/last/\n\nreason:\nMCP error -32001: Request timed out"
	if filed[0].target != "technicalpickles/brineworks" || filed[0].title != "Brent: brineworks/triage_apply approval-timeout (5 events)" || filed[0].body != want {
		t.Errorf("%+v", filed[0])
	}
	if filed[0].key == "" {
		t.Error("the tracker got no key to repeat safely")
	}
}

func TestScanningAgainChangesNothing(t *testing.T) {
	rt, _ := started(t, bugsinkApi(t), effect.DryRun)
	first, second := backfill(t, rt), backfill(t, rt)
	if first.Found != 1 || second.Unchanged != 1 || second.Queued != 0 {
		t.Fatalf("%+v %+v", first, second)
	}
}

func TestGrowingEventsOnAnIssueAlreadySeenAreNotANewDelivery(t *testing.T) {
	api := bugsinkApi(t)
	rt, _ := started(t, api, effect.DryRun)
	backfill(t, rt)
	api.Route(list, page(listed(loud, 9, false, false)))
	again := backfill(t, rt)
	if again.Unchanged != 1 || again.Queued != 0 {
		t.Fatalf("%+v", again)
	}
}

func TestARegressionIsANewDeliveryAndFilesAgain(t *testing.T) {
	api := bugsinkApi(t)
	rt, log := started(t, api, effect.Live)
	backfill(t, rt)
	settle(t, rt)

	api.Route(historyAt(loud), history("regressed", "resolved", "first_seen"))
	if again := backfill(t, rt); again.Queued != 1 {
		t.Fatalf("%+v", again)
	}
	settle(t, rt)

	filed := log.delivered()
	if len(filed) != 2 || filed[0].key == filed[1].key {
		t.Fatalf("%+v", filed)
	}
	if filed[1].title != "Brent: brineworks/triage_apply approval-timeout regressed (5 events)" || !strings.Contains(filed[1].body, "\nregressions: 1 (came back after being resolved)\n") {
		t.Errorf("%+v", filed[1])
	}
}

func TestADryRunCallsNoTracker(t *testing.T) {
	rt, log := started(t, bugsinkApi(t), effect.DryRun)
	if got := backfill(t, rt).Queued; got != 1 {
		t.Fatalf("queued %d", got)
	}
	settle(t, rt)
	if got := log.called(); got != 0 {
		t.Fatalf("a dry run called the tracker %d times", got)
	}
}
