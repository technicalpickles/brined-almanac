// Package compare sends the same recorded events through the Go port and the Rust
// brined-almanac and sets what each made of them side by side. Agreement is the evidence a
// workflow has ported (docs/adrs/0001-port-to-go-alongside-rust.md).
//
// Both sides are driven the way a live `simulate KIND FILE` drives them, with ALMANAC_MODE=live
// and nothing else in the environment: no token and no DATABASE_URL, so every filing goes to a
// LogTracker that prints it, and the store is in memory. The Rust side runs through its binary,
// or a recording of what that printed, and the Go side through Ivy's runtime in this process,
// with the models the binary runs and its LogTracker. What is compared is how the event was
// taken, each run's workflow, result, reason, effects, denied effects and recorded statuses,
// with runs in workflow order, and each filing's tracker key and body as the LogTracker printed
// them. A body carries the event's text, so a difference in one is shown as line numbers and
// hashes.
//
// Rust's `why` is not compared, because it is a fixed sentence per ingest, so the ingest
// already carries it: a parse failure is rejected and an event no workflow takes is unrouted.
package compare

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Side is what one implementation made of one event.
type Side struct {
	// Ingest is how the event was taken, such as queued or rejected.
	Ingest string `json:"ingest"`
	Runs   []Run  `json:"runs"`
}

// Run is one run the event started, as simulate reports it.
type Run struct {
	Workflow string `json:"workflow"`
	// Result is ran, stopped, failed or budget-tripped.
	Result  string   `json:"result"`
	Effects []Effect `json:"effects,omitempty"`
	// Denied are the kinds of the effects the run was not allowed, such as file, or
	// attest pending.
	Denied []string `json:"denied,omitempty"`
	// Recorded is where each effect is in delivery, such as would-apply.
	Recorded []string `json:"recorded,omitempty"`
	// Reason is why a run stopped, or the error it failed with.
	Reason string `json:"reason,omitempty"`
	// Filed is what the run's filings printed, in the order they were filed.
	Filed []Filed `json:"filed,omitempty"`
}

// Effect is the part of an effect both sides print.
type Effect struct {
	Kind   string `json:"kind"`
	Title  string `json:"title,omitempty"`
	Target string `json:"target,omitempty"`
}

func (e Effect) String() string {
	if e.Title == "" && e.Target == "" {
		return e.Kind
	}
	return fmt.Sprintf("%s %q to %q", e.Kind, e.Title, e.Target)
}

