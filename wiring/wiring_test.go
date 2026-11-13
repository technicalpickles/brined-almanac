package wiring_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ivy/almanac/providers"
	"github.com/ivy/almanac/runtime"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/taskwarrior"
	"github.com/technicalpickles/brined-almanac/tiers"
	"github.com/technicalpickles/brined-almanac/wiring"
)

const clientID = "6f1d2c3b-4a5e-4f60-8172-839a4b5c6d7e"

func envOf(pairs map[string]string) wiring.Env {
	return func(name string) (string, bool) {
		value, ok := pairs[name]
		return value, ok
	}
}

func choose(t *testing.T, pairs map[string]string) wiring.Choice {
	t.Helper()
	choice, err := wiring.Choose(envOf(pairs))
	if err != nil {
		t.Fatalf("choose: %v", err)
	}
	return choice
}

func chooseErr(t *testing.T, pairs map[string]string) string {
	t.Helper()
	_, err := wiring.Choose(envOf(pairs))
	if err == nil {
		t.Fatal("want an error")
	}
	return err.Error()
}

func taskchampion(extra map[string]string, drop ...string) map[string]string {
	env := map[string]string{
		"TASKCHAMPION_SYNC_URL":          "https://sync.example.test",
		"TASKCHAMPION_CLIENT_ID":         clientID,
		"TASKCHAMPION_ENCRYPTION_SECRET": "s3cret-as-stored",
		"TASKCHAMPION_REPLICA_DIR":       "/some/dir",
	}
	for _, key := range drop {
		delete(env, key)
	}
	maps.Copy(env, extra)
	return env
}

func route(t *testing.T, choice wiring.Choice) wiring.Route {
	t.Helper()
	r, ok := choice.(wiring.Route)
	if !ok {
		t.Fatalf("got %v, want a route", choice)
	}
	return r
}

func TestNoDatabaseMeansStoreRequiredForBothSides(t *testing.T) {
	choice := choose(t, taskchampion(map[string]string{"GITHUB_TOKEN": "a-token"}))
	if _, ok := choice.(wiring.StoreRequired); !ok {
		t.Fatalf("got %v, want StoreRequired", choice)
	}
}

func TestTasksAloneWithoutADatabaseMeanStoreRequired(t *testing.T) {
	if choice := choose(t, taskchampion(nil)); choice != (wiring.StoreRequired{}) {
		t.Fatalf("got %v, want StoreRequired", choice)
	}
}

func TestNoSyncURLMeansLogOnTheTasksSide(t *testing.T) {
	r := route(t, choose(t, map[string]string{"DATABASE_URL": "postgres://db", "GITHUB_TOKEN": "a-token"}))
	if r.GithubToken != "a-token" || r.Tasks != nil {
		t.Fatalf("got %v, want GitHub issues and logged tasks", r)
	}
}

func TestNoTokenMeansLogOnTheIssuesSide(t *testing.T) {
	r := route(t, choose(t, taskchampion(map[string]string{"DATABASE_URL": "postgres://db"})))
	if r.GithubToken != "" || r.Tasks == nil {
		t.Fatalf("got %v, want logged issues and taskwarrior tasks", r)
	}
}

func TestAFullConfigurationBuildsBoth(t *testing.T) {
	r := route(t, choose(t, taskchampion(map[string]string{"DATABASE_URL": "postgres://db", "GITHUB_TOKEN": "a-token"})))
	if r.GithubToken != "a-token" {
		t.Errorf("issues side: %v", r)
	}
	want := taskwarrior.Config{
		DataDir: "/some/dir",
		Server: taskwarrior.Server{Remote: &taskwarrior.Remote{
			URL: "https://sync.example.test", ClientID: clientID, EncryptionSecret: "s3cret-as-stored",
		}},
	}
	if r.Tasks == nil || r.Tasks.DataDir != want.DataDir || *r.Tasks.Server.Remote != *want.Server.Remote {
		t.Fatalf("tasks side: %v", r)
	}
}

func TestAURLWithoutAClientIDIsAnErrorNamingIt(t *testing.T) {
	err := chooseErr(t, taskchampion(nil, "TASKCHAMPION_CLIENT_ID"))
	if !strings.Contains(err, "TASKCHAMPION_CLIENT_ID") || strings.Contains(err, "s3cret") {
		t.Fatal(err)
	}
}

