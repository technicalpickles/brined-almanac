package securityalerts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine/securityalerts"
	"github.com/technicalpickles/brined-almanac/taskwarrior"
)

// Pins steading-ai/almanac#34 and brined-almanac#43. Ivy's store keys an effect "{run_id}:{i}"
// and the run id comes from a sequence, so a store that restarts at 1 (a reset, a restore, a
// fresh database) reissues "run-1:0" for a different finding. The tracker rightly treats a key it
// has as a retry and files nothing, so the second finding is lost without an error. The tracker's
// dedupe is intended (TestTheSameKeyTwiceFilesOnce); the fix surface is the runtime minting keys
// that stay unique. This test goes through the runtime with the real taskwarrior tracker, so it
// goes green when upstream does.
//
// Skipped by default so the integration branch stays green. Run it with BRINED_PIN_OUTBOX_KEY=1.
func TestTwoFreshStoresFileTwoDifferentFindings(t *testing.T) {
	if os.Getenv("BRINED_PIN_OUTBOX_KEY") == "" {
		t.Skip("known bug: steading-ai/almanac#34, brined-almanac#43. Set BRINED_PIN_OUTBOX_KEY=1 to run it.")
	}
	if _, err := exec.LookPath("task"); err != nil {
		t.Fatal("this test runs the real `task` command (taskwarrior 3), which is not on PATH")
	}
	dir := t.TempDir()
	tw := taskwarrior.New(taskwarrior.Config{
		DataDir: filepath.Join(dir, "replica"),
		Server:  taskwarrior.Server{LocalDir: filepath.Join(dir, "server")},
	})

	for _, alert := range []struct {
		number   int
		severity string
	}{{7, "high"}, {9, "critical"}} {
		api := api(t)
		api.Route("/repos/technicalpickles/dotfiles/dependabot/alerts?state=open&per_page=100", []any{
			dependabot(alert.number, alert.severity, "2020-01-01T00:00:00Z"),
		})
		w, err := securityalerts.New()
		if err != nil {
			t.Fatal(err)
		}
		set := providersFor(t, api, true)
		providers.Provide[effect.Tracker](set, tw)
		rt := runtime.New([]workflow.Registered{w}, set, runtime.InMode(effect.Live))
		if err := rt.Validate(); err != nil {
			t.Fatal(err)
		}
		backfill(t, rt)
		settle(t, rt)
	}

	tasks, err := tw.Tasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var overdue []string
	for _, task := range tasks {
		if strings.Contains(task.Description, "overdue on technicalpickles/dotfiles") {
			overdue = append(overdue, task.Key+" "+task.Description)
		}
	}
	if len(overdue) != 2 {
		t.Fatalf("two stores, two different findings for dotfiles, want two tasks, got %d: %q (all keys: %v)", len(overdue), overdue, keys(tasks))
	}
}

func keys(tasks []taskwarrior.Task) []string {
	var out []string
	for _, task := range tasks {
		out = append(out, task.Key)
	}
	return out
}
