package routing_test

import (
	"strings"
	"testing"

	"github.com/technicalpickles/brined-almanac/routing"
)

func TestRealProjectNamesRoundTrip(t *testing.T) {
	for _, name := range []string{"picklehome", "homelab.backup", "pickled-claude-plugins.hk", "almanac"} {
		p, err := routing.ParseProject(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Target() != "task:"+name {
			t.Errorf("target = %s", p.Target())
		}
		back, isTask, err := routing.FromTarget(p.Target())
		if err != nil || !isTask || back.String() != name {
			t.Errorf("round trip %s = %v %v %v", name, back, isTask, err)
		}
	}
}

func TestRejectsNamesTaskwarriorWouldTakeButFiltersWouldNot(t *testing.T) {
	for _, bad := range []string{"Bad Name", "has/slash", "has:colon", "UPPER", "trail.", ".lead", "a..b", ""} {
		if _, err := routing.ParseProject(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRejectsAnOverlongName(t *testing.T) {
	if _, err := routing.ParseProject(strings.Repeat("a", 195)); err != nil {
		t.Errorf("195 bytes should fit: %v", err)
	}
	if _, err := routing.ParseProject(strings.Repeat("a", 196)); err == nil {
		t.Error("196 bytes should not fit")
	}
}

func TestAGithubTargetIsNotATaskTarget(t *testing.T) {
	_, isTask, err := routing.FromTarget("technicalpickles/brineworks")
	if isTask || err != nil {
		t.Errorf("isTask=%v err=%v", isTask, err)
	}
}

func TestATaskPrefixWithABadProjectIsAnErrorNotAMiss(t *testing.T) {
	for _, target := range []string{"task:Bad", "task:"} {
		_, isTask, err := routing.FromTarget(target)
		if !isTask || err == nil {
			t.Errorf("%s: isTask=%v err=%v", target, isTask, err)
		}
	}
}
