package compare

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/technicalpickles/brined-almanac/wiring"
)

// The Go side is measured live, so it must not pick up anything from the process environment.
func TestGolangProvidersIgnoreProcessEnv(t *testing.T) {
	leaked := []string{
		"GITHUB_TOKEN", "GH_TOKEN", "GITHUB_ALERTS_TOKEN",
		"BUGSINK_DSN", "BUGSINK_API_TOKEN", "BUGSINK_CURSOR", "DATABASE_URL",
		"BRENT_TRANSCRIPTS", "HOST_STATE_REPORTS", "LOCAL_TIERS", "TASKCHAMPION_SYNC_URL",
		"TASKCHAMPION_CLIENT_ID", "TASKCHAMPION_ENCRYPTION_SECRET", "TASKCHAMPION_REPLICA_DIR",
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	for _, name := range leaked {
		value := "http://dummy:dummy@127.0.0.1:1/1"
		switch name {
		case "BRENT_TRANSCRIPTS", "HOST_STATE_REPORTS", "LOCAL_TIERS", "TASKCHAMPION_REPLICA_DIR":
			value = missing
		case "TASKCHAMPION_CLIENT_ID":
			value = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01"
		}
		t.Setenv(name, value)
	}
	g, err := NewGolang(nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := wiring.Providers(func(string) (string, bool) { return "", false }, live)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g.providers, want) {
		t.Errorf("NewGolang's providers differ from the ones an empty environment builds; a variable from this list reached them: %v", leaked)
	}
}
