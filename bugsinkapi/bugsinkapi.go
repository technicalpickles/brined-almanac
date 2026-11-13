// Package bugsinkapi is what the tracker consumer reads from Bugsink's API: issues, their tags,
// and their history. It is the Go port of src/bugsink_api.rs and the read half of the Bugsink
// client in src/cutover.rs, which owns the paging.
package bugsinkapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// timeout is src/bugsink.rs TIMEOUT.
const timeout = 10 * time.Second

// Issue is one issue as GET /issues/ lists it.
type Issue struct {
	ID         string `json:"id"`
	FriendlyID string `json:"friendly_id"`
	// FirstSeen is when Bugsink ingested the first event. Only a fallback for dating an issue
	// with no usable event times.
	FirstSeen string `json:"first_seen"`
	// LastSeen is when Bugsink ingested the last event. Only a fallback, like FirstSeen.
	LastSeen string `json:"last_seen"`
	Count    uint64 `json:"digested_event_count"`
	// Reason is the first event's message, which is transcript text.
	Reason   string `json:"calculated_value"`
	Resolved bool   `json:"is_resolved"`
	Muted    bool   `json:"is_muted"`
}

// Tag is one tag value on an issue, with how many events carried it.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// TurningPoint is one entry of an issue's history. Kind is the lower-case name of Bugsink's
// turning point kind, such as first_seen, resolved, regressed or reopened.
type TurningPoint struct {
	Kind      string `json:"kind"`
	Timestamp string `json:"timestamp"`
}

// Event is one event as GET /events/ lists it. Timestamp is when the failure happened, as the
// shipper sent it. The issue's own first_seen and last_seen are when Bugsink ingested events, so
// replayed history all lands on the day it was replayed.
type Event struct {
	Timestamp string `json:"timestamp"`
}

// Seen returns the earliest and latest event times, as Bugsink wrote them. Bugsink lists events
// newest-digested first, which is not time order for replayed history.
func Seen(events []Event) (first, last string, err error) {
	var min, max time.Time
	for i, e := range events {
		at, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			return "", "", fmt.Errorf("event has the time %q: %w", e.Timestamp, err)
		}
		if i == 0 || at.Before(min) {
			min, first = at, e.Timestamp
		}
		if i == 0 || !at.Before(max) {
			max, last = at, e.Timestamp
		}
	}
	if len(events) == 0 {
		return "", "", errors.New("no events")
	}
	return first, last, nil
}

// LastUnmuted is when the issue was last unmuted, by a person or by its mute's volume condition.
// It is nil when the history has no unmute.
func LastUnmuted(history []TurningPoint) (*time.Time, error) {
	var latest *time.Time
	for _, t := range history {
		if t.Kind != "unmuted" {
			continue
		}
		at, err := time.Parse(time.RFC3339, t.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("history has the time %q: %w", t.Timestamp, err)
		}
		if latest == nil || at.After(*latest) {
			latest = &at
		}
	}
	return latest, nil
}

// Generation is how many times an issue has come back after being resolved: Bugsink marks a
// resolved issue that fires again regressed, and a person reopening it reopened.
func Generation(history []TurningPoint) uint64 {
	var n uint64
	for _, t := range history {
		if t.Kind == "regressed" || t.Kind == "reopened" {
			n++
		}
	}
	return n
}

// Surface is a surface tag value and its event count.
type Surface struct {
	Name  string
	Count uint64
}

// Labels is what the shipper's tags say about an issue.
type Labels struct {
	Service string
	Tool    string
	Class   string
	// Surfaces is most events first, ties by name.
	Surfaces []Surface
	Sessions uint64
}

// dominant is the commonest value of key. An issue is grouped by [service, tool, class], so each
// has one value, and the commonest only breaks a tie that should not happen. Among equal counts
// the smaller value wins, and a full tie goes to the later tag.
func dominant(tags []Tag, key string) (string, error) {
	var best *Tag
	for i := range tags {
		t := &tags[i]
		if t.Key != key {
			continue
		}
		if best == nil || t.Count > best.Count || (t.Count == best.Count && t.Value <= best.Value) {
			best = t
		}
	}
	if best == nil {
		return "", fmt.Errorf("no %s tag", key)
	}
	return best.Value, nil
}

