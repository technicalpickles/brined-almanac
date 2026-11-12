package hoststate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/technicalpickles/brined-almanac/hoststate"
)

func specExample(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../tests/fixtures/host_state/mac.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func reportWith(checks string) string {
	return fmt.Sprintf(`{"host":"mac","generated_at":"2026-11-10T15:00:00Z","interval_seconds":300,"checks":%s}`, checks)
}

func checkJSON(name, status, output string) string {
	return fmt.Sprintf(`{"name":"%s","status":%s,"since":"2026-11-10T14:50:00Z","output":"%s"}`, name, status, output)
}

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func wantParseErr(t *testing.T, text, contains string) {
	t.Helper()
	_, err := hoststate.ParseReport(text)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), contains) {
		t.Errorf("error %q lacks %q", err, contains)
	}
}

func TestParsesTheSpecExample(t *testing.T) {
	report, err := hoststate.ParseReport(specExample(t))
	if err != nil {
		t.Fatal(err)
	}
	if report.Host != "mac" || len(report.Checks) != 1 {
		t.Fatalf("%+v", report)
	}
	if report.Checks[0].Status != hoststate.StatusCritical {
		t.Errorf("status %v", report.Checks[0].Status)
	}
	if !report.Checks[0].Since.Equal(at("2026-11-10 14:50")) {
		t.Errorf("since %v", report.Checks[0].Since)
	}
}

func TestRejectsAStatusOutside0To3(t *testing.T) {
	wantParseErr(t, reportWith("["+checkJSON("gh-auth", "4", "x")+"]"), "status")
}

func TestRejectsACheckNameOutsideTheAlphabet(t *testing.T) {
	wantParseErr(t, reportWith("["+checkJSON("GH Auth", "0", "x")+"]"), "GH Auth")
}

func TestRejectsACheckNamedStale(t *testing.T) {
	wantParseErr(t, reportWith("["+checkJSON("stale", "0", "x")+"]"), "stale")
}

func TestRejectsADuplicateCheckName(t *testing.T) {
	two := "[" + checkJSON("gh-auth", "0", "x") + "," + checkJSON("gh-auth", "2", "y") + "]"
	wantParseErr(t, reportWith(two), "twice")
}

