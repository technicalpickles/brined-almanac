// Command brined-almanac is the Go port. It runs beside the Rust binary and, until cutover, acts
// on nothing: ALMANAC_READ_ONLY is 1 unless the environment sets it. See
// docs/adrs/0001-port-to-go-alongside-rust.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ivy/almanac/app"
	"github.com/ivy/almanac/model"
	"github.com/ivy/almanac/providers"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/brine/bugsinkissues"
	"github.com/technicalpickles/brined-almanac/brine/hoststate"
	"github.com/technicalpickles/brined-almanac/brine/securityalerts"
	"github.com/technicalpickles/brined-almanac/compare"
	"github.com/technicalpickles/brined-almanac/cutover"
	"github.com/technicalpickles/brined-almanac/taskwarrior"
	"github.com/technicalpickles/brined-almanac/wiring"
)

// models are the workflows the binary runs, and the ones compare sends each recorded event to.
var models = []model.Workflow{hoststate.Model, securityalerts.Model, bugsinkissues.Model}

func main() {
	if _, set := os.LookupEnv("ALMANAC_READ_ONLY"); !set {
		_ = os.Setenv("ALMANAC_READ_ONLY", "1")
	}
	app.Almanac{
		Name:       "brined-almanac",
		Vocabulary: brine.Vocabulary,
		Models:     models,
		Setup: func(_ context.Context, s app.Setting) (*providers.Set, error) {
			return wiring.Providers(os.LookupEnv, s.Switches)
		},
		Verbs: []app.Verb{
			{Name: "sync-check", Short: "Pull the TaskChampion server's history into a throwaway replica, to prove the sync settings.", Run: syncCheck},
			{Name: "cutover", Args: "[--apply]", Short: "Print the plan for resolving history in Bugsink, or carry it out with --apply.", Run: runCutover, Flags: cutoverFlags},
		},
		Commands: func() []*cobra.Command { return []*cobra.Command{compare.Command(models)} },
	}.Main()
}

// syncCheck pulls the real server's history into a throwaway replica, so a wrong URL, client id
// or encryption secret fails here instead of at the first filing.
func syncCheck(ctx context.Context, _ app.Setting, _ []string) (app.Said, error) {
	dir, err := os.MkdirTemp("", "brined-almanac-sync-check-")
	if err != nil {
		return app.Said{}, fmt.Errorf("sync-check failed: %w", err)
	}
	defer os.RemoveAll(dir)
	config, err := wiring.SyncCheckConfig(os.LookupEnv, dir)
	if err != nil {
		return app.Said{}, fmt.Errorf("sync-check failed: %w", err)
	}
	pulled, err := taskwarrior.SyncCheck(ctx, config)
	if err != nil {
		return app.Said{}, fmt.Errorf("sync-check failed: %w", err)
	}
	return app.Said{
		Human: fmt.Sprintf("brined-almanac: sync ok, pulled %d task(s) from the server", pulled),
		JSON:  map[string]int{"pulled": pulled},
	}, nil
}

func cutoverFlags(flags *pflag.FlagSet) {
	flags.Bool("apply", false, "resolve and comment, instead of printing the plan")
}

// runCutover resolves history in Bugsink: cutover prints the plan, and cutover --apply carries
// it out unless ALMANAC_READ_ONLY is set.
func runCutover(ctx context.Context, s app.Setting, args []string) (app.Said, error) {
	if len(args) > 0 {
		return app.Said{}, fmt.Errorf("cutover takes only --apply, not %s", args[0])
	}
	apply, err := s.Flags.GetBool("apply")
	if err != nil {
		return app.Said{}, err
	}
	if apply && s.Switches.ReadOnly {
		return app.Said{}, errors.New("cutover --apply resolves Bugsink issues, and ALMANAC_READ_ONLY is set; set ALMANAC_READ_ONLY=0 to apply")
	}
	var out strings.Builder
	if err := cutover.Run(ctx, os.LookupEnv, apply, &out); err != nil {
		// What was printed before the failure, such as the issues already resolved, still shows.
		_, _ = os.Stdout.WriteString(out.String())
		return app.Said{}, fmt.Errorf("cutover failed: %w", err)
	}
	report := strings.TrimSuffix(out.String(), "\n")
	return app.Said{Human: report, JSON: map[string]any{"apply": apply, "report": report}}, nil
}
