package securityalerts

import (
	"encoding/json"

	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/hostile/reveal"
)

// plain opens GitHub's alerts, so that a scan can write what they say into an event's payload.
// A payload is plain JSON and wire.Payload.Text reads a plain string, so no other way carries
// package names and manifest paths from a scan to the event, which holds them hostile again.
func plain(items []hostile.Of[json.RawMessage]) []json.RawMessage {
	opened := make([]json.RawMessage, len(items))
	for i, item := range items {
		opened[i] = reveal.Value(item)
	}
	return opened
}
