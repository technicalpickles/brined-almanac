// Package brine is brined-almanac's vocabulary: the events, observers, predicates and scans its
// workflow models are written with. It is the Go port of the parts of src/workflows that are
// not policy; the models are in its subpackages.
package brine

import (
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/vocab"

	"github.com/technicalpickles/brined-almanac/hoststate"
)

// Vocabulary holds every part of brined-almanac.
var Vocabulary = vocab.New()

var (
	needsHosts       = providers.Require[hoststate.Hosts]()
	needsHostReports = providers.Require[*HostReports]()
)
