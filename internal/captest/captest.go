// Package captest drives one model through a runtime with more events than its hourly budget
// allows, so a test can assert that the budget holds the rest.
package captest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"
	"github.com/ivy/almanac/store"
	"github.com/ivy/almanac/workflow"
)

// Result is how many runs went ahead and what the budget held back.
type Result struct {
	Ran     int
	Tripped []runtime.Outcome
}

// Overrun ingests events distinct events for w, one per index, and drains the runtime.
func Overrun(t *testing.T, w workflow.Registered, events int, payload func(i int) json.RawMessage) Result {
	t.Helper()
	ctx := context.Background()
	rt := runtime.New([]workflow.Registered{w}, providers.New(), runtime.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	for i := range events {
		admitted, err := rt.Ingest(ctx, runtime.Envelope{Source: "test", DeliveryID: fmt.Sprint(i), Kind: w.Kind(), Payload: payload(i)})
		if err != nil {
			t.Fatal(err)
		}
		if admitted != store.Queued {
			t.Fatalf("event %d: %v, want %v", i, admitted, store.Queued)
		}
	}
	outcomes, err := rt.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var r Result
	for _, o := range outcomes {
		switch o.Result {
		case runtime.BudgetTripped:
			r.Tripped = append(r.Tripped, o)
		case runtime.Ran:
			r.Ran++
		default:
			t.Fatalf("unexpected outcome %v: %s", o.Result, o.Reason)
		}
	}
	return r
}
