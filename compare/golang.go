package compare

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/engine"
	"github.com/ivy/almanac/model"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"
	"github.com/ivy/almanac/store"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/wiring"
)

// Golang is the Go side of a comparison: the binary's own models, run in this process.
type Golang struct {
	workflows []workflow.Registered
	providers *providers.Set
}

// readOnly is how the binary runs until cutover: a dry run that refuses every write.
var readOnly = runtime.Switches{Mode: effect.DryRun, ReadOnly: true}

// noEnv is an empty environment, as the Rust side is given.
func noEnv(string) (string, bool) { return "", false }

// NewGolang makes the models runnable, as the binary does when it starts, with the providers
// the binary builds from an empty environment.
func NewGolang(models []model.Workflow) (Golang, error) {
	set, err := wiring.Providers(noEnv, readOnly)
	if err != nil {
		return Golang{}, err
	}
	g := Golang{providers: set}
	for _, m := range models {
		w, err := engine.New(brine.Vocabulary, m)
		if err != nil {
			return Golang{}, err
		}
		g.workflows = append(g.workflows, w)
	}
	return g, nil
}

// Side is what the Go side made of c: a fresh runtime, as a fresh process would have, sent c
// as simulate sends a file.
func (g Golang) Side(ctx context.Context, c Case) (Side, error) {
	payload, err := c.Payload()
	if err != nil {
		return Side{}, err
	}
	rt := runtime.New(g.workflows, g.providers, runtime.Switched(readOnly), runtime.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err := rt.Validate(); err != nil {
		return Side{}, err
	}
	ingest, err := rt.Ingest(ctx, runtime.Envelope{
		Source:     "simulate",
		DeliveryID: fmt.Sprintf("%s:%d", c.Path, len(payload)),
		Kind:       c.Kind,
		Origin:     store.Simulated,
		Payload:    payload,
	})
	if err != nil {
		return Side{}, err
	}
	outcomes, err := rt.Settle(ctx)
	if err != nil {
		return Side{}, err
	}
	side := Side{Ingest: string(ingest), Runs: []Run{}}
	for _, o := range outcomes {
		run := Run{Workflow: o.Workflow, Result: o.Result.String(), Reason: o.Reason}
		for _, e := range o.Effects {
			run.Effects = append(run.Effects, effectOf(e))
		}
		for _, d := range o.Denied {
			state := ""
			if d.Attests != 0 {
				state = kebab(d.Attests.String())
			}
			run.Denied = append(run.Denied, denied(kebab(d.Kind.String()), state))
		}
		if o.Result == runtime.Ran {
			entries, err := rt.EffectsOf(ctx, o.RunID)
			if err != nil {
				return Side{}, err
			}
			for _, entry := range entries {
				run.Recorded = append(run.Recorded, string(entry.Status))
			}
		}
		side.Runs = append(side.Runs, run)
	}
	return side, nil
}

func effectOf(e effect.Effect) Effect {
	out := Effect{Kind: kebab(e.Kind().String())}
	switch e := e.(type) {
	case effect.File:
		out.Title, out.Target = e.Title.String(), e.Target.String()
	case effect.Ask:
		out.Title, out.Target = e.Question.String(), e.Target.String()
	}
	return out
}
