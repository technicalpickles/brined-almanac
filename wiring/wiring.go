// Package wiring builds every provider brined-almanac's workflows and scans reach, once, from
// the environment. It is the Go port of src/providers.rs. An error names the missing or
// malformed variable and never carries a value, since the values include secrets.
package wiring

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/github"
	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/bugsink"
	"github.com/technicalpickles/brined-almanac/cutover"
	"github.com/technicalpickles/brined-almanac/hoststate"
	"github.com/technicalpickles/brined-almanac/routing"
	"github.com/technicalpickles/brined-almanac/securityalerts"
	"github.com/technicalpickles/brined-almanac/taskwarrior"
	"github.com/technicalpickles/brined-almanac/tiers"
)

const (
	api                = "https://api.github.com"
	defaultReplicaDir  = "/var/lib/brined-almanac/taskchampion"
	defaultCursor      = "/var/lib/brined-almanac/bugsink/cursor"
	defaultTranscripts = "transcripts"
)

// Env reads one variable, as os.LookupEnv does.
type Env func(name string) (string, bool)

// set is the variable's value. `KEY=` in an env file sets the variable to nothing, which is not
// configuration, so an empty value is unset.
func (env Env) set(name string) (string, bool) {
	value, ok := env(name)
	return value, ok && value != ""
}

// Choice is which tracker the environment asks for: StoreRequired or a Route.
type Choice interface{ choice() }

func (StoreRequired) choice() {}
func (Route) choice()         {}

// Route sends task: targets to Tasks and everything else to GitHub.
type Route struct {
	// GithubToken empty prints the issues side instead of filing on GitHub.
	GithubToken string
	// Tasks nil prints the tasks side instead of filing in a synced taskwarrior replica.
	Tasks *taskwarrior.Config
}

// Format names the sides and never the token or the encryption secret.
func (r Route) Format(f fmt.State, _ rune) {
	issues, tasks := "Log", "Log"
	if r.GithubToken != "" {
		issues = "Github"
	}
	if r.Tasks != nil {
		tasks = fmt.Sprintf("Taskwarrior(%q)", r.Tasks.DataDir)
	}
	_, _ = fmt.Fprintf(f, "Route{issues: %s, tasks: %s}", issues, tasks)
}

// Choose reads the tracker settings.
func Choose(env Env) (Choice, error) {
	token, _ := env.set("GITHUB_TOKEN")
	var tasks *taskwarrior.Config
	if url, ok := env.set("TASKCHAMPION_SYNC_URL"); ok {
		required := func(key string) (string, error) {
			if value, ok := env.set(key); ok {
				return value, nil
			}
			return "", fmt.Errorf("TASKCHAMPION_SYNC_URL is set but %s is not", key)
		}
		raw, err := required("TASKCHAMPION_CLIENT_ID")
		if err != nil {
			return nil, err
		}
		clientID, ok := parseUUID(raw)
		if !ok {
			return nil, errors.New("TASKCHAMPION_CLIENT_ID is not a UUID")
		}
		secret, err := required("TASKCHAMPION_ENCRYPTION_SECRET")
		if err != nil {
			return nil, err
		}
		dir, ok := env.set("TASKCHAMPION_REPLICA_DIR")
		if !ok {
			dir = defaultReplicaDir
		}
		tasks = &taskwarrior.Config{
			DataDir: dir,
			Server:  taskwarrior.Server{Remote: &taskwarrior.Remote{URL: url, ClientID: clientID, EncryptionSecret: secret}},
		}
	} else {
		// Without this, a lost URL line would fall back to printing, and a live run would mark
		// every task finding applied without filing it.
		for _, key := range []string{"TASKCHAMPION_CLIENT_ID", "TASKCHAMPION_ENCRYPTION_SECRET"} {
			if _, ok := env.set(key); ok {
				return nil, fmt.Errorf("%s is set but TASKCHAMPION_SYNC_URL is not", key)
			}
		}
	}
	printsOnly := token == "" && tasks == nil
	if _, ok := env.set("DATABASE_URL"); !ok && !printsOnly {
		return StoreRequired{}, nil
	}
	return Route{GithubToken: token, Tasks: tasks}, nil
}

// parseUUID accepts the forms Rust's uuid crate parses (simple, hyphenated, braced and urn) and
// returns the hyphenated lowercase form.
func parseUUID(s string) (string, bool) {
	s = strings.ToLower(s)
	switch {
	case len(s) == 45 && strings.HasPrefix(s, "urn:uuid:"):
		s = s[9:]
	case len(s) == 38 && s[0] == '{' && s[37] == '}':
		s = s[1:37]
	}
	if len(s) == 36 {
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return "", false
		}
		s = strings.ReplaceAll(s, "-", "")
	}
	if len(s) != 32 || strings.Trim(s, "0123456789abcdef") != "" {
		return "", false
	}
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], true
}

