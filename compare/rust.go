package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Rust is the Rust side of a comparison: the binary to run, or, with none, the recordings
// beside each case.
type Rust struct {
	// Binary is the Rust brined-almanac to run on each case. Empty reads the recordings.
	Binary string
	// Record writes what Binary printed beside each case, for later comparisons to read.
	Record bool
}

// Side is what the Rust side made of c.
func (r Rust) Side(ctx context.Context, c Case) (Side, error) {
	if r.Binary == "" {
		text, err := os.ReadFile(c.Recording())
		if err != nil {
			return Side{}, fmt.Errorf("no recording of the Rust binary for %s (run compare --rust BIN --record): %w", c.Name, err)
		}
		return parseSimulate(text)
	}
	text, err := r.simulate(ctx, c)
	if err != nil {
		return Side{}, err
	}
	side, err := parseSimulate(text)
	if err != nil {
		return Side{}, err
	}
	if r.Record {
		if err := refuseUnrouting(c, side); err != nil {
			return Side{}, err
		}
		if err := os.WriteFile(c.Recording(), text, 0o644); err != nil {
			return Side{}, fmt.Errorf("recording %s: %w", c.Name, err)
		}
	}
	return side, nil
}

// unrouted is the ingest of an event no workflow takes.
const unrouted = "unrouted"

// refuseUnrouting stops a recording that a binary without c's workflow would make: one that
// turns a case the old recording routed into unrouted. That is a stale or wrong binary far more
// often than a change to Rust, and recording it would make the comparison agree on nothing.
func refuseUnrouting(c Case, now Side) error {
	if now.Ingest != unrouted {
		return nil
	}
	text, err := os.ReadFile(c.Recording())
	if err != nil {
		return nil
	}
	was, err := parseSimulate(text)
	if err != nil || was.Ingest == unrouted {
		return nil
	}
	return fmt.Errorf("not recording %s: the Rust binary leaves it unrouted, but its recording has it %s, "+
		"so the binary is likely stale or not brined-almanac; build it from main, or delete %s to record anyway",
		c.Name, was.Ingest, c.Recording())
}

// simulate runs `BIN --format json simulate KIND FILE --dry-run` with nothing in its
// environment but PATH, so it reaches nothing and reads only the shared config files.
func (r Rust) simulate(ctx context.Context, c Case) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.Binary, "--format", "json", "simulate", c.Kind, c.Path, "--dry-run")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("the Rust binary failed on %s: %w: %s", c.Name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// simulated is what the Rust `--format json simulate` prints: the ingest as its {:?}, and each
// outcome as its {:?}, followed for a run by " recorded as [Status, ...]".
type simulated struct {
	Simulate string   `json:"simulate"`
	Outcomes []string `json:"outcomes"`
}

func parseSimulate(text []byte) (Side, error) {
	var s simulated
	if err := json.Unmarshal(text, &s); err != nil {
		return Side{}, fmt.Errorf("the Rust output is not simulate's JSON: %w", err)
	}
	side := Side{Ingest: kebab(s.Simulate), Runs: []Run{}}
	for _, line := range s.Outcomes {
		run, err := parseOutcome(line)
		if err != nil {
			return Side{}, err
		}
		side.Runs = append(side.Runs, run)
	}
	return side, nil
}

func parseOutcome(line string) (Run, error) {
	outcome, rest, err := parseDebug(line)
	if err != nil {
		return Run{}, err
	}
	run := Run{Result: kebab(outcome.name), Workflow: outcome.fields["workflow"].str()}
	switch outcome.name {
	case "Stopped":
		run.Reason = outcome.fields["reason"].str()
	case "Failed":
		run.Reason = outcome.fields["error"].str()
	case "BudgetTripped":
		if w := outcome.fields["workflow"]; w.name == "Some" {
			run.Workflow = w.str()
		} else {
			run.Workflow = ""
		}
	}
	for _, e := range outcome.fields["effects"].items {
		run.Effects = append(run.Effects, Effect{Kind: kebab(e.name), Title: titleOf(e), Target: e.fields["target"].str()})
	}
	for _, d := range outcome.fields["denied"].items {
		run.Denied = append(run.Denied, denied(kebab(d.fields["kind"].str()), kebab(d.fields["attests"].str())))
	}
	if statuses, ok := strings.CutPrefix(strings.TrimSpace(rest), "recorded as "); ok {
		list, _, err := parseDebug(statuses)
		if err != nil {
			return Run{}, err
		}
		for _, status := range list.items {
			run.Recorded = append(run.Recorded, kebab(status.name))
		}
	}
	return run, nil
}

// denied is a denied effect as both sides can say it: its kind, and an Attest's state. Rust
// writes no state as None, and Go as an empty one.
func denied(kind, attests string) string {
	if attests == "" || attests == "none" {
		return kind
	}
	return kind + " " + attests
}

// titleOf is a File's title or an Ask's question.
func titleOf(e debug) string {
	if title, ok := e.fields["title"]; ok {
		return title.str()
	}
	return e.fields["question"].str()
}
