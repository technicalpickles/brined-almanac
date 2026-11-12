package resolveonclose_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/technicalpickles/brined-almanac/cutover"
	. "github.com/technicalpickles/brined-almanac/resolveonclose"
)

const (
	id    = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01"
	other = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e02"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func open(generation uint64) Open { return Open{ID: id, Generation: generation} }

func closed(reason cutover.Reason) cutover.Standing {
	return cutover.Standing{State: cutover.Closed, Reason: reason, At: at("2026-11-09 12:00")}
}

var stillOpen = cutover.Standing{State: cutover.Open}

func filed(number, generation uint64, created string, standing cutover.Standing) cutover.Linked {
	return cutover.Linked{
		URL:        fmt.Sprintf("gh/%d", number),
		Number:     number,
		Issue:      id,
		Generation: &generation,
		CreatedAt:  at(created),
		Standing:   standing,
	}
}

func resolve(number int) Verdict { return Verdict{Kind: Resolve, Proof: fmt.Sprintf("gh/%d", number)} }

var leave = Verdict{Kind: Leave}

func check(t *testing.T, got, want Verdict, why string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %+v, want %+v", why, got, want)
	}
}

func TestClosedAsCompletedResolvesAndCitesTheClose(t *testing.T) {
	gh := []cutover.Linked{filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))}
	check(t, Decide(open(0), gh), resolve(5), "completed")
}

func TestClosedAsNotPlannedMutes(t *testing.T) {
	gh := []cutover.Linked{filed(5, 0, "2026-11-05 09:00", closed(cutover.NotPlanned))}
	check(t, Decide(open(0), gh), Verdict{Kind: Mute, Proof: "gh/5"}, "not planned")
}

func TestACloseThatSaysNothingAboutWhyIsLeftAlone(t *testing.T) {
	gh := []cutover.Linked{filed(5, 0, "2026-11-05 09:00", closed(cutover.NoReason))}
	check(t, Decide(open(0), gh), leave, "no reason")
}

func TestTheNewestAlmanacIssueDecidesWhateverTheOlderOnesSay(t *testing.T) {
	oldClosed := filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))
	newOpen := filed(6, 0, "2026-11-07 09:00", stillOpen)
	check(t, Decide(open(0), []cutover.Linked{oldClosed, newOpen}), leave, "newer open issue")
	check(t, Decide(open(0), []cutover.Linked{newOpen, oldClosed}), leave, "the order of the list does not matter")

	oldOpen := filed(5, 0, "2026-11-05 09:00", stillOpen)
	newClosed := filed(6, 0, "2026-11-07 09:00", closed(cutover.Completed))
	check(t, Decide(open(0), []cutover.Linked{oldOpen, newClosed}), resolve(6), "newer closed issue")
}

func TestIssuesFiledInTheSameSecondAreOrderedByNumber(t *testing.T) {
	a := filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))
	b := filed(6, 0, "2026-11-05 09:00", stillOpen)
	check(t, Decide(open(0), []cutover.Linked{a, b}), leave, "higher number wins")
	check(t, Decide(open(0), []cutover.Linked{b, a}), leave, "whatever the order")
}

func TestARegressionIsNotResolvedAgainBeforeItsNewIssueFiles(t *testing.T) {
	old := filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))
	check(t, Decide(open(1), []cutover.Linked{old}), leave, "Bugsink is at generation 1, the closed issue was filed at 0")

	regression := filed(6, 1, "2026-11-09 09:00", stillOpen)
	check(t, Decide(open(1), []cutover.Linked{old, regression}), leave, "the regression's issue is open")
}

func TestABodyWhoseGenerationCouldNotBeReadNeverMatches(t *testing.T) {
	unreadable := filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))
	unreadable.Generation = nil
	check(t, Decide(open(0), []cutover.Linked{unreadable}), leave, "unreadable generation")
}

func TestABugsinkIssueWithNoAlmanacIssueIsLeftAlone(t *testing.T) {
	check(t, Decide(open(0), nil), leave, "no almanac issues")
	elsewhere := filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))
	elsewhere.Issue = other
	check(t, Decide(open(0), []cutover.Linked{elsewhere}), leave, "issue for another Bugsink issue")
}

func TestAMutedIssueThatWokeAfterTheCloseIsNotMutedAgain(t *testing.T) {
	gh := []cutover.Linked{filed(5, 0, "2026-11-05 09:00", closed(cutover.NotPlanned))}
	woke := func(s string) Open {
		o := open(0)
		w := at(s)
		o.UnmutedAt = &w
		return o
	}
	check(t, Decide(woke("2026-11-09 13:00"), gh), leave, "woke after the close")
	check(t, Decide(woke("2026-11-09 11:00"), gh), Verdict{Kind: Mute, Proof: "gh/5"}, "an unmute before the close is an older decision")
}

func TestAWakeDoesNotHoldBackACompletedClose(t *testing.T) {
	gh := []cutover.Linked{filed(5, 0, "2026-11-05 09:00", closed(cutover.Completed))}
	woke := open(0)
	w := at("2026-11-09 13:00")
	woke.UnmutedAt = &w
	check(t, Decide(woke, gh), resolve(5), "completed close with a later wake")
}

func TestCommentsCiteTheProof(t *testing.T) {
	check2 := func(got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	check2(ResolveComment("gh/5"), "Resolved by the almanac scan: its almanac issue was closed as completed.\n\n- gh/5")
	check2(MuteComment("gh/5"), "Muted by the almanac scan: its almanac issue was closed as not planned. It wakes at 50 events in a day.\n\n- gh/5")
}