func TestAURLWithoutASecretIsAnErrorNamingIt(t *testing.T) {
	if err := chooseErr(t, taskchampion(nil, "TASKCHAMPION_ENCRYPTION_SECRET")); !strings.Contains(err, "TASKCHAMPION_ENCRYPTION_SECRET") {
		t.Fatal(err)
	}
}

func TestAClientIDThatIsNotAUUIDIsAnErrorNamingIt(t *testing.T) {
	err := chooseErr(t, taskchampion(map[string]string{"TASKCHAMPION_CLIENT_ID": "not-a-uuid"}))
	if !strings.Contains(err, "TASKCHAMPION_CLIENT_ID") || strings.Contains(err, "not-a-uuid") || strings.Contains(err, "s3cret") {
		t.Fatal(err)
	}
}

func TestAClientIDInAnyFormRustAcceptsIsHyphenated(t *testing.T) {
	for _, form := range []string{
		"6F1D2C3B-4A5E-4F60-8172-839A4B5C6D7E",
		"6f1d2c3b4a5e4f608172839a4b5c6d7e",
		"{6f1d2c3b-4a5e-4f60-8172-839a4b5c6d7e}",
		"urn:uuid:6f1d2c3b-4a5e-4f60-8172-839a4b5c6d7e",
	} {
		r := route(t, choose(t, taskchampion(map[string]string{"DATABASE_URL": "postgres://db", "TASKCHAMPION_CLIENT_ID": form})))
		if got := r.Tasks.Server.Remote.ClientID; got != clientID {
			t.Errorf("%s: client id %s, want %s", form, got, clientID)
		}
	}
	for _, bad := range []string{"6f1d2c3b-4a5e-4f60-8172-839a4b5c6d7", "6f1d2c3b_4a5e_4f60_8172_839a4b5c6d7e", "{6f1d2c3b4a5e4f608172839a4b5c6d7e}"} {
		if err := chooseErr(t, taskchampion(map[string]string{"TASKCHAMPION_CLIENT_ID": bad})); !strings.Contains(err, "not a UUID") {
			t.Errorf("%s: %s", bad, err)
		}
	}
}

func TestTheReplicaDirDefaults(t *testing.T) {
	r := route(t, choose(t, taskchampion(map[string]string{"DATABASE_URL": "postgres://db"}, "TASKCHAMPION_REPLICA_DIR")))
	if r.Tasks.DataDir != "/var/lib/brined-almanac/taskchampion" {
		t.Fatal(r.Tasks.DataDir)
	}
}

func TestAClientIDWithoutAURLIsAnErrorNamingBoth(t *testing.T) {
	if err := chooseErr(t, taskchampion(nil, "TASKCHAMPION_SYNC_URL")); err != "TASKCHAMPION_CLIENT_ID is set but TASKCHAMPION_SYNC_URL is not" {
		t.Fatal(err)
	}
}

func TestATokenWithoutADatabaseOrTasksMeansStoreRequired(t *testing.T) {
	if choice := choose(t, map[string]string{"GITHUB_TOKEN": "a-token"}); choice != (wiring.StoreRequired{}) {
		t.Fatalf("got %v", choice)
	}
}

func TestNothingSetPrintsBothSides(t *testing.T) {
	if r := route(t, choose(t, nil)); r.GithubToken != "" || r.Tasks != nil {
		t.Fatalf("got %v", r)
	}
}

func TestAnEmptyGithubTokenMeansLogOnTheIssuesSide(t *testing.T) {
	if r := route(t, choose(t, map[string]string{"GITHUB_TOKEN": ""})); r.GithubToken != "" {
		t.Fatalf("got %v", r)
	}
}

func TestAnEmptySyncURLIsNotSet(t *testing.T) {
	if err := chooseErr(t, taskchampion(map[string]string{"TASKCHAMPION_SYNC_URL": ""})); !strings.Contains(err, "TASKCHAMPION_SYNC_URL") {
		t.Fatal(err)
	}
}

func TestAnEmptySecretIsNotSet(t *testing.T) {
	if err := chooseErr(t, taskchampion(map[string]string{"TASKCHAMPION_ENCRYPTION_SECRET": ""})); !strings.Contains(err, "TASKCHAMPION_ENCRYPTION_SECRET") {
		t.Fatal(err)
	}
}

