// Package resolveonclose decides what a closed almanac issue means for its Bugsink issue, so
// that closing one never silences its failure.
package resolveonclose

import (
	"fmt"
	"time"

	"github.com/technicalpickles/brined-almanac/cutover"
)

// MuteWakeEvents is the events in one day that wake an issue muted because its almanac issue
// closed as not planned. A default to tune: a wrong not-planned close would otherwise be silent
// forever.
const MuteWakeEvents = 50

// Open is an unresolved, unmuted Bugsink issue and what its history says.
type Open struct {
	ID string
	// Generation is how many times it came back after being resolved, as bugsinkapi.Generation
	// counts.
	Generation uint64
	UnmutedAt  *time.Time
}

type Kind int

const (
	Leave Kind = iota
	Resolve
	Mute
)

// Verdict is what to do with one Bugsink issue. Proof is the closed almanac issue's URL, empty
// under Leave.
type Verdict struct {
	Kind  Kind
	Proof string
}

// Decide acts only when the newest almanac issue for the Bugsink issue is closed, was filed for
// its current generation, and says why it closed.
//
// Newest wins because an older closed issue says nothing about a failure that came back. The
// generation must match because a regression stays open in Bugsink until its new almanac issue
// files, and the old closed issue still carries the same link: without the check, the scan would
// resolve the regression again before it was ever reported.
//
// A not-planned close is skipped when the issue was unmuted since: its volume condition fired or
// a person woke it, and muting it again would swallow the wake.
func Decide(open Open, linked []cutover.Linked) Verdict {
	var newest *cutover.Linked
	for i := range linked {
		l := &linked[i]
		if l.Issue != open.ID {
			continue
		}
		if newest == nil || l.CreatedAt.After(newest.CreatedAt) ||
			l.CreatedAt.Equal(newest.CreatedAt) && l.Number > newest.Number {
			newest = l
		}
	}
	if newest == nil || newest.Generation == nil || *newest.Generation != open.Generation {
		return Verdict{Kind: Leave}
	}
	if newest.Standing.State != cutover.Closed {
		return Verdict{Kind: Leave}
	}
	switch newest.Standing.Reason {
	case cutover.Completed:
		return Verdict{Kind: Resolve, Proof: newest.URL}
	case cutover.NotPlanned:
		if open.UnmutedAt == nil || open.UnmutedAt.Before(newest.Standing.At) {
			return Verdict{Kind: Mute, Proof: newest.URL}
		}
	}
	return Verdict{Kind: Leave}
}

func ResolveComment(proof string) string {
	return "Resolved by the almanac scan: its almanac issue was closed as completed.\n\n- " + proof
}

func MuteComment(proof string) string {
	return fmt.Sprintf("Muted by the almanac scan: its almanac issue was closed as not planned. It wakes at %d events in a day.\n\n- %s", MuteWakeEvents, proof)
}
