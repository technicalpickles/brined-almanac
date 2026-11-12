package compare

import (
	"reflect"
	"testing"

	"github.com/technicalpickles/brined-almanac/wiring"
)

// The Go side is measured live, so it must not pick up anything from the process environment.
func TestGolangProvidersIgnoreProcessEnv(t *testing.T) {
	leaked := []string{
		"GITHUB_TOKEN", "GH_TOKEN", "GITHUB_ALERTS_TOKEN",
		"BUGSINK_DSN", "BUGSINK_API_TOKEN", "BUGSINK_CURSOR", "DATABASE_URL",
	}
	for _, name := range leaked {
		t.Setenv(name, "http://dummy:dummy@127.0.0.1:1/1")
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
		t.Errorf("NewGolang's providers differ from the ones an empty environment builds; one of %v reached them", leaked)
	}
}
