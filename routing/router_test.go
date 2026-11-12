package routing_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"

	"github.com/technicalpickles/brined-almanac/routing"
)

type recorder struct{ calls []string }

func (r *recorder) File(_ context.Context, key string, issue effect.Issue) error {
	r.calls = append(r.calls, "file "+key+" "+issue.Target.String())
	return nil
}

func (r *recorder) Ask(_ context.Context, key string, q effect.Question) error {
	r.calls = append(r.calls, "ask "+key+" "+q.Target.String())
	return nil
}

func route(t *testing.T, target string, ask bool) (issues, tasks *recorder, err error) {
	t.Helper()
	issues, tasks = &recorder{}, &recorder{}
	r := routing.Router{Issues: issues, Tasks: tasks}
	slug := hostile.MustParseSlug(target)
	if ask {
		err = r.Ask(context.Background(), "k", effect.Question{Question: "q", Target: slug, Body: hostile.New("b")})
	} else {
		err = r.File(context.Background(), "k", effect.Issue{Title: "t", Target: slug, Body: hostile.New("b")})
	}
	return issues, tasks, err
}

func TestATaskTargetGoesToTheTasksSide(t *testing.T) {
	for _, ask := range []bool{false, true} {
		issues, tasks, err := route(t, "task:homelab.backup", ask)
		if err != nil || len(issues.calls) != 0 || len(tasks.calls) != 1 {
			t.Errorf("ask=%v: err %v, issues %v, tasks %v", ask, err, issues.calls, tasks.calls)
		}
	}
}

func TestARepositoryTargetGoesToTheIssuesSide(t *testing.T) {
	for _, ask := range []bool{false, true} {
		issues, tasks, err := route(t, "technicalpickles/brineworks", ask)
		if err != nil || len(issues.calls) != 1 || len(tasks.calls) != 0 {
			t.Errorf("ask=%v: err %v, issues %v, tasks %v", ask, err, issues.calls, tasks.calls)
		}
	}
}

func TestATaskTargetWithABadProjectGoesNowhere(t *testing.T) {
	for _, ask := range []bool{false, true} {
		issues, tasks, err := route(t, "task:Bad", ask)
		if err == nil || !strings.Contains(err.Error(), "task:Bad is not a task target") {
			t.Errorf("ask=%v: got %v", ask, err)
		}
		if len(issues.calls)+len(tasks.calls) != 0 {
			t.Errorf("ask=%v: something was filed: %v %v", ask, issues.calls, tasks.calls)
		}
	}
}
