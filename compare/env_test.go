package compare_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/technicalpickles/brined-almanac/compare"
)

// envDumper is the name the test binary is run under to stand in for the Rust binary. Run so, it
// writes its environment to "env" and prints "recording", both beside itself, and exits.
const envDumper = "env-dumper"

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == envDumper {
		beside := filepath.Dir(os.Args[0])
		env := os.Environ()
		sort.Strings(env)
		recording, err := os.ReadFile(filepath.Join(beside, "recording"))
		if err == nil {
			err = os.WriteFile(filepath.Join(beside, "env"), []byte(strings.Join(env, "\n")), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Stdout.Write(recording)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// credentials are dummy values for every variable the Go binary reads. None is a real secret.
// The URLs point at a listener the test owns.
func credentials(addr string) map[string]string {
	return map[string]string{
		"GITHUB_TOKEN":                   "dummy-github-token",
		"GH_TOKEN":                       "dummy-gh-token",
		"GITHUB_ALERTS_TOKEN":            "dummy-alerts-token",
		"BUGSINK_DSN":                    "http://dummykey@" + addr + "/1",
		"BUGSINK_API_TOKEN":              "dummy-bugsink-token",
		"BUGSINK_CURSOR":                 "dummy-cursor",
		"DATABASE_URL":                   "postgres://dummy:dummy@" + addr + "/dummy",
		"ALMANAC_READ_ONLY":              "1",
		"ALMANAC_MODE":                   "dry-run",
		"TASKCHAMPION_SYNC_URL":          "http://" + addr,
		"TASKCHAMPION_CLIENT_ID":         "00000000-0000-4000-8000-000000000000",
		"TASKCHAMPION_ENCRYPTION_SECRET": "dummy-secret",
		"TASKCHAMPION_REPLICA_DIR":       "/nonexistent/replica",
		"HOST_STATE_REPORTS":             "/nonexistent/reports",
		"BRENT_TRANSCRIPTS":              "/nonexistent/transcripts",
		"LOCAL_TIERS":                    "/nonexistent/tiers",
	}
}

func TestTheRustChildSeesOnlyPathAndLiveMode(t *testing.T) {
	for name, value := range credentials("127.0.0.1:1") {
		t.Setenv(name, value)
	}
	dir, c := oneCase(t, "host.state.finding", stale, nil)
	if err := os.WriteFile(filepath.Join(dir, "recording"), staleFiled(t, "host_state", "host-state: no report from mac"), 0o644); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, envDumper)
	if err := os.Symlink(self, bin); err != nil {
		t.Fatal(err)
	}

	if _, err := sideOf(compare.Rust{Binary: bin}, c); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ALMANAC_MODE=live", "PATH=" + os.Getenv("PATH")}
	if got := strings.Split(string(text), "\n"); !reflect.DeepEqual(got, want) {
		names := make([]string, len(got))
		for i, kv := range got {
			names[i], _, _ = strings.Cut(kv, "=")
		}
		t.Errorf("the Rust child's environment is not exactly PATH and ALMANAC_MODE=live; it has %q", names)
	}
}

func TestTheGoSideIgnoresTheProcessEnvironment(t *testing.T) {
	dir, _ := oneCase(t, "host.state.finding", stale, staleFiled(t, "host-state", "host-state: no report from mac"))
	baseline, baselineErr := run(t, "compare", "--fixtures", dir)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var connections atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			conn.Close()
		}
	}()
	for name, value := range credentials(listener.Addr().String()) {
		t.Setenv(name, value)
	}

	got, gotErr := run(t, "compare", "--fixtures", dir)
	if got != baseline || fmt.Sprint(gotErr) != fmt.Sprint(baselineErr) {
		t.Errorf("credentials in the environment changed the Go side\nwithout: %v\n%s\nwith: %v\n%s", baselineErr, baseline, gotErr, got)
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("the Go side connected to the listener %d time(s)", n)
	}
}
