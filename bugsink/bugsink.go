// Package bugsink ships each failed tool call in Brent's transcripts to Bugsink as one Sentry
// event, through the store endpoint. Bugsink owns grouping and issue state from there. It is the
// Go port of src/bugsink.rs, without the engine glue (ship_from, BugsinkClient) that stays with
// the Rust scanner.
//
// This package is a taint boundary: Event.Message is the one place error text leaves
// hostile.Of, because Bugsink is where a person reads it. Everything else that comes from the
// text is a class name from classes.toml.
package bugsink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/hostile/reveal"

	"github.com/technicalpickles/brined-almanac/classes"
	"github.com/technicalpickles/brined-almanac/transcript"
)

// Overlap is how much a scan resends before the cursor, so an event in a file the puller copied
// late still ships. Resending is harmless: the event id makes Bugsink digest it once.
const Overlap = time.Hour

// Timeout is src/bugsink.rs TIMEOUT.
const Timeout = 10 * time.Second

// clientVersion is Cargo.toml's package version, which the Rust client sends.
const clientVersion = "0.1.0"

const malformed = "BUGSINK_DSN is not of the form scheme://key@host/project"

// Dsn is a project's DSN, scheme://key@host[:port]/project, split into what a send needs.
type Dsn struct {
	base    string
	project string
	store   string
	key     string
}

// ParseDsn splits a DSN. The error never carries the value, which holds the key.
func ParseDsn(raw string) (Dsn, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return Dsn{}, errors.New(malformed)
	}
	key, place, ok := strings.Cut(rest, "@")
	if !ok {
		return Dsn{}, errors.New(malformed)
	}
	slash := strings.LastIndex(place, "/")
	if slash < 0 {
		return Dsn{}, errors.New(malformed)
	}
	host, project := place[:slash], place[slash+1:]
	if (scheme != "http" && scheme != "https") || key == "" || host == "" || project == "" || !digits(project) {
		return Dsn{}, errors.New(malformed)
	}
	base := scheme + "://" + host
	return Dsn{base: base, project: project, store: base + "/api/" + project + "/store/", key: key}, nil
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// StoreURL is the endpoint events are posted to.
func (d Dsn) StoreURL() string { return d.store }

// BaseURL is scheme://host[:port], where Bugsink's API lives. It names the tailnet host: never
// print it.
func (d Dsn) BaseURL() string { return d.base }

// Project is the numeric project id the DSN ends in.
func (d Dsn) Project() string { return d.project }

// Format prints the store endpoint for every verb, so the key never reaches a log through fmt.
func (d Dsn) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "Dsn{store: %q}", d.store)
}

// EventID is 32 hex characters of SHA-256 over the transcript event id, so a resend repeats the
// id.
func EventID(transcriptID string) string {
	sum := sha256.Sum256([]byte(transcriptID))
	return hex.EncodeToString(sum[:16])
}

// Event is the Sentry event for one failed call. Bugsink groups by Fingerprint, and keeps
// Timestamp as the event's own time.
type Event struct {
	EventID     string   `json:"event_id"`
	Timestamp   string   `json:"timestamp"`
	Platform    string   `json:"platform"`
	Level       string   `json:"level"`
	Logger      string   `json:"logger"`
	Message     string   `json:"message"`
	Fingerprint []string `json:"fingerprint"`
	Tags        Tags     `json:"tags"`
}

// Tags are the searchable fields Bugsink indexes on an event.
type Tags struct {
	Service string `json:"service"`
	Tool    string `json:"tool"`
	Class   string `json:"class"`
	Surface string `json:"surface"`
	Session string `json:"session"`
}

// verdict is the closed set of class names from classes.toml. A class read from the table is
// not a declared value, so verdictOf maps it onto the declared one. A test keeps this list equal
// to the table's rows.
type verdict struct {
	hostile.Closed
	name string
}

var verdicts = []verdict{
	{name: "queued-skip"},
	{name: "mcp-timeout"},
	{name: "mcp-disconnected"},
	{name: "pg-too-many-clients"},
	{name: "approval-timeout"},
	{name: "approval-unavailable"},
	{name: "rate-limit"},
	{name: "unknown-tool-id"},
	{name: "arg-validation"},
	{name: "model-error"},
	{name: "timeout"},
	{name: "ssrf-blocked"},
	{name: "upstream-http"},
	{name: "extract-empty"},
	{name: "provider-error"},
	{name: "other"},
}

var table = classes.Default()

// verdictOf is the declared verdict whose name the table gives this text. A name the list lacks
// sorts as other, which is last.
func verdictOf(text string) verdict {
	name := table.Classify(text).Name
	for _, v := range verdicts {
		if v.name == name {
			return v
		}
	}
	return verdicts[len(verdicts)-1]
}

