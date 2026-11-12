package securityalerts_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/github"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/brine/securityalerts"
	"github.com/technicalpickles/brined-almanac/internal/fakeapi"
	rules "github.com/technicalpickles/brined-almanac/securityalerts"
	"github.com/technicalpickles/brined-almanac/tiers"
)

type obj = map[string]any

const declared = `
[[repo]]
name = "technicalpickles/dotfiles"
tiers = ["active"]

[[repo]]
name = "technicalpickles/example-private"
tiers = ["deployed"]

[[repo]]
name = "technicalpickles/example-public"
tiers = ["maintained"]

[[repo]]
name = "technicalpickles/not-watched"
tiers = ["dormant"]
`

func ago(days int) string {
	return time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
}

func dependabot(number int, severity, created string) obj {
	return obj{
		"number":                 number,
		"dependency":             obj{"package": obj{"name": "toml"}, "manifest_path": "package-lock.json"},
		"security_advisory":      obj{"ghsa_id": "GHSA-aaaa-bbbb-cccc", "severity": severity},
		"security_vulnerability": obj{"first_patched_version": obj{"identifier": "5.0.1"}},
		"created_at":             created,
		"html_url":               "https://example.test/alerts/" + strconv.Itoa(number),
	}
}

func api(t *testing.T) *fakeapi.Api {
	t.Helper()
	api := fakeapi.Serve(t)
	list := "?state=open&per_page=100"

	api.Route("/repos/technicalpickles/dotfiles", obj{"private": false})
	api.Status("/repos/technicalpickles/dotfiles/vulnerability-alerts", 204)
	api.Route("/repos/technicalpickles/dotfiles/dependabot/alerts"+list, []any{
		dependabot(7, "high", "2020-01-01T00:00:00Z"),
		dependabot(8, "high", ago(1)),
	})
	api.Route("/repos/technicalpickles/dotfiles/secret-scanning/alerts"+list, []any{})

	api.Route("/repos/technicalpickles/example-private", obj{"private": true})
	api.Status("/repos/technicalpickles/example-private/vulnerability-alerts", 404)

	api.Route("/repos/technicalpickles/example-public", obj{"private": false})
	api.Status("/repos/technicalpickles/example-public/vulnerability-alerts", 204)
	api.Route("/repos/technicalpickles/example-public/dependabot/alerts"+list, []any{})
	return api
}

func providersFor(t *testing.T, api *fakeapi.Api, client bool) *providers.Set {
	t.Helper()
	declaredTiers, err := tiers.Parse(declared, nil)
	if err != nil {
		t.Fatal(err)
	}
	set := providers.New()
	providers.Provide(set, declaredTiers)
	alerts := brine.AlertsClient{}
	if client {
		alerts.Client = rules.NewClient(api.Origin, "alerts-token", github.Retrying(github.Retry{Attempts: 1}))
	}
	providers.Provide(set, alerts)
	return set
}

func scan(t *testing.T, api *fakeapi.Api, client bool) ([]workflow.Found, error) {
	t.Helper()
	w, err := securityalerts.New()
	if err != nil {
		t.Fatal(err)
	}
	return w.Scan(context.Background(), workflow.NewScanner(providersFor(t, api, client)))
}

func TestAScanFilesOneTaskForTheOverdueRepositoryAndOneForTheGaps(t *testing.T) {
	found, err := scan(t, api(t), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("%+v", found)
	}
	overdue, coverage := filing(t, found[0].Payload), filing(t, found[1].Payload)
	if overdue.Title.String() != "security-alerts: 1 overdue on technicalpickles/dotfiles (1 high)" || overdue.Target.String() != "task:dotfiles" {
		t.Errorf("%q to %s", overdue.Title, overdue.Target)
	}
	if body := textOf(t, overdue.Body); !strings.HasPrefix(body, "#7 high toml (package-lock.json) GHSA-aaaa-bbbb-cccc, fixed in 5.0.1, ") || strings.Contains(body, "\n") {
		t.Errorf("body %q", body)
	}
	if coverage.Title.String() != "security-alerts: alerts are off on 2 repositories" || coverage.Target.String() != "task:brined-almanac" {
		t.Errorf("%q to %s", coverage.Title, coverage.Target)
	}
	want := "Dependabot alerts are off: technicalpickles/example-private\nSecret scanning is off: technicalpickles/example-public"
	if got := textOf(t, coverage.Body); got != want {
		t.Errorf("body %q", got)
	}
	if !strings.HasPrefix(found[0].DeliveryID, "scan:security-alerts/technicalpickles/dotfiles@") || !strings.HasPrefix(found[1].DeliveryID, "scan:security-alerts/coverage@") {
		t.Errorf("%s, %s", found[0].DeliveryID, found[1].DeliveryID)
	}
}

