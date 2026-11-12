package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ivy/almanac/model"
)

// DefaultFixtures is where the committed events are, from the repository root.
const DefaultFixtures = "compare/fixtures"

// All compares every case, Rust side first.
func All(ctx context.Context, cases []Case, rust Rust, golang Golang) (Report, error) {
	rusts, err := rust.Sides(ctx, cases)
	if err != nil {
		return Report{}, err
	}
	var compared []Compared
	for i, c := range cases {
		g, err := golang.Side(ctx, c)
		if err != nil {
			return Report{}, fmt.Errorf("the Go side failed on %s: %w", c.Name, err)
		}
		compared = append(compared, Compare(c, rusts[i], g))
	}
	return Tally(compared), nil
}

// Command is `compare`. It prints the report and exits non-zero when any case disagrees. It is
// a command and not a verb because a verb's output is lost when it returns an error.
func Command(models []model.Workflow) *cobra.Command {
	var fixtures, binary string
	var record bool
	cmd := &cobra.Command{
		Use:   "compare [--fixtures DIR] [--rust BIN [--record]]",
		Short: "Send each recorded event through the Go and Rust workflows and print where they disagree.",
		Long: `Send each recorded event through the Go and Rust workflows and print where they disagree.

Both sides run live with no token and no DATABASE_URL, so each filing goes to a LogTracker that
prints it. Each side's ingest and runs are compared: workflow, result, reason, effects with their
titles and targets, denied effects, recorded statuses, and each filing's tracker key and body. A
body is shown only as the numbers of the lines that differ and a hash of each side, never its text.
A case whose name starts with body-only differs from another case only in fields that reach the
body, so only the body comparison can tell it apart.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if record && binary == "" {
				return errors.New("compare --record needs --rust BIN, the Rust binary whose output to record")
			}
			cases, err := Cases(fixtures)
			if err != nil {
				return err
			}
			golang, err := NewGolang(models)
			if err != nil {
				return err
			}
			report, err := All(cmd.Context(), cases, Rust{Binary: binary, Record: record}, golang)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if format, _ := cmd.Flags().GetString("format"); format == "json" {
				text, err := json.Marshal(report)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(text))
			} else {
				fmt.Fprintln(out, Render(report))
			}
			if report.Disagreements > 0 {
				return fmt.Errorf("compare: %d of %d case(s) disagree", report.Disagreements, len(report.Cases))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fixtures, "fixtures", DefaultFixtures, "the events to compare, one directory per kind")
	cmd.Flags().StringVar(&binary, "rust", "", "run this Rust brined-almanac on each event, instead of reading the recordings")
	cmd.Flags().BoolVar(&record, "record", false, "with --rust, write what it printed beside each event")
	return cmd
}
