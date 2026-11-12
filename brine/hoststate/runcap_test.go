package hoststate_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/technicalpickles/brined-almanac/brine/hoststate"
	"github.com/technicalpickles/brined-almanac/internal/captest"
)

func TestTheThousandAndFirstRunInAnHourIsHeld(t *testing.T) {
	w, err := hoststate.New()
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(scanned(t, map[string]string{"mac.json": critical(t)}), &fields); err != nil {
		t.Fatal(err)
	}
	got := captest.Overrun(t, w, 1001, func(i int) json.RawMessage {
		fields["subject"] = fmt.Sprintf("mac%d.gh-auth.2026-11-10T14:50Z", i)
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	})
	if got.Ran != 1000 || len(got.Tripped) != 1 || got.Tripped[0].Held != 1 {
		t.Fatalf("ran %d, tripped %+v, want 1000 ran and one budget trip holding 1", got.Ran, got.Tripped)
	}
}
