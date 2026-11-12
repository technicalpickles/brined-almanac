// Package securityalerts files a task for each repository with overdue security alerts, and one
// when alerts are off on some repositories. It is the Go port of the workflow half of
// src/workflows/security_alerts.rs; the rules for which alerts are overdue are in the top-level
// securityalerts package.
package securityalerts

import (
	"strings"

	"github.com/ivy/almanac/engine"
	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/model"

	. "github.com/ivy/almanac/dsl"

	"github.com/technicalpickles/brined-almanac/brine"
)

// Model is the workflow, as data the engine runs and the checker proves.
var Model = Define("security_alerts",
	Intent("File one task per repository with overdue security alerts, and one for repositories where alerts are off."),
	On(brine.SecurityAlertFinding),
	Key(subject),
	May(Files),
	Limits(PerHour(1000)),
	Scan(brine.ScanSecurityAlerts),

	Observe("filing", "Find the project and total the overdue alerts", filing, brine.FileSecurity, finding),
	Decide("file", "What does the task say?", rules()...),
)

var (
	finding      = EventOf(brine.SecurityAlertFinding)
	subject      = Field[hostile.Slug](finding, "subject")
	repositories = Field[uint64](finding, "repositories")
	lines        = HostileField[string](finding, "lines")

	filing = Fact[brine.SecurityFiling]("filing")
	target = Field[hostile.Slug](filing, "target")
	total  = Field[uint64](filing, "total")
)

// counted are the counts a title lists, in the order it lists them. A mask has bit i set when
// counted[i] is above zero.
var counted = []struct {
	name  string
	count CleanFact[uint64]
}{
	{"critical", Field[uint64](finding, "critical")},
	{"high", Field[uint64](finding, "high")},
	{"medium", Field[uint64](finding, "medium")},
	{"unknown", Field[uint64](finding, "unknown")},
	{"low", Field[uint64](finding, "low")},
	{"secret", Field[uint64](finding, "secrets")},
}

func rules() []model.Rule {
	out := []model.Rule{
		Rule("coverage_one", "Alerts are off on one repository",
			All(finding.Is(brine.IsCoverage), finding.Is(brine.IsOneRepository)),
			File(target, Title("security-alerts: alerts are off on %d repository"), repositories).
				With(ShowingText(lines))),
		Rule("coverage", "Alerts are off on several repositories", finding.Is(brine.IsCoverage),
			File(target, Title("security-alerts: alerts are off on %d repositories"), repositories).
				With(ShowingText(lines))),
	}
	for mask := 1; mask < len(overdueTitles); mask++ {
		out = append(out, overdueRule(mask))
	}
	return append(out, Otherwise("overdue_none", "Overdue, with nothing counted",
		File(target, overdueTitles[0], total, subject).With(ShowingText(lines))))
}

// overdueRule is the task for the overdue finding whose counts above zero are those of mask.
func overdueRule(mask int) model.Rule {
	var names []string
	var conditions []model.Cond
	values := []Clean{total, subject}
	for i, c := range counted {
		if mask>>i&1 == 1 {
			names = append(names, c.name)
			conditions = append(conditions, c.count.Is(brine.IsPositive))
			values = append(values, c.count)
		} else {
			conditions = append(conditions, Not(c.count.Is(brine.IsPositive)))
		}
	}
	return Rule("overdue_"+strings.Join(names, "_"), "Overdue: "+strings.Join(names, ", "),
		All(conditions...),
		File(target, overdueTitles[mask], values...).With(ShowingText(lines)))
}

// New is the workflow, ready for a runtime to run.
func New() (*engine.Workflow, error) {
	return engine.New(brine.Vocabulary, Model)
}