// SyncCheckConfig is the taskwarrior settings `sync-check` uses, pointed at dataDir instead of
// the daemon's own replica, so the check starts empty and never touches the daemon's replica.
func SyncCheckConfig(env Env, dataDir string) (taskwarrior.Config, error) {
	choice, err := Choose(env)
	if err != nil {
		return taskwarrior.Config{}, err
	}
	route, ok := choice.(Route)
	switch {
	case !ok:
		return taskwarrior.Config{}, errors.New("DATABASE_URL is not set")
	case route.Tasks == nil:
		return taskwarrior.Config{}, errors.New("TASKCHAMPION_SYNC_URL is not set")
	}
	config := *route.Tasks
	config.DataDir = dataDir
	return config, nil
}

// TrackerFrom builds the tracker a Choice names. Nothing here opens a connection.
func TrackerFrom(choice Choice) effect.Tracker {
	route, ok := choice.(Route)
	if !ok {
		return StoreRequired{}
	}
	var issues, tasks effect.Tracker = LogTracker{}, LogTracker{}
	if route.GithubToken != "" {
		client := github.New(api, github.Token(route.GithubToken))
		issues = NewPrivateOnly(github.NewIssues(client, ""), githubVisibility{client})
	}
	if route.Tasks != nil {
		tasks = taskwarrior.New(*route.Tasks)
	}
	return routing.Router{Issues: issues, Tasks: tasks}
}

// TiersFrom loads the committed tiers plus the private file LOCAL_TIERS names.
func TiersFrom(env Env) (*tiers.Tiers, error) {
	local, _ := env.set("LOCAL_TIERS")
	loaded, err := tiers.Load(local)
	if err != nil {
		return nil, fmt.Errorf("LOCAL_TIERS: %w", err)
	}
	return loaded, nil
}

// AlertsClientFrom is the read-only client for GITHUB_ALERTS_TOKEN, or none without one.
func AlertsClientFrom(env Env) brine.AlertsClient {
	token, ok := env.set("GITHUB_ALERTS_TOKEN")
	if !ok {
		return brine.AlertsClient{}
	}
	return brine.AlertsClient{Client: securityalerts.NewClient(api, github.Token(token), github.ReadOnly())}
}

// BugsinkClientFrom is the shipper for BUGSINK_DSN, or none when it is unset. The error never
// carries the DSN, which holds a key.
func BugsinkClientFrom(env Env, live bool) (brine.BugsinkClient, error) {
	raw, ok := env.set("BUGSINK_DSN")
	if !ok {
		return brine.BugsinkClient{Live: live}, nil
	}
	dsn, err := bugsink.ParseDsn(raw)
	if err != nil {
		return brine.BugsinkClient{}, err
	}
	cursor, ok := env.set("BUGSINK_CURSOR")
	if !ok {
		cursor = defaultCursor
	}
	return brine.BugsinkClient{Sender: bugsink.New(dsn, bugsink.Cursor{Path: cursor}, live), Live: live}, nil
}

// BugsinkReaderFrom is the reader for BUGSINK_API_TOKEN and BUGSINK_DSN, or none when either is
// unset. GITHUB_TOKEN adds the GitHub side, which closed almanac issues are read from.
func BugsinkReaderFrom(env Env, live bool) (brine.BugsinkReader, error) {
	raw, hasDsn := env.set("BUGSINK_DSN")
	token, hasToken := env.set("BUGSINK_API_TOKEN")
	if !hasDsn || !hasToken {
		return brine.BugsinkReader{}, nil
	}
	dsn, err := bugsink.ParseDsn(raw)
	if err != nil {
		return brine.BugsinkReader{}, err
	}
	reading := &brine.Reading{Bugsink: cutover.NewBugsink(dsn, token), Live: live}
	if gh, ok := env.set("GITHUB_TOKEN"); ok {
		reading.Github = cutover.NewGithub(gh)
	}
	return brine.BugsinkReader{Reading: reading}, nil
}

// Providers is every provider, from env. A live run is almanac's own rule, ALMANAC_MODE=live
// and no --dry-run (and, in Go, no ALMANAC_READ_ONLY), which the shipper and the Bugsink reader
// need because they write outside almanac's effects.
func Providers(env Env, switches runtime.Switches) (*providers.Set, error) {
	transcripts, ok := env("BRENT_TRANSCRIPTS")
	if !ok {
		transcripts = defaultTranscripts
	}
	choice, err := Choose(env)
	if err != nil {
		return nil, err
	}
	loaded, err := TiersFrom(env)
	if err != nil {
		return nil, err
	}
	hosts, err := hoststate.LoadHosts()
	if err != nil {
		return nil, err
	}
	live := switches.Mode == effect.Live
	client, err := BugsinkClientFrom(env, live)
	if err != nil {
		return nil, err
	}
	reader, err := BugsinkReaderFrom(env, live)
	if err != nil {
		return nil, err
	}
	set := providers.New()
	providers.Provide(set, loaded)
	providers.Provide(set, hosts)
	providers.Provide(set, brine.HostReportsFrom(func(name string) string { value, _ := env(name); return value }))
	providers.Provide(set, AlertsClientFrom(env))
	providers.Provide(set, client)
	providers.Provide(set, reader)
	providers.Provide(set, brine.Transcripts{Dir: transcripts})
	providers.Provide(set, TrackerFrom(choice))
	return set, nil
}
