// Package tiers reads which repositories sit in which tier. It is the Go port of src/tiers.rs
// and reads the same tiers.toml, plus an optional private file with the same schema.
package tiers

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	brined "github.com/technicalpickles/brined-almanac"
	"github.com/technicalpickles/brined-almanac/routing"
)

// Tier is how a repository is watched.
type Tier int

const (
	Dormant Tier = iota + 1
	Deployed
	Active
	Maintained
)

var tierNames = map[string]Tier{"dormant": Dormant, "deployed": Deployed, "active": Active, "maintained": Maintained}

// UnmarshalText reads a tier name and refuses any other.
func (t *Tier) UnmarshalText(text []byte) error {
	v, ok := tierNames[string(text)]
	if !ok {
		return fmt.Errorf("unknown tier %q", text)
	}
	*t = v
	return nil
}

// WatchedBySecurityAlerts are the tiers the security alerts workflow scans.
var WatchedBySecurityAlerts = []Tier{Deployed, Active, Maintained}

type declaration struct {
	Name   string  `toml:"name"`
	Tiers  []Tier  `toml:"tiers"`
	Signal *string `toml:"signal"`
	Note   *string `toml:"note"`
}

type file struct {
	Repo []declaration `toml:"repo"`
}

// Tiers maps each repository to its tiers.
type Tiers struct {
	byRepo map[string]map[Tier]bool
}

// Parse reads the public file and, when given, the local one. It fails on an unknown key or
// tier, a name that is not owner/repo or whose repo is not a taskwarrior project name, a
// repository with no tiers, and a repository declared twice.
func Parse(public string, local *string) (*Tiers, error) {
	t := &Tiers{byRepo: map[string]map[Tier]bool{}}
	if err := t.read("public tiers", public); err != nil {
		return nil, err
	}
	if local != nil {
		if err := t.read("local tiers", *local); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// Load reads the embedded public file and the local file at path. An empty path means no local
// file, which is said on stderr, as the Rust does.
func Load(localPath string) (*Tiers, error) {
	var local *string
	if localPath == "" {
		fmt.Fprintln(os.Stderr, "tiers: LOCAL_TIERS is not set, loading public tiers only")
	} else {
		b, err := os.ReadFile(localPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", localPath, err)
		}
		s := string(b)
		local = &s
	}
	return Parse(string(brined.TiersTOML), local)
}

// Watched returns the repositories in any of the tiers, sorted by name.
func (t *Tiers) Watched(anyOf []Tier) []string {
	var repos []string
	for repo, have := range t.byRepo {
		for _, want := range anyOf {
			if have[want] {
				repos = append(repos, repo)
				break
			}
		}
	}
	sort.Strings(repos)
	return repos
}

func (t *Tiers) read(source, text string) error {
	var f file
	md, err := toml.Decode(text, &f)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return fmt.Errorf("%s: unknown key %s", source, un[0])
	}
	for _, d := range f.Repo {
		owner, repo, ok := strings.Cut(d.Name, "/")
		if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
			return fmt.Errorf("%s: %q is not owner/repo", source, d.Name)
		}
		if _, err := routing.ParseProject(repo); err != nil {
			return fmt.Errorf("%s: %s: %w", source, d.Name, err)
		}
		if len(d.Tiers) == 0 {
			return fmt.Errorf("%s: %s declares no tiers", source, d.Name)
		}
		if _, dup := t.byRepo[d.Name]; dup {
			return fmt.Errorf("%s: %s is declared twice, or in both the public and local files", source, d.Name)
		}
		set := map[Tier]bool{}
		for _, tier := range d.Tiers {
			set[tier] = true
		}
		t.byRepo[d.Name] = set
	}
	return nil
}
