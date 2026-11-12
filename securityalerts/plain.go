package securityalerts

import (
	"encoding/json"

	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/hostile/reveal"
)

// plain opens GitHub's alerts, so that a scan can write what they say into an event's payload.
// A payload is plain JSON and wire.Payload.Text reads a plain string, so no other way carries
// package names and manifest paths from a scan to the event, which holds them hostile again.
//
// hostile.Narrow and hostile.Classify cannot stand in for it: they answer a yes-or-no or a
// closed set, and the text itself has to reach the payload. Decoding the names into hostile.Slug
// would refuse a manifest path with a space in it, which Rust writes into the event as it is.
// Keeping the lines hostile through the payload fails at wire.Payload.Text, which reads a plain
// string and not the tagged object a hostile.Of marshals to.
func plain(items []hostile.Of[json.RawMessage]) []json.RawMessage {
	opened := make([]json.RawMessage, len(items))
	for i, item := range items {
		opened[i] = reveal.Value(item)
	}
	return opened
}