func TestAScanReadsOnlyTheTiersTheWorkflowWatches(t *testing.T) {
	api := api(t)
	if _, err := scan(t, api, true); err != nil {
		t.Fatal(err)
	}
	for _, target := range api.Targets() {
		if strings.Contains(target, "not-watched") {
			t.Errorf("read %s", target)
		}
	}
}

func TestAMissingTokenSkipsTheScan(t *testing.T) {
	api := api(t)
	found, err := scan(t, api, false)
	if err != nil || len(found) != 0 {
		t.Fatalf("%+v, err %v", found, err)
	}
	if got := api.Targets(); len(got) != 0 {
		t.Errorf("read %v", got)
	}
}

func TestAFailingRepositoryIsSkippedAndHoldsTheCoverageTask(t *testing.T) {
	api := api(t)
	api.Status("/repos/technicalpickles/example-public", 502)
	found, err := scan(t, api, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.HasPrefix(found[0].DeliveryID, "scan:security-alerts/technicalpickles/dotfiles@") {
		t.Fatalf("%+v", found)
	}
}

func TestEveryRepositoryFailingFailsTheScan(t *testing.T) {
	api := fakeapi.Serve(t)
	_, err := scan(t, api, true)
	if err == nil {
		t.Fatal("the scan passed with nothing to read")
	}
	for _, repo := range []string{"technicalpickles/dotfiles", "technicalpickles/example-private", "technicalpickles/example-public"} {
		if !strings.Contains(err.Error(), repo+" was not found on GitHub: check tiers.toml and local/tiers.toml") {
			t.Errorf("%v does not name %s", err, repo)
		}
	}
}

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
	w, err := securityalerts.New()
	if err != nil {
		t.Fatal(err)
	}
	log := &tracker{t: t}
	set := providersFor(t, api, true)
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

func TestALiveScanFilesOnceForEachDeliveryAndScanningAgainChangesNothing(t *testing.T) {
	rt, log := started(t, api(t), effect.Live)
	if got := backfill(t, rt); got.Found != 2 || got.Queued != 2 {
		t.Fatalf("%+v", got)
	}
	settle(t, rt)
	if again := backfill(t, rt); again.Unchanged != 2 || again.Queued != 0 {
		t.Fatalf("%+v", again)
	}
	settle(t, rt)

	filed := log.delivered()
	if len(filed) != 2 {
		t.Fatalf("%+v", filed)
	}
	byTarget := map[string]delivered{}
	for _, f := range filed {
		if f.key == "" {
			t.Error("the tracker got no key to repeat safely")
		}
		byTarget[f.target] = f
	}
	if byTarget["task:dotfiles"].title != "security-alerts: 1 overdue on technicalpickles/dotfiles (1 high)" ||
		byTarget["task:brined-almanac"].title != "security-alerts: alerts are off on 2 repositories" {
		t.Errorf("%+v", filed)
	}
}

func TestANewOverdueAlertIsANewDeliveryForTheSameRepository(t *testing.T) {
	api := api(t)
	rt, log := started(t, api, effect.Live)
	backfill(t, rt)
	settle(t, rt)

	api.Route("/repos/technicalpickles/dotfiles/dependabot/alerts?state=open&per_page=100", []any{
		dependabot(7, "high", "2020-01-01T00:00:00Z"),
		dependabot(9, "critical", "2020-01-01T00:00:00Z"),
	})
	if again := backfill(t, rt); again.Queued != 1 || again.Unchanged != 1 {
		t.Fatalf("%+v", again)
	}
	settle(t, rt)
	var titles []string
	for _, f := range log.delivered() {
		titles = append(titles, f.title)
	}
	if !strings.Contains(strings.Join(titles, "\n"), "security-alerts: 2 overdue on technicalpickles/dotfiles (1 critical, 1 high)") {
		t.Errorf("%v", titles)
	}
}

func TestADryRunCallsNoTracker(t *testing.T) {
	rt, log := started(t, api(t), effect.DryRun)
	if got := backfill(t, rt).Queued; got != 2 {
		t.Fatalf("queued %d", got)
	}
	settle(t, rt)
	if got := log.called(); got != 0 {
		t.Fatalf("a dry run called the tracker %d times", got)
	}
}