func TestARouteNeverPrintsItsSecrets(t *testing.T) {
	r := route(t, choose(t, taskchampion(map[string]string{"DATABASE_URL": "postgres://db", "GITHUB_TOKEN": "a-token"})))
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		shown := fmt.Sprintf(verb, r)
		if strings.Contains(shown, "a-token") || strings.Contains(shown, "s3cret") || !strings.Contains(shown, "/some/dir") {
			t.Errorf("%s shows %s", verb, shown)
		}
	}
}

func TestSyncCheckConfigNeedsASyncURL(t *testing.T) {
	_, err := wiring.SyncCheckConfig(envOf(nil), "x")
	if err == nil || err.Error() != "TASKCHAMPION_SYNC_URL is not set" {
		t.Fatal(err)
	}
}

func TestSyncCheckConfigUsesTheDirectoryItIsGiven(t *testing.T) {
	env := envOf(map[string]string{
		"TASKCHAMPION_SYNC_URL":          "https://sync.example",
		"TASKCHAMPION_CLIENT_ID":         "8b46d55e-0000-4000-8000-000000000000",
		"TASKCHAMPION_ENCRYPTION_SECRET": "s",
		"DATABASE_URL":                   "postgres://x",
	})
	config, err := wiring.SyncCheckConfig(env, "/tmp/check")
	if err != nil || config.DataDir != "/tmp/check" {
		t.Fatal(config, err)
	}
}

func tiersWith(local *string) (*tiers.Tiers, error) {
	pairs := map[string]string{}
	if local != nil {
		pairs["LOCAL_TIERS"] = *local
	}
	return wiring.TiersFrom(envOf(pairs))
}

func watchesDotfiles(t *testing.T, loaded *tiers.Tiers, err error) bool {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return slices.Contains(loaded.Watched([]tiers.Tier{tiers.Active, tiers.Deployed, tiers.Maintained, tiers.Dormant}), "technicalpickles/dotfiles")
}

func TestTiersFromUnsetLoadsPublicOnly(t *testing.T) {
	if loaded, err := tiersWith(nil); !watchesDotfiles(t, loaded, err) {
		t.Fatal("public tiers not loaded")
	}
}

func TestTiersFromAnEmptyValueLoadsPublicOnly(t *testing.T) {
	empty := ""
	if loaded, err := tiersWith(&empty); !watchesDotfiles(t, loaded, err) {
		t.Fatal("public tiers not loaded")
	}
}

func TestTiersFromAMissingPathIsAnError(t *testing.T) {
	missing := "/no/such/tiers.toml"
	_, err := tiersWith(&missing)
	if err == nil || !strings.Contains(err.Error(), "LOCAL_TIERS") || !strings.Contains(err.Error(), missing) {
		t.Fatal(err)
	}
}

func TestAlertsClientAbsentWithoutAToken(t *testing.T) {
	if wiring.AlertsClientFrom(envOf(nil)).Client != nil {
		t.Error("unset token made a client")
	}
	if wiring.AlertsClientFrom(envOf(map[string]string{"GITHUB_ALERTS_TOKEN": ""})).Client != nil {
		t.Error("empty token made a client")
	}
}

func TestAlertsClientPresentWithAToken(t *testing.T) {
	if wiring.AlertsClientFrom(envOf(map[string]string{"GITHUB_ALERTS_TOKEN": "t"})).Client == nil {
		t.Error("no client")
	}
}

const dsnKey = "0123abcd"

func TestBugsinkClientFromTheDsn(t *testing.T) {
	if c, err := wiring.BugsinkClientFrom(envOf(nil), true); err != nil || c.Sender != nil {
		t.Errorf("unset DSN: %v, %v", c.Sender, err)
	}
	if c, err := wiring.BugsinkClientFrom(envOf(map[string]string{"BUGSINK_DSN": ""}), true); err != nil || c.Sender != nil {
		t.Errorf("empty DSN: %v, %v", c.Sender, err)
	}
	_, err := wiring.BugsinkClientFrom(envOf(map[string]string{"BUGSINK_DSN": dsnKey + "@nowhere"}), true)
	if err == nil || strings.Contains(err.Error(), dsnKey) {
		t.Errorf("bad DSN: %v", err)
	}
	if c, err := wiring.BugsinkClientFrom(envOf(map[string]string{"BUGSINK_DSN": "http://" + dsnKey + "@127.0.0.1:9/1"}), true); err != nil || c.Sender == nil {
		t.Errorf("good DSN: %v, %v", c.Sender, err)
	}
}

