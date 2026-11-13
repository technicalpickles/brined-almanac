package securityalerts

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/technicalpickles/brined-almanac/routing"
)

// CoverageProject is where a finding about missing coverage files.
const CoverageProject = "brined-almanac"

// Payload is a security finding event as the scan writes it. Kind is overdue or coverage.
type Payload struct {
	Kind              string `json:"kind"`
	Project           string `json:"project"`
	Subject           string `json:"subject"`
	Critical          int    `json:"critical"`
	High              int    `json:"high"`
	Medium            int    `json:"medium"`
	Low               int    `json:"low"`
	Unknown           int    `json:"unknown"`
	Secrets           int    `json:"secrets"`
	DependabotOff     int    `json:"dependabot_off"`
	SecretScanningOff int    `json:"secret_scanning_off"`
	Repositories      int    `json:"repositories"`
	Lines             string `json:"lines"`
}

// Finding is a payload with its delivery ID.
type Finding struct {
	DeliveryID string
	Payload    Payload
}

// Repo is a repository and what reading it found.
type Repo struct {
	Name string
	Read RepoRead
}

// sortBy puts the highest severity first, with secrets last, then by number.
func sortBy(alerts []Alert) {
	sort.SliceStable(alerts, func(i, j int) bool {
		a, b := alerts[i], alerts[j]
		if a.rank() != b.rank() {
			return a.rank() < b.rank()
		}
		return a.Number() < b.Number()
	})
}

// projectOf is the taskwarrior project of a repository. A name that is not a valid project is a
// declaration bug, so it fails the scan.
func projectOf(repo string) (string, error) {
	name := repo[strings.LastIndex(repo, "/")+1:]
	project, err := routing.ParseProject(name)
	if err != nil {
		return "", fmt.Errorf("%s: %v", repo, err)
	}
	return project.String(), nil
}

func overdueFinding(repo string, alerts []Alert, today Day) (*Finding, error) {
	late := Overdue(alerts, today)
	if len(late) == 0 {
		return nil, nil
	}
	sortBy(late)
	keys := make([]string, len(late))
	lines := make([]string, len(late))
	payload := Payload{Kind: "overdue", Subject: repo}
	for i, a := range late {
		keys[i] = a.Key()
		lines[i] = a.Line(today)
		switch a := a.(type) {
		case Secret:
			payload.Secrets++
		case Dependency:
			switch a.Severity {
			case Critical:
				payload.Critical++
			case High:
				payload.High++
			case Medium:
				payload.Medium++
			case Low:
				payload.Low++
			default:
				payload.Unknown++
			}
		}
	}
	project, err := projectOf(repo)
	if err != nil {
		return nil, err
	}
	payload.Project = project
	payload.Lines = strings.Join(lines, "\n")
	return &Finding{DeliveryID: fmt.Sprintf("scan:security-alerts/%s@%s", repo, Digest(keys)), Payload: payload}, nil
}

func coverageFinding(reads []Repo) *Finding {
	var keys, lines []string
	repositories := map[string]bool{}
	payload := Payload{Kind: "coverage", Project: CoverageProject, Subject: "coverage"}
	for _, r := range reads {
		if !r.Read.Dependabot.On {
			payload.DependabotOff++
			keys = append(keys, "dependabot-off:"+r.Name)
			lines = append(lines, "Dependabot alerts are off: "+r.Name)
			repositories[r.Name] = true
		}
		// Secret scanning on a private repository needs a paid plan, so off there is not a gap.
		if !r.Read.Secrets.On && !r.Read.Private {
			payload.SecretScanningOff++
			keys = append(keys, "secret-scanning-off:"+r.Name)
			lines = append(lines, "Secret scanning is off: "+r.Name)
			repositories[r.Name] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	payload.Repositories = len(repositories)
	payload.Lines = strings.Join(lines, "\n")
	return &Finding{DeliveryID: "scan:security-alerts/coverage@" + Digest(keys), Payload: payload}
}

func overdueFindings(reads []Repo, today Day) ([]Finding, error) {
	var out []Finding
	for _, r := range reads {
		var alerts []Alert
		for _, side := range []Coverage{r.Read.Dependabot, r.Read.Secrets} {
			if side.On {
				alerts = append(alerts, side.Alerts...)
			}
		}
		finding, err := overdueFinding(r.Name, alerts, today)
		if err != nil {
			return nil, err
		}
		if finding != nil {
			out = append(out, *finding)
		}
	}
	return out, nil
}

// Findings is one overdue finding per repository with something overdue, then one coverage
// finding when anything is off.
func Findings(reads []Repo, today Day) ([]Finding, error) {
	out, err := overdueFindings(reads, today)
	if err != nil {
		return nil, err
	}
	if coverage := coverageFinding(reads); coverage != nil {
		out = append(out, *coverage)
	}
	return out, nil
}

// Result is one repository's read, or why it failed.
type Result struct {
	Repo string
	Read RepoRead
	Err  error
}

// Tick is one scan's reads. A repository that failed is left out of Reads and its error kept in
// Failed, so one bad repository doesn't hide every other repository's alerts.
type Tick struct {
	Reads  []Repo
	Failed []error
}

func NewTick(results []Result) Tick {
	var tick Tick
	for _, r := range results {
		if r.Err != nil {
			tick.Failed = append(tick.Failed, r.Err)
		} else {
			tick.Reads = append(tick.Reads, Repo{Name: r.Repo, Read: r.Read})
		}
	}
	return tick
}

// Findings: every repository failing points at the token or GitHub, so that still fails the
// scan. The coverage digest spans every repository, so a partial read holds it rather than file
// a coverage task for a set that is missing a repository.
func (t Tick) Findings(today Day) ([]Finding, error) {
	if len(t.Reads) == 0 && len(t.Failed) > 0 {
		all := make([]string, len(t.Failed))
		for i, e := range t.Failed {
			all[i] = e.Error()
		}
		return nil, errors.New(strings.Join(all, "; "))
	}
	if len(t.Failed) == 0 {
		return Findings(t.Reads, today)
	}
	return overdueFindings(t.Reads, today)
}
