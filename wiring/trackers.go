package wiring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/github"
	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/hostile/reveal"
)

// Visibility is whether a repository is private. Issue bodies carry transcript text, so this
// gates every delivery.
type Visibility interface {
	IsPrivate(ctx context.Context, repo hostile.Slug) (bool, error)
}

type githubVisibility struct{ client *github.Client }

func (v githubVisibility) IsPrivate(ctx context.Context, repo hostile.Slug) (bool, error) {
	found, err := v.client.Get(ctx, "/repos/"+repo.String())
	if err != nil {
		return false, err
	}
	var flag struct {
		Private *bool `json:"private"`
	}
	if err := hostile.DecodeClean(found, &flag); err != nil || flag.Private == nil {
		return false, fmt.Errorf("%s has no private flag in the response", repo)
	}
	return *flag.Private, nil
}

// PrivateOnly refuses every delivery to a repository that is not confirmed private. Only true
// answers are remembered, so a repository that goes public after a refusal is checked again.
type PrivateOnly struct {
	inner      effect.Tracker
	visibility Visibility

	mu      sync.Mutex
	private map[hostile.Slug]bool
}

func NewPrivateOnly(inner effect.Tracker, visibility Visibility) *PrivateOnly {
	return &PrivateOnly{inner: inner, visibility: visibility, private: map[hostile.Slug]bool{}}
}

func (p *PrivateOnly) check(ctx context.Context, target hostile.Slug) error {
	p.mu.Lock()
	known := p.private[target]
	p.mu.Unlock()
	if known {
		return nil
	}
	private, err := p.visibility.IsPrivate(ctx, target)
	if err != nil {
		return err
	}
	if !private {
		return fmt.Errorf("%s is not private, refusing to file", target)
	}
	p.mu.Lock()
	p.private[target] = true
	p.mu.Unlock()
	return nil
}

func (p *PrivateOnly) File(ctx context.Context, key string, issue effect.Issue) error {
	if err := p.check(ctx, issue.Target); err != nil {
		return err
	}
	return p.inner.File(ctx, key, issue)
}

func (p *PrivateOnly) Ask(ctx context.Context, key string, question effect.Question) error {
	if err := p.check(ctx, question.Target); err != nil {
		return err
	}
	return p.inner.Ask(ctx, key, question)
}

// StoreRequired stands in for the tracker on a run that would file with no durable store.
// Without one, every backfill re-admits every delivery id, so filing would repeat each cluster
// with its transcript text.
type StoreRequired struct{}

var errNoStore = errors.New("DATABASE_URL is not set; a live run without a durable store would file every cluster again")

func (StoreRequired) File(context.Context, string, effect.Issue) error   { return errNoStore }
func (StoreRequired) Ask(context.Context, string, effect.Question) error { return errNoStore }

// LogTracker prints what it would file. It stands in for a side with no credentials.
type LogTracker struct {
	// Out is where it prints. Nil is standard output.
	Out io.Writer
}

func (l LogTracker) File(_ context.Context, key string, issue effect.Issue) error {
	out := l.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "FILE key=%s target=%s title=%q\n%s\n", key, issue.Target, issue.Title, reveal.Value(issue.Body))
	return nil
}

func (l LogTracker) Ask(ctx context.Context, key string, question effect.Question) error {
	return l.File(ctx, key, effect.Issue{Title: question.Question, Target: question.Target, Body: question.Body})
}
