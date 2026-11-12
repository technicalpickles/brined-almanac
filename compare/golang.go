package compare

import (
	"bytes"
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
	"github.com/technicalpickles/brined-almanac/routing"
	"github.com/technicalpickles/brined-almanac/wiring"
)

// Golang is the Go side of a comparison: the binary's own models, run in this process.
type Golang struct {
	workflows []workflow.Registered
	providers *providers.Set
}

// live is how the Rust side is run: ALMANAC_MODE=live, and in Go, ALMANAC_READ_ONLY=0.
var live = runtime.Switches{Mode: effect.Live}

// noEnv is an empty environment. The Rust side is given only PATH and the mode.
func noEnv(string) (string, bool) { return "", false }

// NewGolang makes the models runnable, as the binary does when it starts, with the providers
// the binary builds from an empty environment.
func NewGolang(models []model.Workflow) (Golang, error) {
	set, err := wiring.Providers(noEnv, live)
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
// as simulate sends a file. Its LogTracker prints into a buffer of the case's own, which is the
// tracker the binary builds with no token on either side of the router.
func (g Golang) Side(ctx context.Context, c Case) (Side, error) {
	payload, err := c.Payload()
	if err != nil {
		return Side{}, err
	}
	var printed bytes.Buffer
	logs := wiring.LogTracker{Out: &printed}
	set := providers.Provide[effect.Tracker](g.providers.Clone(), routing.Router{Issues: logs, Tasks: logs})
	rt := runtime.New(g.workflows, set, runtime.Switched(live), runtime.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
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
	var runIDs []string
	for _, o := range outcomes {
		runIDs = append(runIDs, o.RunID)
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
	filings, err := parseFilings(printed.String())
	if err != nil {
		return Side{}, fmt.Errorf("the Go LogTracker %w", err)
	}
	if err := fileUnder(side.Runs, runIDs, filings); err != nil {
		return Side{}, fmt.Errorf("the Go LogTracker %w", err)
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