// NewEvent builds the event for one failed call.
func NewEvent(e transcript.ToolError) Event {
	class := hostile.Classify(e.Text.Hostile(), verdictOf).name
	service, tool := e.Service.String(), e.Tool.String()
	return Event{
		EventID:     EventID(e.ID),
		Timestamp:   e.At.String(),
		Platform:    "other",
		Level:       "error",
		Logger:      service + "." + tool,
		Message:     reveal.Value(e.Text.Reason()),
		Fingerprint: []string{service, tool, class},
		Tags: Tags{
			Service: service,
			Tool:    tool,
			Class:   class,
			Surface: e.Surface.String(),
			Session: e.Session.String(),
		},
	}
}

func instant(at transcript.At) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, at.String())
	return t, err == nil
}

// Due is the errors a scan sends: all of them without a cursor, otherwise those no older than
// Overlap before it. An error whose time does not parse is sent, since a resend costs nothing.
func Due(errs []transcript.ToolError, cursor *transcript.At) []transcript.ToolError {
	var floor time.Time
	if cursor != nil {
		if at, ok := instant(*cursor); ok {
			floor = at.Add(-Overlap)
		}
	}
	if floor.IsZero() {
		return append([]transcript.ToolError(nil), errs...)
	}
	var due []transcript.ToolError
	for _, e := range errs {
		if at, ok := instant(e.At); !ok || !at.Before(floor) {
			due = append(due, e)
		}
	}
	return due
}

// Cursor is the file holding the time of the newest event Bugsink has accepted. It only limits
// traffic: a lost or garbled cursor resends history, which Bugsink digests once.
type Cursor struct{ Path string }

func pathError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// Load is the saved time, or nil when the file is missing or holds no time.
func (c Cursor) Load() (*transcript.At, error) {
	text, err := os.ReadFile(c.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", c.Path, pathError(err))
	}
	at, ok := transcript.ParseAt(strings.TrimSpace(string(text)))
	if !ok {
		return nil, nil
	}
	return &at, nil
}

// Save writes beside the file and renames over it, so a crash leaves the old cursor or the new.
func (c Cursor) Save(at transcript.At) error {
	next := strings.TrimSuffix(c.Path, filepath.Ext(c.Path)) + ".next"
	if err := os.WriteFile(next, []byte(at.String()), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %v", c.Path, pathError(err))
	}
	if err := os.Rename(next, c.Path); err != nil {
		return fmt.Errorf("cannot write %s: %v", c.Path, pathError(err))
	}
	return nil
}

// Shipped counts what a scan found due and what Bugsink accepted.
type Shipped struct {
	Due  int
	Sent int
}

// Bugsink sends events to one project.
type Bugsink struct {
	http   *http.Client
	dsn    Dsn
	cursor Cursor
	// live false is a dry run, which counts what is due and sends nothing.
	live bool
}

// New builds a sender with a timeout.
func New(dsn Dsn, cursor Cursor, live bool) *Bugsink {
	return &Bugsink{http: &http.Client{Timeout: Timeout}, dsn: dsn, cursor: cursor, live: live}
}

func (b *Bugsink) send(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.dsn.store, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Bugsink unreachable: %v", urlCause(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s, sentry_client=brined-almanac/%s", b.dsn.key, clientVersion))
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("Bugsink unreachable: %v", urlCause(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Bugsink answered %s", resp.Status)
	}
	return nil
}

// urlCause drops the URL a *url.Error repeats, since it names the host.
func urlCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Ship sends what is due, oldest first, and moves the cursor to the newest event accepted. A
// failure stops the send and keeps the cursor at the last success, so the next scan resumes.
func (b *Bugsink) Ship(ctx context.Context, errs []transcript.ToolError) (Shipped, error) {
	cursor, err := b.cursor.Load()
	if err != nil {
		return Shipped{}, err
	}
	due := Due(errs, cursor)
	shipped := Shipped{Due: len(due)}
	if !b.live {
		return shipped, nil
	}
	newest := cursor
	var failure error
	for _, e := range due {
		if err := b.send(ctx, NewEvent(e)); err != nil {
			failure = err
			break
		}
		shipped.Sent++
		if newest == nil || newest.Before(e.At) {
			at := e.At
			newest = &at
		}
	}
	if shipped.Sent > 0 && newest != nil {
		if err := b.cursor.Save(*newest); err != nil {
			return shipped, err
		}
	}
	if failure != nil {
		return shipped, fmt.Errorf("sent %d of %d event(s), then: %v", shipped.Sent, shipped.Due, failure)
	}
	return shipped, nil
}
