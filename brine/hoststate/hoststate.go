// Package hoststate files a task for each host check that has been failing long enough, and one
// when a host's report stops arriving. It is the Go port of src/workflows/host_state.rs; the
// rules for what is due live in the top-level hoststate package.
package hoststate

import (
	"github.com/ivy/almanac/engine"
	"github.com/ivy/almanac/hostile"

	. "github.com/ivy/almanac/dsl"

	"github.com/technicalpickles/brined-almanac/brine"
)

// Model is the workflow, as data the engine runs and the checker proves.
var Model = Define("host_state",
	Intent("File one task for each host check that has been failing long enough, and one when a host's report stops arriving."),
	On(brine.HostStateFinding),
	Key(subject),
	May(Files),
	Limits(PerHour(1000)),
	Scan(brine.ScanHostState),

	Observe("filing", "Find the project and the start", filing, brine.FileFinding, finding),
	Decide("file", "What does the task say?",
		Rule("check", "A check failing long enough", finding.Is(brine.IsCheck),
			File(target, Title("host-state: %s %s on %s since %s %s"), check, status, host, sinceDate, sinceTime).
				With(ShowingText(body))),
		Rule("stale_since", "No report since a known time", finding.Is(brine.HasSince),
			File(target, Title("host-state: no report from %s since %s %s"), host, sinceDate, sinceTime).
				With(ShowingText(body))),
		Otherwise("stale", "No report at all",
			File(target, Title("host-state: no report from %s"), host).
				With(ShowingText(body))),
	),
)

// The facts a run reads. The event is clean but its body, which the host wrote.
var (
	finding = EventOf(brine.HostStateFinding)
	subject = Field[hostile.Slug](finding, "subject")
	host    = Field[hostile.Slug](finding, "host")
	check   = Field[*hostile.Slug](finding, "check")
	status  = Field[*hostile.Slug](finding, "status")
	body    = HostileField[string](finding, "body")

	filing    = Fact[brine.Filing]("filing")
	target    = Field[hostile.Slug](filing, "target")
	since     = Field[*brine.Since](filing, "since")
	sinceDate = Field[hostile.Slug](since, "date")
	sinceTime = Field[hostile.Slug](since, "time")
)

// New is the workflow, ready for a runtime to run.
func New() (*engine.Workflow, error) {
	return engine.New(brine.Vocabulary, Model)
}
