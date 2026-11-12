package tiers_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/technicalpickles/brined-almanac/tiers"
)

const publicA = `
[[repo]]
name = "technicalpickles/a"
tiers = ["active"]
`

func repo(name, tiersList string) string {
	return "[[repo]]\nname = \"" + name + "\"\ntiers = " + tiersList + "\n"
}

func wantErr(t *testing.T, public string, local *string, contains ...string) {
	t.Helper()
	_, err := tiers.Parse(public, local)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, c := range contains {
		if !strings.Contains(err.Error(), c) {
			t.Errorf("error %q lacks %q", err, c)
		}
	}
}

func TestParsesDeclarationsFromBothFiles(t *testing.T) {
	local := `
[[repo]]
name = "technicalpickles/b"
tiers = ["deployed"]
signal = "runs somewhere"
note = "a note"
`
	got, err := tiers.Parse(publicA, &local)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"technicalpickles/a", "technicalpickles/b"}
	if w := got.Watched([]tiers.Tier{tiers.Active, tiers.Deployed}); !reflect.DeepEqual(w, want) {
		t.Errorf("watched = %v, want %v", w, want)
	}
}

func TestARepositoryInBothFilesIsAnError(t *testing.T) {
	a := publicA
	wantErr(t, publicA, &a, "technicalpickles/a", "both")
}

func TestAnUnknownTierIsAnError(t *testing.T) {
	wantErr(t, repo("technicalpickles/a", `["sleepy"]`), nil, "sleepy")
}

func TestAnUnknownKeyIsAnError(t *testing.T) {
	wantErr(t, "[[repo]]\nname = \"technicalpickles/a\"\nteirs = [\"active\"]\n", nil, "teirs")
}

func TestAnEmptyTierListIsAnError(t *testing.T) {
	wantErr(t, repo("technicalpickles/a", `[]`), nil, "technicalpickles/a")
}

func TestANameWithoutAnOwnerIsAnError(t *testing.T) {
	wantErr(t, repo("a", `["active"]`), nil, "owner/repo")
}

func TestANameWithAnEmptyHalfIsAnError(t *testing.T) {
	for _, name := range []string{"/a", "technicalpickles/"} {
		wantErr(t, repo(name, `["active"]`), nil, name, "owner/repo")
	}
}

func TestARepositoryThatIsNotAProjectNameIsAnError(t *testing.T) {
	wantErr(t, repo("technicalpickles/Bad..Name", `["active"]`), nil, "technicalpickles/Bad..Name", "project")
}

func TestWatchedKeepsARepositoryThatIsDormantAndDeployed(t *testing.T) {
	public := repo("technicalpickles/a", `["dormant", "deployed"]`) + "\n" + repo("technicalpickles/b", `["dormant"]`)
	got, err := tiers.Parse(public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := got.Watched(tiers.WatchedBySecurityAlerts); !reflect.DeepEqual(w, []string{"technicalpickles/a"}) {
		t.Errorf("watched = %v", w)
	}
}

func TestLoadWithAMissingPathIsAnError(t *testing.T) {
	_, err := tiers.Load("/nonexistent/tiers.toml")
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/tiers.toml") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadWithALocalFileThatDoesNotParseIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tiers.toml")
	if err := os.WriteFile(path, []byte("this is [not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tiers.Load(path)
	if err == nil || !strings.Contains(err.Error(), "local") {
		t.Errorf("err = %v", err)
	}
}

func TestTheCommittedFileParsesAndWatchesTheDocumentedRepositories(t *testing.T) {
	got, err := tiers.Load("")
	if err != nil {
		t.Fatal(err)
	}
	watched := map[string]bool{}
	for _, r := range got.Watched(tiers.WatchedBySecurityAlerts) {
		watched[r] = true
	}
	for _, name := range []string{"technicalpickles/dotfiles", "technicalpickles/sb", "technicalpickles/cq", "technicalpickles/pickled-claude-plugins"} {
		if !watched[name] {
			t.Errorf("%s should be watched", name)
		}
	}
	if watched["technicalpickles/chirpfinder"] {
		t.Error("chirpfinder should not be watched")
	}
}