// LabelsOf reads the labels off an issue's tags.
func LabelsOf(tags []Tag) (Labels, error) {
	counts := map[string]uint64{}
	sessions := map[string]struct{}{}
	for _, t := range tags {
		switch t.Key {
		case "surface":
			counts[t.Value] += t.Count
		case "session":
			sessions[t.Value] = struct{}{}
		}
	}
	surfaces := make([]Surface, 0, len(counts))
	for name, count := range counts {
		surfaces = append(surfaces, Surface{name, count})
	}
	sort.Slice(surfaces, func(i, j int) bool {
		if surfaces[i].Count != surfaces[j].Count {
			return surfaces[i].Count > surfaces[j].Count
		}
		return surfaces[i].Name < surfaces[j].Name
	})
	var l Labels
	var err error
	if l.Service, err = dominant(tags, "service"); err != nil {
		return Labels{}, err
	}
	if l.Tool, err = dominant(tags, "tool"); err != nil {
		return Labels{}, err
	}
	if l.Class, err = dominant(tags, "class"); err != nil {
		return Labels{}, err
	}
	l.Surfaces = surfaces
	l.Sessions = uint64(len(sessions))
	return l, nil
}

// Client reads Bugsink's canonical API with a bearer token.
type Client struct {
	http    *http.Client
	base    string
	api     string
	project string
	token   string
}

// New builds a client for the project at base, a scheme://host[:port] origin.
func New(base, project, token string) *Client {
	return &Client{
		http:    &http.Client{Timeout: timeout},
		base:    base,
		api:     base + "/api/canonical/0",
		project: project,
		token:   token,
	}
}

type page[T any] struct {
	Next    *string `json:"next"`
	Results []T     `json:"results"`
}

// withoutURL drops the URL net/http puts in its errors, which may name the tailnet host.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func get[T any](ctx context.Context, c *Client, first, what string) ([]T, error) {
	var out []T
	next := &first
	for next != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *next, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, withoutURL(err))
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, withoutURL(err))
		}
		var p page[T]
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			resp.Body.Close()
			return nil, fmt.Errorf("%s: answered %s", what, resp.Status)
		}
		err = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		out = append(out, p.Results...)
		next = p.Next
	}
	return out, nil
}

// OpenIssues is the project's issues that are neither resolved nor muted.
func (c *Client) OpenIssues(ctx context.Context) ([]Issue, error) {
	return get[Issue](ctx, c, fmt.Sprintf("%s/issues/?project=%s&state=open", c.api, c.project), "Bugsink issue list")
}

// Tags is the issue's tag values.
func (c *Client) Tags(ctx context.Context, issue Issue) ([]Tag, error) {
	return get[Tag](ctx, c, fmt.Sprintf("%s/issues/%s/tags/", c.api, issue.ID), "Bugsink tags of "+issue.FriendlyID)
}

// Events is the issue's events, newest-digested first.
func (c *Client) Events(ctx context.Context, issue Issue) ([]Event, error) {
	return get[Event](ctx, c, fmt.Sprintf("%s/events/?issue=%s", c.api, issue.ID), "Bugsink events of "+issue.FriendlyID)
}

// History is the issue's turning points.
func (c *Client) History(ctx context.Context, issue Issue) ([]TurningPoint, error) {
	return get[TurningPoint](ctx, c, fmt.Sprintf("%s/issue-history/?issue=%s", c.api, issue.ID), "Bugsink history of "+issue.FriendlyID)
}

// IssueURL is where a person reads the issue. It names the tailnet host: never print it.
func (c *Client) IssueURL(issue Issue) string {
	return fmt.Sprintf("%s/issues/issue/%s/event/last/", c.base, issue.ID)
}