func TestAnEmptyCheckListIsValid(t *testing.T) {
	report, err := hoststate.ParseReport(reportWith("[]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 0 {
		t.Errorf("%+v", report.Checks)
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	text := strings.Replace(specExample(t), "{", `{"extra": 1,`, 1)
	if _, err := hoststate.ParseReport(text); err == nil {
		t.Fatal("want an error")
	}
}

func TestRejectsATimeWithoutAnOffset(t *testing.T) {
	text := strings.ReplaceAll(specExample(t), "2026-11-10T15:00:00Z", "2026-11-10 15:00")
	wantParseErr(t, text, "generated_at")
}

func TestCutsOutputTo500BytesOnACharBoundary(t *testing.T) {
	output := strings.Repeat("a", 499) + "€" + strings.Repeat("b", 100)
	if len(output) <= 500 {
		t.Fatal("setup")
	}
	report, err := hoststate.ParseReport(reportWith("[" + checkJSON("gh-auth", "2", output) + "]"))
	if err != nil {
		t.Fatal(err)
	}
	cut := report.Checks[0].Output
	if len(cut) > 500 {
		t.Errorf("len %d", len(cut))
	}
	if !utf8.ValidString(cut) {
		t.Error("cut output is not valid UTF-8")
	}
}

func TestTheCommittedHostsFileDeclaresTheMacAndPicklelab(t *testing.T) {
	hosts, err := hoststate.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	all := hosts.All()
	if len(all) != 2 {
		t.Fatalf("%d hosts", len(all))
	}
	if all[0].Name != "mac" || all[0].Project.String() != "brined-almanac" {
		t.Errorf("%+v", all[0])
	}
	if all[1].Name != "picklelab" || all[1].Project.String() != "homelab" {
		t.Errorf("%+v", all[1])
	}
}

func wantHostsErr(t *testing.T, text, contains string) {
	t.Helper()
	_, err := hoststate.ParseHosts(text)
	if err == nil {
		t.Fatal("want an error")
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Errorf("error %q lacks %q", err, contains)
	}
}

func TestRejectsAHostDeclaredTwice(t *testing.T) {
	text := `
[[host]]
name = "mac"
project = "brined-almanac"

[[host]]
name = "mac"
project = "brined-almanac"
`
	wantHostsErr(t, text, "twice")
}

func TestRejectsAProjectNameTaskwarriorWouldRefuse(t *testing.T) {
	wantHostsErr(t, "[[host]]\nname = \"mac\"\nproject = \"Bad Name\"\n", "mac")
}

func TestRejectsAHostNameThatIsAPath(t *testing.T) {
	wantHostsErr(t, "[[host]]\nname = \"../x\"\nproject = \"brined-almanac\"\n", "")
}

var now = at("2026-11-10 15:00")

func mac(t *testing.T) hoststate.Host {
	t.Helper()
	hosts, err := hoststate.LoadHosts()
	if err != nil {
		t.Fatal(err)
	}
	return hosts.All()[0]
}

func report(generatedAt time.Time, status hoststate.Status, since time.Time) hoststate.Read {
	return hoststate.ReadReport{Report: hoststate.Report{
		Host:            "mac",
		GeneratedAt:     generatedAt,
		IntervalSeconds: 300,
		Checks:          []hoststate.CheckResult{{Name: "gh-auth", Status: status, Since: since, Output: "GH CRITICAL - nope"}},
	}}
}

func fresh(t *testing.T, status hoststate.Status, since time.Time) []hoststate.Finding {
	return hoststate.Findings(mac(t), report(at("2026-11-10 14:59"), status, since), now, true)
}

func isStale(f hoststate.Finding) bool {
	_, ok := f.(*hoststate.StaleFinding)
	return ok
}

func wantKey(t *testing.T, f hoststate.Finding, want string) {
	t.Helper()
	if got := f.Key(); got != want {
		t.Errorf("key %q, want %q", got, want)
	}
}

func TestTheThresholdsAreTheDocumentedOnes(t *testing.T) {
	if hoststate.CriticalAfter != 10*time.Minute || hoststate.WarningAfter != 24*time.Hour ||
		hoststate.StaleAfter != 30*time.Minute || hoststate.WakeGap != 20*time.Minute {
		t.Fatal("thresholds changed")
	}
}

func TestACriticalCheckFilesAfterTenMinutes(t *testing.T) {
	found := fresh(t, hoststate.StatusCritical, at("2026-11-10 14:50"))
	if len(found) != 1 {
		t.Fatalf("%d findings", len(found))
	}
	if _, ok := found[0].(*hoststate.CheckFinding); !ok {
		t.Fatalf("%T", found[0])
	}
	wantKey(t, found[0], "mac/gh-auth/2026-11-10T14:50Z")
}

func TestACriticalCheckUnderTenMinutesFilesNothing(t *testing.T) {
	if found := fresh(t, hoststate.StatusCritical, at("2026-11-10 14:51")); len(found) != 0 {
		t.Errorf("%v", found)
	}
}

func TestUnknownFilesLikeCritical(t *testing.T) {
	found := fresh(t, hoststate.StatusUnknown, at("2026-11-10 14:50"))
	if len(found) != 1 {
		t.Fatalf("%d findings", len(found))
	}
	if _, ok := found[0].(*hoststate.CheckFinding); !ok {
		t.Fatalf("%T", found[0])
	}
}

func TestAWarningFilesAfterADay(t *testing.T) {
	found := fresh(t, hoststate.StatusWarning, at("2026-11-09 15:00"))
	if len(found) != 1 {
		t.Fatalf("%d findings", len(found))
	}
	wantKey(t, found[0], "mac/gh-auth/2026-11-09T15:00Z")
}

func TestAWarningUnderADayFilesNothing(t *testing.T) {
	if found := fresh(t, hoststate.StatusWarning, at("2026-11-09 15:01")); len(found) != 0 {
		t.Errorf("%v", found)
	}
}

func TestAnOkCheckFilesNothing(t *testing.T) {
	if found := fresh(t, hoststate.StatusOK, at("2026-11-01 00:00")); len(found) != 0 {
		t.Errorf("%v", found)
	}
}

func TestALongOutageKeepsOneKey(t *testing.T) {
	since := at("2026-11-08 23:00")
	early := hoststate.Findings(mac(t), report(at("2026-11-09 00:59"), hoststate.StatusCritical, since), at("2026-11-09 01:00"), true)
	late := fresh(t, hoststate.StatusCritical, since)
	wantKey(t, early[0], "mac/gh-auth/2026-11-08T23:00Z")
	wantKey(t, late[0], "mac/gh-auth/2026-11-08T23:00Z")
}

func TestAStaleReportFilesStaleAndNoChecks(t *testing.T) {
	generated := at("2026-11-10 14:29")
	found := hoststate.Findings(mac(t), report(generated, hoststate.StatusCritical, at("2026-11-10 14:00")), now, true)
	if len(found) != 1 {
		t.Fatalf("%d findings", len(found))
	}
	stale, ok := found[0].(*hoststate.StaleFinding)
	if !ok || stale.Since == nil || !stale.Since.Equal(generated) {
		t.Fatalf("%+v", found[0])
	}
	wantKey(t, found[0], "mac/stale/2026-11-10T14:29Z")
}

func TestAReportAtThirtyMinutesIsNotStale(t *testing.T) {
	found := hoststate.Findings(mac(t), report(at("2026-11-10 14:30"), hoststate.StatusOK, at("2026-11-10 14:00")), now, true)
	for _, f := range found {
		if isStale(f) {
			t.Errorf("stale finding %v", f)
		}
	}
}

func TestAStaleReportThatMayNotFileFilesNothing(t *testing.T) {
	found := hoststate.Findings(mac(t), report(at("2026-11-10 14:29"), hoststate.StatusCritical, at("2026-11-10 14:00")), now, false)
	if len(found) != 0 {
		t.Errorf("%v", found)
	}
}

func TestOnlyARecentEarlierScanIsTrusted(t *testing.T) {
	ago := func(d time.Duration) *time.Time { p := now.Add(-d); return &p }
	later := now.Add(time.Second)
	cases := []struct {
		name     string
		previous *time.Time
		want     bool
	}{
		{"none", nil, false},
		{"15 minutes", ago(15 * time.Minute), true},
		{"the wake gap", ago(hoststate.WakeGap), true},
		{"21 minutes", ago(21 * time.Minute), false},
		{"one second ahead", &later, false},
	}
	for _, c := range cases {
		if got := hoststate.ScanGapIsTrusted(c.previous, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAMissingReportIsStale(t *testing.T) {
	found := hoststate.Findings(mac(t), hoststate.ReadMissing{}, now, true)
	if len(found) != 1 {
		t.Fatalf("%d findings", len(found))
	}
	stale, ok := found[0].(*hoststate.StaleFinding)
	if !ok || stale.Since != nil {
		t.Fatalf("%+v", found[0])
	}
	wantKey(t, found[0], "mac/stale/missing")
}

func TestAnInvalidReportIsStaleWithTheError(t *testing.T) {
	read := hoststate.ReadInvalid{Modified: at("2026-11-10 14:00"), Error: "x"}
	found := hoststate.Findings(mac(t), read, now, true)
	if len(found) != 1 || !isStale(found[0]) {
		t.Fatalf("%v", found)
	}
	wantKey(t, found[0], "mac/stale/2026-11-10T14:00Z")
	if !strings.Contains(found[0].Body(), "x") {
		t.Errorf("body %q", found[0].Body())
	}
}

func TestASinceInTheFutureFilesNothing(t *testing.T) {
	if found := fresh(t, hoststate.StatusCritical, at("2026-11-10 15:30")); len(found) != 0 {
		t.Errorf("%v", found)
	}
}

func TestAGeneratedAtInTheFutureIsFresh(t *testing.T) {
	found := hoststate.Findings(mac(t), report(at("2026-11-10 15:30"), hoststate.StatusOK, at("2026-11-10 14:00")), now, true)
	for _, f := range found {
		if isStale(f) {
			t.Errorf("stale finding %v", f)
		}
	}
}

func TestTheTitleNamesCheckStatusHostAndTime(t *testing.T) {
	found := fresh(t, hoststate.StatusCritical, at("2026-11-10 14:50"))
	want := "host-state: gh-auth critical on mac since 2026-11-10 14:50Z"
	if got := found[0].Title(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheStaleTitleNamesTheHostAndTime(t *testing.T) {
	found := hoststate.Findings(mac(t), report(at("2026-11-10 14:29"), hoststate.StatusCritical, at("2026-11-10 14:00")), now, true)
	want := "host-state: no report from mac since 2026-11-10 14:29Z"
	if got := found[0].Title(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestACheckBodyIsOutputStatusAndSince(t *testing.T) {
	found := fresh(t, hoststate.StatusCritical, at("2026-11-10 14:50"))
	want := "GH CRITICAL - nope\nstatus: critical\nsince: 2026-11-10T14:50:00Z"
	if got := found[0].Body(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func dirWith(t *testing.T, file *string) string {
	t.Helper()
	dir := t.TempDir()
	if file != nil {
		if err := os.WriteFile(filepath.Join(dir, "mac.json"), []byte(*file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadsAValidReport(t *testing.T) {
	text := specExample(t)
	if _, ok := hoststate.ReadHost(dirWith(t, &text), mac(t)).(hoststate.ReadReport); !ok {
		t.Fatal("want a report")
	}
}

func TestAMissingFileIsMissing(t *testing.T) {
	if got := hoststate.ReadHost(dirWith(t, nil), mac(t)); got != (hoststate.ReadMissing{}) {
		t.Fatalf("%v", got)
	}
}

func TestATruncatedFileIsInvalid(t *testing.T) {
	text := `{"host":`
	if _, ok := hoststate.ReadHost(dirWith(t, &text), mac(t)).(hoststate.ReadInvalid); !ok {
		t.Fatal("want invalid")
	}
}

func TestAReportNamingAnotherHostIsInvalid(t *testing.T) {
	text := strings.ReplaceAll(specExample(t), `"mac"`, `"lab"`)
	invalid, ok := hoststate.ReadHost(dirWith(t, &text), mac(t)).(hoststate.ReadInvalid)
	if !ok {
		t.Fatal("want invalid")
	}
	if !strings.Contains(invalid.Error, "lab") || !strings.Contains(invalid.Error, "mac") {
		t.Errorf("error %q", invalid.Error)
	}
}

func TestContinueOutagesKeepsTheFirstSinceWithinTheHold(t *testing.T) {
	var episodes hoststate.Episodes
	first := &hoststate.CheckFinding{Host: "mac", Check: "gh-auth", Status: hoststate.StatusCritical, Since: at("2026-11-10 14:00")}
	episodes.ContinueOutages([]hoststate.Finding{first}, at("2026-11-10 14:10"))
	back := &hoststate.CheckFinding{Host: "mac", Check: "gh-auth", Status: hoststate.StatusUnknown, Since: at("2026-11-10 14:40")}
	episodes.ContinueOutages([]hoststate.Finding{back}, at("2026-11-10 14:45"))
	wantKey(t, back, "mac/gh-auth/2026-11-10T14:00Z")

	late := &hoststate.CheckFinding{Host: "mac", Check: "gh-auth", Status: hoststate.StatusCritical, Since: at("2026-11-10 17:00")}
	episodes.ContinueOutages([]hoststate.Finding{late}, at("2026-11-10 17:05"))
	wantKey(t, late, "mac/gh-auth/2026-11-10T17:00Z")
}
