// Package walktest is a workflow.Cx for walking a model outside a runtime. A model that only
// observes through its vocabulary and decides needs nothing else, so everything else is refused.
package walktest

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ivy/almanac/agent"
	"github.com/ivy/almanac/cooled"
	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/workflow"
)

// Cx records by running the read and refuses a measure, hold, screen, agent or provider.
type Cx struct{}

func (Cx) Record(ctx context.Context, _ string, read func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return read(ctx)
}
func (Cx) Attempts(context.Context) (workflow.Attempts, error) { return workflow.Attempts{}, nil }
func (Cx) Age(context.Context, time.Duration, func(context.Context) (cooled.Published, error)) (cooled.Age, cooled.Cooled, error) {
	return 0, cooled.Cooled{}, errors.New("this model measures nothing")
}
func (Cx) Hold(context.Context, string, hostile.Slug, hostile.Sha) error {
	return errors.New("this model holds nothing")
}
func (Cx) Screen(context.Context, string, hostile.Of[string], float64) error {
	return errors.New("this model screens nothing")
}
func (Cx) Agent(context.Context, agent.Call) (hostile.Of[json.RawMessage], error) {
	return hostile.Of[json.RawMessage]{}, errors.New("this model calls no agent")
}
func (Cx) Provider(need providers.Requirement) (any, error) {
	return nil, errors.New("this model reads no provider in a run: " + need.Name())
}
