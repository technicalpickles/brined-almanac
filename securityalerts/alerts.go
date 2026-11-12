// Package securityalerts holds the rules for GitHub security alerts: parsing Dependabot and
// secret scanning alerts, which are overdue, and what each repository's read amounts to. It is
// the Go port of the rules half of src/workflows/security_alerts.rs; the event, the scan and
// the model are in brine and brine/securityalerts.
package securityalerts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Severity is how bad a Dependabot alert says it is.
type Severity int

const (
	Low Severity = iota + 1
	Medium
	High
	Critical
	Unknown
)

// SeverityFromGithub reads GitHub's label. Any other label, `moderate` among them, is Unknown,
// which gets the medium window.
func SeverityFromGithub(s string) Severity {
	switch s {
	case "low":
		return Low
	case "medium":
		return Medium
	case "high":
		return High
	case "critical":
		return Critical
	default:
		return Unknown
	}
}

func (s Severity) name() string {
	switch s {
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	case Critical:
		return "critical"
	default:
		return "unknown"
	}
}

// Day is a calendar date in UTC, held as midnight.
type Day = time.Time

// DayOf is the UTC date of t.
func DayOf(t time.Time) Day {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func daysBetween(from, to Day) int {
	return int(to.Sub(from) / (24 * time.Hour))
}

// Alert is an open alert of either kind.
type Alert interface {
	// Key names the alert, such as dependabot:37.
	Key() string
	// WindowDays is how many days the alert gets before it is overdue. A secret has none.
	WindowDays() int
	// Line is one line of a task body, with the alert's age as of today.
	Line(today Day) string
	// Number is the alert's number within its repository.
	Number() uint64

	created() Day
	rank() int
}

// Dependency is a Dependabot alert.
type Dependency struct {
	ID       uint64
	Severity Severity
	Package  string
	Manifest string
	Ghsa     string
	// Patched is the first patched version, empty when there is none yet.
	Patched string
	Created Day
	URL     string
}

// Secret is a secret scanning alert. It never holds the secret itself.
type Secret struct {
	ID      uint64
	Kind    string
	Created Day
	URL     string
}

func (d Dependency) Key() string    { return "dependabot:" + strconv.FormatUint(d.ID, 10) }
func (d Dependency) Number() uint64 { return d.ID }
func (d Dependency) created() Day   { return d.Created }

func (d Dependency) WindowDays() int {
	switch d.Severity {
	case Critical:
		return 2
	case High:
		return 14
	case Low:
		return 90
	default:
		return 30
	}
}

// rank puts the highest severity first.
func (d Dependency) rank() int {
	switch d.Severity {
	case Critical:
		return 0
	case High:
		return 1
	case Medium:
		return 2
	case Unknown:
		return 3
	default:
		return 4
	}
}

func (d Dependency) Line(today Day) string {
	fix := "no fix yet"
	if d.Patched != "" {
		fix = "fixed in " + d.Patched
	}
	return fmt.Sprintf("#%d %s %s (%s) %s, %s, %d days, %s",
		d.ID, d.Severity.name(), d.Package, d.Manifest, d.Ghsa, fix, daysBetween(d.Created, today), d.URL)
}

func (s Secret) Key() string     { return "secret:" + strconv.FormatUint(s.ID, 10) }
func (s Secret) Number() uint64  { return s.ID }
func (s Secret) created() Day    { return s.Created }
func (s Secret) WindowDays() int { return 0 }
func (s Secret) rank() int       { return 5 }
func (s Secret) Line(today Day) string {
	return fmt.Sprintf("#%d secret %s, %d days, %s", s.ID, s.Kind, daysBetween(s.Created, today), s.URL)
}

// Overdue is the alerts on the last day of their window or past it, so a secret is overdue the
// day it opens.
func Overdue(alerts []Alert, today Day) []Alert {
	var late []Alert
	for _, a := range alerts {
		if daysBetween(a.created(), today) >= a.WindowDays() {
			late = append(late, a)
		}
	}
	return late
}

// Digest is the first 12 hex characters of SHA-256 over the sorted keys joined with newlines.
func Digest(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:6])
}

func tree(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, errors.New("not JSON")
	}
	return root, nil
}

func descend(v any, path []string) (any, bool) {
	for _, key := range path {
		object, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = object[key]; !ok {
			return nil, false
		}
	}
	return v, true
}

func text(v any, path ...string) (string, error) {
	if s, ok := descend(v, path); ok {
		if s, ok := s.(string); ok {
			return s, nil
		}
	}
	return "", fmt.Errorf("missing %s", strings.Join(path, "."))
}

func number(v any) (uint64, error) {
	if n, ok := descend(v, []string{"number"}); ok {
		if n, ok := n.(json.Number); ok {
			if u, err := strconv.ParseUint(n.String(), 10, 64); err == nil {
				return u, nil
			}
		}
	}
	return 0, errors.New("missing number")
}

// created is the UTC date, so an alert opened late in a western evening ages from the next day.
func created(v any) (Day, error) {
	raw, err := text(v, "created_at")
	if err != nil {
		return Day{}, err
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return Day{}, fmt.Errorf("created_at %q: %v", raw, err)
	}
	return DayOf(at), nil
}

// ParseDependabot reads a Dependabot alert from GitHub's answer.
func ParseDependabot(raw json.RawMessage) (Alert, error) {
	v, err := tree(raw)
	if err != nil {
		return nil, err
	}
	patched, _ := text(v, "security_vulnerability", "first_patched_version", "identifier")
	id, err := number(v)
	if err != nil {
		return nil, err
	}
	severity, err := text(v, "security_advisory", "severity")
	if err != nil {
		return nil, err
	}
	pkg, err := text(v, "dependency", "package", "name")
	if err != nil {
		return nil, err
	}
	manifest, err := text(v, "dependency", "manifest_path")
	if err != nil {
		return nil, err
	}
	ghsa, err := text(v, "security_advisory", "ghsa_id")
	if err != nil {
		return nil, err
	}
	day, err := created(v)
	if err != nil {
		return nil, err
	}
	url, err := text(v, "html_url")
	if err != nil {
		return nil, err
	}
	return Dependency{ID: id, Severity: SeverityFromGithub(severity), Package: pkg, Manifest: manifest, Ghsa: ghsa, Patched: patched, Created: day, URL: url}, nil
}

// ParseSecret reads a secret scanning alert from GitHub's answer. It never reads the alert's
// `secret` field.
func ParseSecret(raw json.RawMessage) (Alert, error) {
	v, err := tree(raw)
	if err != nil {
		return nil, err
	}
	kind, err := text(v, "secret_type_display_name")
	if err != nil {
		if kind, err = text(v, "secret_type"); err != nil {
			return nil, errors.New("missing secret_type_display_name and secret_type")
		}
	}
	id, err := number(v)
	if err != nil {
		return nil, err
	}
	day, err := created(v)
	if err != nil {
		return nil, err
	}
	url, err := text(v, "html_url")
	if err != nil {
		return nil, err
	}
	return Secret{ID: id, Kind: kind, Created: day, URL: url}, nil
}
