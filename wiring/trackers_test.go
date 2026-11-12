package wiring_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"

	"github.com/technicalpickles/brined-almanac/wiring"
)

type fakeVisibility struct {
	private bool
	err     error

	mu    sync.Mutex
	asked []string
}

func (v *fakeVisibility) IsPrivate(_ context.Context, repo hostile.Slug) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, repo.String())
	return v.private, v.err
}

// recorder keeps the key of every delivery it is handed.
type recorder struct {
	mu    sync.Mutex
	filed []string
}

func (r *recorder) File(_ context.Context, key string, _ effect.Issue) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filed = append(r.filed, key)
	return nil
}

func (r *recorder) Ask(_ context.Context, key string, _ effect.Question) error {
	return r.File(context.Background(), key, effect.Issue{})
}

func (r *recorder) Filed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.filed
}

var target = hostile.MustParseSlug("technicalpickles/brineworks")

func issue(title string) effect.Issue {
	return effect.Issue{Title: title, Target: target, Body: hostile.New("synthetic body")}
}

func question() effect.Question {
	return effect.Question{Question: "question?", Target: target, Body: hostile.New("synthetic body")}
}

func TestRefusesAPublicTargetAndFilesNothing(t *testing.T) {
	inner := &recorder{}
	err := wiring.NewPrivateOnly(inner, &fakeVisibility{}).File(context.Background(), "k1", issue("title"))
	if err == nil || !strings.Contains(err.Error(), "technicalpickles/brineworks") || strings.Contains(err.Error(), "synthetic body") {
		t.Fatal(err)
	}
	if len(inner.Filed()) != 0 {
		t.Fatal(inner.Filed())
	}
}

func TestFilesOnAPrivateTarget(t *testing.T) {
	inner := &recorder{}
	if err := wiring.NewPrivateOnly(inner, &fakeVisibility{private: true}).File(context.Background(), "k1", issue("title")); err != nil {
		t.Fatal(err)
	}
	if len(inner.Filed()) != 1 {
		t.Fatal(inner.Filed())
	}
}

func TestAFailedLookupFilesNothing(t *testing.T) {
	inner := &recorder{}
	err := wiring.NewPrivateOnly(inner, &fakeVisibility{err: errors.New("boom")}).File(context.Background(), "k1", issue("title"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
	if len(inner.Filed()) != 0 {
		t.Fatal(inner.Filed())
	}
}

func TestAsksOnceForAPrivateTarget(t *testing.T) {
	visibility := &fakeVisibility{private: true}
	tracker := wiring.NewPrivateOnly(&recorder{}, visibility)
	for _, key := range []string{"k1", "k2"} {
		if err := tracker.File(context.Background(), key, issue(key)); err != nil {
			t.Fatal(err)
		}
	}
	if len(visibility.asked) != 1 {
		t.Fatal(visibility.asked)
	}
}

func TestAsksAgainAfterAPublicAnswer(t *testing.T) {
	visibility := &fakeVisibility{}
	tracker := wiring.NewPrivateOnly(&recorder{}, visibility)
	for _, key := range []string{"k1", "k2"} {
		if tracker.File(context.Background(), key, issue(key)) == nil {
			t.Fatal("filed on a public target")
		}
	}
	if len(visibility.asked) != 2 {
		t.Fatal(visibility.asked)
	}
}

func TestAskIsGuardedLikeFile(t *testing.T) {
	inner := &recorder{}
	err := wiring.NewPrivateOnly(inner, &fakeVisibility{}).Ask(context.Background(), "k1", question())
	if err == nil || !strings.Contains(err.Error(), "technicalpickles/brineworks") {
		t.Fatal(err)
	}
	if len(inner.Filed()) != 0 {
		t.Fatal(inner.Filed())
	}
}

func TestALiveRunWithoutAStoreRefusesToFileOrAsk(t *testing.T) {
	tracker := wiring.TrackerFrom(wiring.StoreRequired{})
	filed := tracker.File(context.Background(), "k1", issue("title"))
	asked := tracker.Ask(context.Background(), "k1", question())
	for _, err := range []error{filed, asked} {
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") ||
			strings.Contains(err.Error(), "synthetic body") || strings.Contains(err.Error(), "technicalpickles/brineworks") {
			t.Fatal(err)
		}
	}
}

func TestAFailedLookupIsAskedAgain(t *testing.T) {
	visibility := &fakeVisibility{err: errors.New("boom")}
	inner := &recorder{}
	tracker := wiring.NewPrivateOnly(inner, visibility)
	for _, key := range []string{"k1", "k2"} {
		if tracker.File(context.Background(), key, issue(key)) == nil {
			t.Fatal("filed after a failed lookup")
		}
	}
	if len(visibility.asked) != 2 || len(inner.Filed()) != 0 {
		t.Fatal(visibility.asked, inner.Filed())
	}
}
