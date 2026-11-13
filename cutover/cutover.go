// Package cutover resolves in Bugsink every fingerprint whose almanac issues are all closed on
// GitHub, with a comment that links them. It is the Go port of src/cutover.rs. Run is a dry run
// unless apply is set.
package cutover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/technicalpickles/brined-almanac/bugsink"
	"github.com/technicalpickles/brined-almanac/bugsinkapi"
)

// timeout is src/bugsink.rs TIMEOUT.
const timeout = 10 * time.Second

// join is what Bugsink joins a custom fingerprint's parts with to make its grouping key.
const join = " ⋄ "

// Repos is where the consumer files: classes.OwnerOf sends brineworks to the first, all else to
// the second.
var Repos = [2]string{"technicalpickles/brineworks", "technicalpickles/pickleclaw"}

// GroupingKey is the key Bugsink computes for the fingerprint bugsink.NewEvent sends.
func GroupingKey(service, tool, class string) string {
	return strings.Join([]string{service, tool, class}, join)
}

// header is the name: value lines of an almanac issue body. Only the header lines before the
// first blank line count, so the reason after it can never supply a field.
func header(body string) map[string]string {
	fields := map[string]string{}
	lines := strings.Split(body, "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(strings.TrimLeftFunc(strings.TrimSuffix(lines[i], "\r"), unicode.IsSpace), "```") {
		i++
	}
	for ; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			break
		}
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if _, seen := fields[name]; !seen {
			fields[name] = strings.TrimSpace(value)
		}
	}
	return fields
}

// KeyInBody is the grouping key named by an almanac issue body.
func KeyInBody(body string) (string, bool) {
	fields := header(body)
	service, tool, class := fields["service"], fields["tool"], fields["class"]
	if service == "" || tool == "" || class == "" {
		return "", false
	}
	return GroupingKey(service, tool, class), true
}

// State is whether an almanac issue is open on GitHub.
type State int

const (
	Open State = iota
	Closed
)

// Filed is one almanac issue on GitHub.
type Filed struct {
	URL   string
	State State
	// Key is nil when the body names no service, tool and class.
	Key *string
}

// Reason is why GitHub says an issue was closed. Any other state_reason is NoReason, which is
// not one almanac acts on.
type Reason string

const (
	NoReason   Reason = ""
	Completed  Reason = "completed"
	NotPlanned Reason = "not_planned"
)

// Standing is an issue's state, with why and when it closed.
type Standing struct {
	State  State
	Reason Reason
	At     time.Time
}

// Linked is one almanac issue that links a Bugsink issue, open or closed.
type Linked struct {
	URL    string
	Number uint64
	// Issue is the Bugsink issue id from the body's bugsink: line.
	Issue string
	// Generation is the body's regressions: count, 0 when it has none. Nil when the line is not
	// a count, so a body we cannot read never matches a Bugsink generation.
	Generation *uint64
	CreatedAt  time.Time
	Standing   Standing
}

func isUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// BugsinkLinkInBody is the Bugsink issue id in an almanac issue body's bugsink: line, which
// links .../issues/issue/<id>/event/last/.
func BugsinkLinkInBody(body string) (string, bool) {
	link, ok := header(body)["bugsink"]
	if !ok {
		return "", false
	}
	_, rest, ok := strings.Cut(link, "/issues/issue/")
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	if !isUUID(id) {
		return "", false
	}
	return strings.ToLower(id), true
}

// GenerationInBody is how many times the body says the Bugsink issue had regressed when it was
// filed. Nil when the regressions: line does not start with a count.
func GenerationInBody(body string) *uint64 {
	line, ok := header(body)["regressions"]
	if !ok {
		zero := uint64(0)
		return &zero
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "+"), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// Tracked is one Bugsink issue.
type Tracked struct {
	ID        string
	Name      string
	Keys      []string
	Resolved  bool
	FirstSeen string
}

// VerdictKind is what the plan decides for one Bugsink issue.
type VerdictKind int

const (
	Resolve VerdictKind = iota
	StillOpen
	NeverFiled
	AlreadyResolved
)

// Verdict is the decision for one Bugsink issue. Proof is the closed issues under Resolve, and
// Open the open ones under StillOpen.
type Verdict struct {
	Kind  VerdictKind
	Proof []string
	Open  []string
}

// Decision pairs a Bugsink issue with its verdict.
type Decision struct {
	Tracked Tracked
	Verdict Verdict
}

// Plan is every verdict, and the almanac issues that match no Bugsink issue.
type Plan struct {
	Verdicts  []Decision
	Unmatched []Filed
}

// MakePlan decides each Bugsink issue from the almanac issues that share one of its grouping
// keys.
func MakePlan(filed []Filed, tracked []Tracked) Plan {
	byKey := map[string][]Filed{}
	for _, f := range filed {
		if f.Key != nil {
			byKey[*f.Key] = append(byKey[*f.Key], f)
		}
	}
	known := map[string]struct{}{}
	for _, t := range tracked {
		for _, k := range t.Keys {
			known[k] = struct{}{}
		}
	}
	var plan Plan
	for _, t := range tracked {
		var mine []Filed
		for _, k := range t.Keys {
			mine = append(mine, byKey[k]...)
		}
		urls := func(state State) []string {
			var out []string
			for _, f := range mine {
				if f.State == state {
					out = append(out, f.URL)
				}
			}
			return out
		}
		var verdict Verdict
		switch {
		case t.Resolved:
			verdict = Verdict{Kind: AlreadyResolved}
		case len(mine) == 0:
			verdict = Verdict{Kind: NeverFiled}
		case len(urls(Open)) > 0:
			verdict = Verdict{Kind: StillOpen, Open: urls(Open)}
		default:
			verdict = Verdict{Kind: Resolve, Proof: urls(Closed)}
		}
		plan.Verdicts = append(plan.Verdicts, Decision{t, verdict})
	}
	for _, f := range filed {
		if f.Key == nil {
			plan.Unmatched = append(plan.Unmatched, f)
		} else if _, ok := known[*f.Key]; !ok {
			plan.Unmatched = append(plan.Unmatched, f)
		}
	}
	return plan
}

// Comment is what Bugsink records on a resolved issue: each closed almanac issue, linked.
func Comment(proof []string) string {
	var b strings.Builder
	b.WriteString("Resolved at the almanac cutover: every almanac issue for this fingerprint is closed on GitHub.\n")
	for _, u := range proof {
		b.WriteString("\n- " + u)
	}
	return b.String()
}

// Report prints the plan. It never prints a Bugsink URL, which names the tailnet host.
func Report(plan Plan) string {
	var resolve, open, never, resolved []string
	for _, d := range plan.Verdicts {
		t := d.Tracked
		label := fmt.Sprintf("%s [%s]", strings.Join(t.Keys, " | "), t.Name)
		switch d.Verdict.Kind {
		case Resolve:
			resolve = append(resolve, fmt.Sprintf("%s\n    proof: %s", label, strings.Join(d.Verdict.Proof, "\n           ")))
		case StillOpen:
			open = append(open, fmt.Sprintf("%s\n    open: %s", label, strings.Join(d.Verdict.Open, "\n          ")))
		case NeverFiled:
			never = append(never, label)
		case AlreadyResolved:
			resolved = append(resolved, label)
		}
	}
	var unmatched []string
	for _, f := range plan.Unmatched {
		state := "closed"
		if f.State == Open {
			state = "open"
		}
		key := "no service/tool/class in body"
		if f.Key != nil {
			key = *f.Key
		}
		unmatched = append(unmatched, fmt.Sprintf("%s (%s): %s", f.URL, state, key))
	}
	sections := []struct {
		title string
		lines []string
	}{
		{"resolve", resolve},
		{"left open: an almanac issue is still open", open},
		{"left open: never filed (below the filer's threshold)", never},
		{"already resolved", resolved},
		{"almanac issues that match no Bugsink issue", unmatched},
	}
	var out strings.Builder
	for _, s := range sections {
		fmt.Fprintf(&out, "\n%s (%d):\n", s.title, len(s.lines))
		for _, line := range s.lines {
			fmt.Fprintf(&out, "  %s\n", line)
		}
	}
	return out.String()
}

func newHTTP() *http.Client { return &http.Client{Timeout: timeout} }

// withoutURL drops the URL net/http puts in its errors, which may name the tailnet host.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// send sends and reads JSON. The error never carries the URL, which may name the tailnet host.
func send(c *http.Client, req *http.Request, what string) (json.RawMessage, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, withoutURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s: answered %s", what, resp.Status)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return raw, nil
}

// Github reads the almanac issues from GitHub's REST API.
type Github struct {
	http  *http.Client
	base  string
	token string
}

type githubIssue struct {
	HTMLURL     string          `json:"html_url"`
	Number      uint64          `json:"number"`
	State       string          `json:"state"`
	StateReason *string         `json:"state_reason"`
	CreatedAt   string          `json:"created_at"`
	ClosedAt    *string         `json:"closed_at"`
	Body        *string         `json:"body"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func (i githubIssue) linked() (*Linked, error) {
	if i.Body == nil {
		return nil, nil
	}
	issue, ok := BugsinkLinkInBody(*i.Body)
	if !ok {
		return nil, nil
	}
	when := func(at string) (time.Time, error) {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s has the time %q: %w", i.HTMLURL, at, err)
		}
		return t, nil
	}
	standing := Standing{State: Open}
	if i.State != "open" {
		standing.State = Closed
		if i.StateReason != nil {
			switch *i.StateReason {
			case "completed":
				standing.Reason = Completed
			case "not_planned":
				standing.Reason = NotPlanned
			}
		}
		closed := i.CreatedAt
		if i.ClosedAt != nil {
			closed = *i.ClosedAt
		}
		at, err := when(closed)
		if err != nil {
			return nil, err
		}
		standing.At = at
	}
	created, err := when(i.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &Linked{
		URL:        i.HTMLURL,
		Number:     i.Number,
		Issue:      issue,
		Generation: GenerationInBody(*i.Body),
		CreatedAt:  created,
		Standing:   standing,
	}, nil
}

// NewGithub reads GitHub's own API.
func NewGithub(token string) *Github { return NewGithubAt("https://api.github.com", token) }

// NewGithubAt is for a server other than GitHub's own, which the tests run on loopback.
func NewGithubAt(base, token string) *Github {
	return &Github{http: newHTTP(), base: base, token: token}
}

// issues is every almanac-labelled issue in repo, open or closed, pull requests left out.
func (g *Github) issues(ctx context.Context, repo string) ([]githubIssue, error) {
	what := fmt.Sprintf("GitHub %s issues", repo)
	var out []githubIssue
	for page := 1; ; page++ {
		target := fmt.Sprintf("%s/repos/%s/issues?labels=almanac&state=all&per_page=100&page=%d", g.base, repo, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, withoutURL(err))
		}
		req.Header.Set("Authorization", "Bearer "+g.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "brined-almanac")
		raw, err := send(g.http, req, what)
		if err != nil {
			return nil, err
		}
		var issues []githubIssue
		if err := json.Unmarshal(raw, &issues); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		last := len(issues) < 100
		for _, i := range issues {
			if len(i.PullRequest) == 0 || string(i.PullRequest) == "null" {
				out = append(out, i)
			}
		}
		if last {
			return out, nil
		}
	}
}

// Filed is every almanac-labelled issue in repo, open or closed.
func (g *Github) Filed(ctx context.Context, repo string) ([]Filed, error) {
	issues, err := g.issues(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]Filed, 0, len(issues))
	for _, i := range issues {
		f := Filed{URL: i.HTMLURL, State: Closed}
		if i.State == "open" {
			f.State = Open
		}
		if i.Body != nil {
			if key, ok := KeyInBody(*i.Body); ok {
				f.Key = &key
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// Linked is every almanac-labelled issue in repo that links a Bugsink issue, open or closed.
func (g *Github) Linked(ctx context.Context, repo string) ([]Linked, error) {
	issues, err := g.issues(ctx, repo)
	if err != nil {
		return nil, err
	}
	var out []Linked
	for _, i := range issues {
		l, err := i.linked()
		if err != nil {
			return nil, err
		}
		if l != nil {
			out = append(out, *l)
		}
	}
	return out, nil
}

// Bugsink is the cutover's Bugsink client: the read half is bugsinkapi's, and this adds the
// grouping read and the writes.
type Bugsink struct {
	*bugsinkapi.Client
	http    *http.Client
	api     string
	project string
	token   string
}

// NewBugsink builds a client for the project the DSN names.
func NewBugsink(dsn bugsink.Dsn, token string) *Bugsink {
	return &Bugsink{
		Client:  bugsinkapi.New(dsn.BaseURL(), dsn.Project(), token),
		http:    newHTTP(),
		api:     dsn.BaseURL() + "/api/canonical/0",
		project: dsn.Project(),
		token:   token,
	}
}

type bugsinkPage[T any] struct {
	Next    *string `json:"next"`
	Results []T     `json:"results"`
}

func all[T any](ctx context.Context, b *Bugsink, first, what string) ([]T, error) {
	var out []T
	for next := &first; next != nil; {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *next, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, withoutURL(err))
		}
		req.Header.Set("Authorization", "Bearer "+b.token)
		raw, err := send(b.http, req, what)
		if err != nil {
			return nil, err
		}
		var p bugsinkPage[T]
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		out = append(out, p.Results...)
		next = p.Next
	}
	return out, nil
}

type bugsinkIssue struct {
	ID         string `json:"id"`
	FriendlyID string `json:"friendly_id"`
	IsResolved bool   `json:"is_resolved"`
	FirstSeen  string `json:"first_seen"`
}

type grouping struct {
	GroupingKey string `json:"grouping_key"`
}

// Tracked is every issue in the project, resolved or not, with its grouping keys.
func (b *Bugsink) Tracked(ctx context.Context) ([]Tracked, error) {
	issues, err := all[bugsinkIssue](ctx, b, fmt.Sprintf("%s/issues/?project=%s&state=all", b.api, b.project), "Bugsink issue list")
	if err != nil {
		return nil, err
	}
	out := make([]Tracked, 0, len(issues))
	for _, i := range issues {
		keys, err := all[grouping](ctx, b, fmt.Sprintf("%s/issues/%s/grouping/", b.api, i.ID), "Bugsink grouping of "+i.FriendlyID)
		if err != nil {
			return nil, err
		}
		t := Tracked{ID: i.ID, Name: i.FriendlyID, Resolved: i.IsResolved, FirstSeen: i.FirstSeen, Keys: []string{}}
		for _, k := range keys {
			t.Keys = append(t.Keys, k.GroupingKey)
		}
		out = append(out, t)
	}
	return out, nil
}

func (b *Bugsink) post(ctx context.Context, target string, body any, what string) error {
	var reader io.Reader
	if body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		reader = bytes.NewReader(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, reader)
	if err != nil {
		return fmt.Errorf("%s: %w", what, withoutURL(err))
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	_, err = send(b.http, req, what)
	return err
}

// triage comments first, so a failure before the action leaves the proof on an unchanged issue
// that the next attempt picks up again.
func (b *Bugsink) triage(ctx context.Context, id, name, comment, action string, body any) error {
	if err := b.post(ctx, b.api+"/issue-comments/", map[string]any{"issue": id, "comment": comment}, "comment on "+name); err != nil {
		return err
	}
	return b.post(ctx, fmt.Sprintf("%s/issues/%s/%s/", b.api, id, action), body, action+" "+name)
}

// Resolve comments the proof on the issue, then resolves it.
func (b *Bugsink) Resolve(ctx context.Context, issue Tracked, proof []string) error {
	return b.triage(ctx, issue.ID, issue.Name, Comment(proof), "resolve", nil)
}

// ResolveIssue comments on the issue, then resolves it.
func (b *Bugsink) ResolveIssue(ctx context.Context, issue bugsinkapi.Issue, comment string) error {
	return b.triage(ctx, issue.ID, issue.FriendlyID, comment, "resolve", nil)
}

// MuteUntil mutes until wakeEvents events arrive within one day. A plain mute would never wake.
func (b *Bugsink) MuteUntil(ctx context.Context, issue bugsinkapi.Issue, comment string, wakeEvents uint64) error {
	wake := map[string]any{"period_name": "day", "nr_of_periods": 1, "gte_threshold": wakeEvents}
	return b.triage(ctx, issue.ID, issue.FriendlyID, comment, "mute-until", wake)
}

// Run reads both sides, prints the plan to out, and with apply resolves and comments.
func Run(ctx context.Context, lookup func(string) (string, bool), apply bool, out io.Writer) error {
	need := func(name string) (string, error) {
		if v, ok := lookup(name); ok && v != "" {
			return v, nil
		}
		return "", fmt.Errorf("%s is not set", name)
	}
	raw, err := need("BUGSINK_DSN")
	if err != nil {
		return err
	}
	dsn, err := bugsink.ParseDsn(raw)
	if err != nil {
		return err
	}
	apiToken, err := need("BUGSINK_API_TOKEN")
	if err != nil {
		return err
	}
	ghToken, err := need("GITHUB_TOKEN")
	if err != nil {
		return err
	}
	bs := NewBugsink(dsn, apiToken)
	gh := NewGithub(ghToken)

	var filed []Filed
	for _, repo := range Repos {
		f, err := gh.Filed(ctx, repo)
		if err != nil {
			return err
		}
		filed = append(filed, f...)
	}
	tracked, err := bs.Tracked(ctx)
	if err != nil {
		return err
	}
	plan := MakePlan(filed, tracked)

	count := func(state State) int {
		n := 0
		for _, f := range filed {
			if f.State == state {
				n++
			}
		}
		return n
	}
	seen := make([]string, 0, len(tracked))
	for _, t := range tracked {
		seen = append(seen, t.FirstSeen)
	}
	sort.Strings(seen)
	first, last := "-", "-"
	if len(seen) > 0 {
		first, last = seen[0], seen[len(seen)-1]
	}
	fmt.Fprintf(out, "cutover: %d Bugsink issue(s), first seen %s to %s; %d almanac issue(s), %d open and %d closed\n",
		len(tracked), first, last, len(filed), count(Open), count(Closed))
	fmt.Fprint(out, Report(plan))

	var due []Decision
	for _, d := range plan.Verdicts {
		if d.Verdict.Kind == Resolve {
			due = append(due, d)
		}
	}
	if !apply {
		fmt.Fprintf(out, "\ncutover: dry run, would resolve %d issue(s); pass --apply to resolve and comment\n", len(due))
		return nil
	}
	for i, d := range due {
		if err := bs.Resolve(ctx, d.Tracked, d.Verdict.Proof); err != nil {
			return fmt.Errorf("resolved %d of %d, then: %w", i, len(due), err)
		}
		fmt.Fprintf(out, "cutover: resolved %s\n", d.Tracked.Name)
	}
	fmt.Fprintf(out, "\ncutover: resolved %d issue(s)\n", len(due))
	return nil
}
