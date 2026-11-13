package routing

import (
	"context"
	"fmt"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"
)

// Router sends task:<project> targets to the tasks tracker and everything else to the issues
// tracker.
type Router struct {
	Issues effect.Tracker
	Tasks  effect.Tracker
}

var _ effect.Tracker = Router{}

func (r Router) File(ctx context.Context, key string, issue effect.Issue) error {
	side, err := r.side(issue.Target)
	if err != nil {
		return err
	}
	return side.File(ctx, key, issue)
}

func (r Router) Ask(ctx context.Context, key string, question effect.Question) error {
	side, err := r.side(question.Target)
	if err != nil {
		return err
	}
	return side.Ask(ctx, key, question)
}

func (r Router) side(target hostile.Slug) (effect.Tracker, error) {
	_, isTask, err := FromTarget(target.String())
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s is not a task target: %w", target, err)
	case isTask:
		return r.Tasks, nil
	default:
		return r.Issues, nil
	}
}
