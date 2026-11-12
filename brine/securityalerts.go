package brine

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/vocab"
	"github.com/ivy/almanac/wire"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/routing"
	"github.com/technicalpickles/brined-almanac/securityalerts"
	"github.com/technicalpickles/brined-almanac/tiers"
)

// AlertsClient reads GitHub's security alerts. It is always provided, because start-up checks a
// scan's providers. A nil Client means GITHUB_ALERTS_TOKEN is not set.
type AlertsClient struct{ Client *securityalerts.Client }

var (
	needsAlertsClient = providers.Require[AlertsClient]()
	needsTiers        = providers.Require[*tiers.Tiers]()
)

// AlertKind is whether a finding is overdue alerts in one repository or alerts switched off.
type AlertKind struct {
	hostile.Closed
	name string
}

var (
	AlertOverdue  = AlertKind{name: "overdue"}
	AlertCoverage = AlertKind{name: "coverage"}
)

func (k AlertKind) String() string { return k.name }

func (k AlertKind) MarshalText() ([]byte, error) { return []byte(k.name), nil }

// UnmarshalJSON refuses any kind but overdue and coverage, so the payload is malformed at /kind.
func (k *AlertKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	switch name {
	case AlertOverdue.name:
		*k = AlertOverdue
	case AlertCoverage.name:
		*k = AlertCoverage
	default:
		return errors.New("not a finding kind")
	}
	return nil
}

// SecurityFinding is one security finding. Lines carries package names and manifest paths from
// lockfiles, so it stays hostile. Repositories is how many distinct repositories a coverage
// finding names.
type SecurityFinding struct {
	Type              AlertKind          `json:"kind"`
	Project           hostile.Slug       `json:"project"`
	Subject           hostile.Slug       `json:"subject"`
	Critical          uint64             `json:"critical"`
	High              uint64             `json:"high"`
	Medium            uint64             `json:"medium"`
	Low               uint64             `json:"low"`
	Unknown           uint64             `json:"unknown"`
	Secrets           uint64             `json:"secrets"`
	DependabotOff     uint64             `json:"dependabot_off"`
	SecretScanningOff uint64             `json:"secret_scanning_off"`
	Repositories      uint64             `json:"repositories"`
	Lines             hostile.Of[string] `json:"lines"`
}

func (SecurityFinding) Kind() string        { return "security.alert.finding" }
func (SecurityFinding) AlsoKinds() []string { return nil }

// Parse refuses a kind that is neither overdue nor coverage, and a project that is not a
// taskwarrior project, as Rust's parse does.
func (e *SecurityFinding) Parse(p *wire.Payload) {
	e.Type = wire.Member[AlertKind](p, "/kind")
	e.Project = wire.Field[hostile.Slug](p, "/project")
	_ = wire.Field[taskProject](p, "/project")
	e.Subject = wire.Field[hostile.Slug](p, "/subject")
	e.Critical = wire.Field[uint64](p, "/critical")
	e.High = wire.Field[uint64](p, "/high")
	e.Medium = wire.Field[uint64](p, "/medium")
	e.Low = wire.Field[uint64](p, "/low")
	e.Unknown = wire.Field[uint64](p, "/unknown")
	e.Secrets = wire.Field[uint64](p, "/secrets")
	e.DependabotOff = wire.Field[uint64](p, "/dependabot_off")
	e.SecretScanningOff = wire.Field[uint64](p, "/secret_scanning_off")
	e.Repositories = wire.Field[uint64](p, "/repositories")
	e.Lines = p.Text("/lines")
}

// SecurityFiling is where a finding files, and how many alerts are overdue in all.
type SecurityFiling struct {
	Target hostile.Slug `json:"target"`
	Total  uint64       `json:"total"`
}

func fileSecurity(_ context.Context, _ workflow.Lookup, e SecurityFinding) (SecurityFiling, error) {
	project, err := routing.ParseProject(e.Project.String())
	if err != nil {
		return SecurityFiling{}, err
	}
	target, err := hostile.ParseSlug(project.Target())
	if err != nil {
		return SecurityFiling{}, err
	}
	return SecurityFiling{
		Target: target,
		Total:  e.Critical + e.High + e.Medium + e.Low + e.Unknown + e.Secrets,
	}, nil
}

func isCoverage(e SecurityFinding) bool      { return e.Type == AlertCoverage }
func isOneRepository(e SecurityFinding) bool { return e.Repositories == 1 }
func isPositive(n uint64) bool               { return n > 0 }

var (
	SecurityAlertFinding = vocab.Event[SecurityFinding](Vocabulary, "brine.security_finding",
		"a repository has overdue security alerts, or alerts are switched off on some repositories")
	FileSecurity = vocab.Observer(Vocabulary, "brine.file_security",
		"the project a security finding files to, and its overdue alerts in all", fileSecurity)
	IsCoverage = vocab.Predicate(Vocabulary, "brine.is_coverage",
		"the finding is about alerts switched off", isCoverage)
	IsOneRepository = vocab.Predicate(Vocabulary, "brine.is_one_repository",
		"the finding names exactly one repository", isOneRepository)
	IsPositive = vocab.Predicate(Vocabulary, "brine.is_positive",
		"the count is more than zero", isPositive)
	ScanSecurityAlerts = vocab.Scan(Vocabulary, "brine.security_alerts",
		"repositories with overdue security alerts, and repositories where alerts are switched off",
		scanSecurityAlerts, needsAlertsClient, needsTiers)
)

func scanSecurityAlerts(ctx context.Context, scanner *workflow.Scanner) ([]workflow.Found, error) {
	alerts, err := workflow.Get[AlertsClient](scanner)
	if err != nil {
		return nil, err
	}
	if alerts.Client == nil {
		logf("security-alerts: GITHUB_ALERTS_TOKEN is not set, skipping")
		return nil, nil
	}
	watched, err := workflow.Get[*tiers.Tiers](scanner)
	if err != nil {
		return nil, err
	}
	var results []securityalerts.Result
	for _, repo := range watched.Watched(tiers.WatchedBySecurityAlerts) {
		read, err := securityalerts.ReadRepo(ctx, alerts.Client, repo)
		results = append(results, securityalerts.Result{Repo: repo, Read: read, Err: err})
	}
	tick := securityalerts.NewTick(results)
	for _, err := range tick.Failed {
		logf("security-alerts: skipping %s", err)
	}
	findings, err := tick.Findings(securityalerts.DayOf(time.Now()))
	if err != nil {
		return nil, err
	}
	found := make([]workflow.Found, 0, len(findings))
	for _, f := range findings {
		payload, err := json.Marshal(f.Payload)
		if err != nil {
			return nil, err
		}
		found = append(found, workflow.Scanned[SecurityFinding](f.DeliveryID, payload))
	}
	return found, nil
}
