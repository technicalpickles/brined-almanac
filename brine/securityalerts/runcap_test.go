package securityalerts_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/technicalpickles/brined-almanac/brine/securityalerts"
	"github.com/technicalpickles/brined-almanac/internal/captest"
)

func TestTheThousandAndFirstRunInAnHourIsHeld(t *testing.T) {
	w, err := securityalerts.New()
	if err != nil {
		t.Fatal(err)
	}
	got := captest.Overrun(t, w, 1001, func(i int) json.RawMessage {
		return overdueEvent(t, func(p map[string]any) { p["subject"] = fmt.Sprintf("technicalpickles/repo%d", i) })
	})
	if got.Ran != 1000 || len(got.Tripped) != 1 || got.Tripped[0].Held != 1 {
		t.Fatalf("ran %d, tripped %+v, want 1000 ran and one budget trip holding 1", got.Ran, got.Tripped)
	}
}