// Case is one recorded event: a payload of a kind. Its kind is the name of the directory it is
// in, so fixtures/host.state.finding/stale.json is a host.state.finding.
type Case struct {
	// Name is the kind and the file's name without .json, such as host.state.finding/stale.
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"-"`
}

// recordingSuffix ends the file beside a case that holds what the Rust binary printed for it:
// its LogTracker's filings, then simulate's JSON on the last line. It is not .json, so Cases
// never reads a recording as an event.
const recordingSuffix = ".rust.out"

// Cases are the events under dir, one directory per kind, in name order.
func Cases(dir string) ([]Case, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, path := range paths {
		kind := filepath.Base(filepath.Dir(path))
		out = append(out, Case{Name: kind + "/" + strings.TrimSuffix(filepath.Base(path), ".json"), Kind: kind, Path: path})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no events under %s: compare reads <kind>/<case>.json", dir)
	}
	return out, nil
}

// Recording is where the Rust binary's output for c is kept.
func (c Case) Recording() string {
	return strings.TrimSuffix(c.Path, ".json") + recordingSuffix
}

// Payload is the event's text.
func (c Case) Payload() ([]byte, error) {
	text, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", c.Path, err)
	}
	return text, nil
}

// Compared is one case on both sides.
type Compared struct {
	Case string `json:"case"`
	// Differences are what the sides disagree on, one line each. None is agreement.
	Differences []string `json:"differences"`
	Rust        Side     `json:"rust"`
	Go          Side     `json:"go"`
}

// Report is every case compared.
type Report struct {
	Cases []Compared `json:"cases"`
	// Disagreements is how many cases have a difference.
	Disagreements int `json:"disagreements"`
}

// Compare is the case's two sides, and every difference between them. Runs are put in
// workflow order first, since neither side promises the order an event's runs settle in.
// The order reads '-' as '_', because Rust names workflows with hyphens and Go with
// underscores, and the two sort on opposite sides of the digits.
func Compare(c Case, rust, golang Side) Compared {
	rust, golang = inWorkflowOrder(rust), inWorkflowOrder(golang)
	return Compared{Case: c.Name, Differences: differences(rust, golang), Rust: rust, Go: golang}
}

func inWorkflowOrder(s Side) Side {
	s.Runs = slices.Clone(s.Runs)
	slices.SortStableFunc(s.Runs, func(a, b Run) int { return strings.Compare(sortKey(a), sortKey(b)) })
	return s
}

func sortKey(r Run) string { return strings.ReplaceAll(r.Workflow, "-", "_") }

// Tally is the cases in a report, counting those that disagree.
func Tally(cases []Compared) Report {
	r := Report{Cases: cases}
	for _, c := range cases {
		if len(c.Differences) > 0 {
			r.Disagreements++
		}
	}
	return r
}

func differences(rust, golang Side) []string {
	var out []string
	differ := func(what, r, g string) {
		if r != g {
			out = append(out, fmt.Sprintf("%s: rust %s, go %s", what, shown(r), shown(g)))
		}
	}
	differ("ingest", rust.Ingest, golang.Ingest)
	if len(rust.Runs) != len(golang.Runs) {
		out = append(out, fmt.Sprintf("runs: rust %s, go %s", runsOf(rust.Runs), runsOf(golang.Runs)))
		return out
	}
	for i := range rust.Runs {
		r, g := rust.Runs[i], golang.Runs[i]
		at := fmt.Sprintf("run %d ", i+1)
		differ(at+"workflow", r.Workflow, g.Workflow)
		differ(at+"result", r.Result, g.Result)
		differ(at+"reason", r.Reason, g.Reason)
		differ(at+"denied", strings.Join(r.Denied, ", "), strings.Join(g.Denied, ", "))
		differ(at+"recorded", strings.Join(r.Recorded, ", "), strings.Join(g.Recorded, ", "))
		if !slices.Equal(r.Effects, g.Effects) {
			out = append(out, fmt.Sprintf("%seffects: rust %s, go %s", at, effectsOf(r.Effects), effectsOf(g.Effects)))
		}
		if len(r.Filed) != len(g.Filed) {
			out = append(out, fmt.Sprintf("%sfiled: rust %d, go %d", at, len(r.Filed), len(g.Filed)))
			continue
		}
		for j := range r.Filed {
			rf, gf := r.Filed[j], g.Filed[j]
			filing := fmt.Sprintf("%sfiling %d ", at, j+1)
			differ(filing+"key", rf.Key, gf.Key)
			if !slices.Equal(rf.Body, gf.Body) {
				out = append(out, filing+"body: "+bodyDifference(rf.Body, gf.Body))
			}
		}
	}
	return out
}

func shown(s string) string {
	if s == "" {
		return "(none)"
	}
	return fmt.Sprintf("%q", s)
}

func effectsOf(effects []Effect) string {
	if len(effects) == 0 {
		return "(none)"
	}
	parts := make([]string, len(effects))
	for i, e := range effects {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// Render is the report for a person: each case's verdict, its differences, and a total.
func Render(r Report) string {
	var b strings.Builder
	for _, c := range r.Cases {
		if len(c.Differences) == 0 {
			fmt.Fprintf(&b, "agree     %s: %s\n", c.Case, summary(c.Go))
			continue
		}
		fmt.Fprintf(&b, "DISAGREE  %s\n", c.Case)
		for _, d := range c.Differences {
			fmt.Fprintf(&b, "          %s\n", d)
		}
	}
	fmt.Fprintf(&b, "%d of %d case(s) disagree", r.Disagreements, len(r.Cases))
	return b.String()
}

func summary(s Side) string {
	if len(s.Runs) == 0 {
		return s.Ingest
	}
	return s.Ingest + ", " + runsOf(s.Runs)
}

func runsOf(runs []Run) string {
	if len(runs) == 0 {
		return "(no run)"
	}
	parts := make([]string, len(runs))
	for i, run := range runs {
		parts[i] = fmt.Sprintf("%q %s", run.Workflow, run.Result)
		if run.Reason != "" {
			parts[i] += fmt.Sprintf(" (%q)", run.Reason)
		}
		if len(run.Effects) > 0 {
			parts[i] += " " + effectsOf(run.Effects)
		}
	}
	return strings.Join(parts, "; ")
}
