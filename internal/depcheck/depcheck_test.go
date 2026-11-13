package depcheck_test

import (
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"
)

func TestUpstreamIsImportable(t *testing.T) {
	var _ effect.Tracker
	_ = hostile.New("x")
}