func TestTheReaderReadsGithubOnlyWithATokenAndNoneWithoutBugsink(t *testing.T) {
	reading := func(github *string, bugsink bool) *brine.Reading {
		t.Helper()
		pairs := map[string]string{}
		if bugsink {
			pairs["BUGSINK_DSN"] = "http://" + dsnKey + "@bugsink.example/7"
			pairs["BUGSINK_API_TOKEN"] = "t"
		}
		if github != nil {
			pairs["GITHUB_TOKEN"] = *github
		}
		reader, err := wiring.BugsinkReaderFrom(envOf(pairs), true)
		if err != nil {
			t.Fatal(err)
		}
		return reader.Reading
	}
	g, empty := "g", ""
	if r := reading(&g, true); r == nil || r.Github == nil {
		t.Error("a token should read GitHub")
	}
	if r := reading(&empty, true); r == nil || r.Github != nil {
		t.Error("an empty token should not read GitHub")
	}
	if r := reading(nil, true); r == nil || r.Github != nil {
		t.Error("no token should not read GitHub")
	}
	if r := reading(&g, false); r != nil {
		t.Error("no Bugsink should read nothing")
	}
}

// Rust's live() is ALMANAC_MODE=live and no --dry-run. Go derives it from Ivy's switches, which
// the shipper and the reader then carry.
func TestOnlyAlmanacModeLiveWithoutDryRunIsLive(t *testing.T) {
	for _, c := range []struct {
		mode   *string
		dryRun bool
		live   bool
	}{
		{mode: ptr("live"), live: true},
		{mode: ptr("live"), dryRun: true},
		{},
		{mode: ptr("dry")},
	} {
		pairs := map[string]string{
			"BUGSINK_DSN":       "http://" + dsnKey + "@bugsink.example/7",
			"BUGSINK_API_TOKEN": "t",
			"BRENT_TRANSCRIPTS": t.TempDir(),
		}
		if c.mode != nil {
			pairs["ALMANAC_MODE"] = *c.mode
		}
		env := envOf(pairs)
		set, err := wiring.Providers(env, runtime.SwitchesFrom(c.dryRun, env))
		if err != nil {
			t.Fatal(err)
		}
		client, _ := providers.Lookup[brine.BugsinkClient](set)
		reader, _ := providers.Lookup[brine.BugsinkReader](set)
		if client.Live != c.live || reader.Reading.Live != c.live {
			t.Errorf("mode %v, dry run %v: shipper live %v, reader live %v, want %v", c.mode, c.dryRun, client.Live, reader.Reading.Live, c.live)
		}
	}
}

func TestReadOnlyIsNeverLive(t *testing.T) {
	env := envOf(map[string]string{"ALMANAC_MODE": "live", "ALMANAC_READ_ONLY": "1", "BUGSINK_DSN": "http://" + dsnKey + "@bugsink.example/7"})
	set, err := wiring.Providers(env, runtime.SwitchesFrom(false, env))
	if err != nil {
		t.Fatal(err)
	}
	if client, _ := providers.Lookup[brine.BugsinkClient](set); client.Live {
		t.Error("a read-only run ships")
	}
}

func TestTranscriptsDefaultOnlyWhenUnset(t *testing.T) {
	for _, c := range []struct {
		pairs map[string]string
		dir   string
	}{
		{nil, "transcripts"},
		{map[string]string{"BRENT_TRANSCRIPTS": "/srv/t"}, "/srv/t"},
		{map[string]string{"BRENT_TRANSCRIPTS": ""}, ""},
	} {
		env := envOf(c.pairs)
		set, err := wiring.Providers(env, runtime.SwitchesFrom(false, env))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := providers.Lookup[brine.Transcripts](set); got.Dir != c.dir {
			t.Errorf("%v: dir %q, want %q", c.pairs, got.Dir, c.dir)
		}
	}
}

func TestBadConfigurationFailsStartupWithoutTheValue(t *testing.T) {
	for _, pairs := range []map[string]string{
		{"TASKCHAMPION_CLIENT_ID": "secret-client"},
		{"LOCAL_TIERS": "/no/such/tiers.toml"},
		{"BUGSINK_DSN": "secret-key@nowhere"},
	} {
		env := envOf(pairs)
		_, err := wiring.Providers(env, runtime.SwitchesFrom(false, env))
		if err == nil {
			t.Errorf("%v: started", pairs)
			continue
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("%v: error shows a value: %v", pairs, err)
		}
	}
}

func ptr(s string) *string { return &s }
