// Package bugsinkissues files a GitHub issue for each Bugsink issue of failed tool calls that
// nobody has resolved or muted. It is the Go port of src/workflows/bugsink_issues.rs.
package bugsinkissues

import (
	"github.com/ivy/almanac/engine"
	"github.com/ivy/almanac/hostile"

	. "github.com/ivy/almanac/dsl"

	"github.com/technicalpickles/brined-almanac/brine"
)

// Model is the workflow, as data the engine runs and the checker proves.
var Model = Define("bugsink_issues",
	Intent("File one issue per Bugsink issue of failed tool calls that nobody has resolved or muted."),
	On(brine.BugsinkUnaddressed),
	Key(source, issue, generation),
	May(Files),
	Limits(PerHour(1000)),
	Scan(brine.ScanBugsinkIssues),

	Observe("filing", "Find the repository and write the body", filing, brine.FileIssue, unaddressed),
	Decide("file", "What does the issue say?",
		Rule("regressed", "It came back after being resolved", unaddressed.Is(brine.IsRegression),
			File(target, Title("Brent: %s/%s %s regressed (%d events)"), service, tool, class, count).
				With(ShowingText(body))),
		Otherwise("first", "Nobody has resolved it before",
			File(target, Title("Brent: %s/%s %s (%d events)"), service, tool, class, count).
				With(ShowingText(body))),
	),
)

var (
	unaddressed = EventOf(brine.BugsinkUnaddressed)
	source      = Field[hostile.Slug](unaddressed, "source")
	issue       = Field[hostile.Slug](unaddressed, "issue")
	generation  = Field[uint64](unaddressed, "generation")
	service     = Field[hostile.Slug](unaddressed, "service")
	tool        = Field[hostile.Slug](unaddressed, "tool")
	class       = Field[hostile.Slug](unaddressed, "class")
	count       = Field[uint64](unaddressed, "count")

	filing = Fact[brine.IssueFiling]("filing")
	target = Field[hostile.Slug](filing, "target")
	body   = HostileField[string](filing, "body")
)

// New is the workflow, ready for a runtime to run.
func New() (*engine.Workflow, error) {
	return engine.New(brine.Vocabulary, Model)
}
