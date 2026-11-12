package securityalerts_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ivy/almanac/diag"
	"github.com/ivy/almanac/github"

	"github.com/technicalpickles/brined-almanac/securityalerts"
)

func day(y int, m time.Month, d int) securityalerts.Day {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

var today = day(2026, 11, 8)

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../tests/fixtures/security_alerts/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func dependency(severity securityalerts.Severity, created securityalerts.Day) securityalerts.Dependency {
	return securityalerts.Dependency{
		ID: 1, Severity: severity, Package: "toml", Manifest: "package-lock.json",
		Ghsa: "GHSA-aaaa-bbbb-cccc", Patched: "5.0.1", Created: created, URL: "https://example.test/1",
	}
}

func numbered(d securityalerts.Dependency, id uint64) securityalerts.Dependency {
	d.ID = id
	return d
}

func secret(created securityalerts.Day) securityalerts.Secret {
	return securityalerts.Secret{ID: 2, Kind: "Token", Created: created, URL: "https://example.test/2"}
}

func TestWindowsMatchTheSpec(t *testing.T) {
	for name, got := range map[string][2]int{
		"secret":   {secret(today).WindowDays(), 0},
		"critical": {dependency(securityalerts.Critical, today).WindowDays(), 2},
		"high":     {dependency(securityalerts.High, today).WindowDays(), 14},
		"medium":   {dependency(securityalerts.Medium, today).WindowDays(), 30},
		"unknown":  {dependency(securityalerts.Unknown, today).WindowDays(), 30},
		"low":      {dependency(securityalerts.Low, today).WindowDays(), 90},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: %d days, want %d", name, got[0], got[1])
		}
	}
}

func TestOverdueStartsOnTheLastDayOfTheWindow(t *testing.T) {
	high := []securityalerts.Alert{dependency(securityalerts.High, day(2026, 10, 25))}
	if got := securityalerts.Overdue(high, day(2026, 11, 7)); len(got) != 0 {
		t.Errorf("overdue a day early: %v", got)
	}
	if got := securityalerts.Overdue(high, day(2026, 11, 8)); len(got) != 1 {
		t.Errorf("not overdue on the last day: %v", got)
	}
	if got := securityalerts.Overdue([]securityalerts.Alert{secret(today)}, today); len(got) != 1 {
		t.Errorf("a secret is not overdue the day it opens: %v", got)
	}
}

func TestAgeUsesTheUTCDate(t *testing.T) {
	v := fixture(t, "dependabot_alert.json")
	v["created_at"] = "2026-10-25T23:30:00-05:00"
	alert, err := securityalerts.ParseDependabot(raw(t, v))
	if err != nil {
		t.Fatal(err)
	}
	if got := alert.(securityalerts.Dependency).Created; !got.Equal(day(2026, 10, 26)) {
		t.Errorf("created %v", got)
	}
}

func TestDigestIgnoresOrderAndChangesWithTheSet(t *testing.T) {
	a := []string{"dependabot:1", "secret:2"}
	b := []string{"secret:2", "dependabot:1"}
	more := []string{"dependabot:1", "secret:2", "dependabot:3"}
	if securityalerts.Digest(a) != securityalerts.Digest(b) {
		t.Error("order changed the digest")
	}
	got := securityalerts.Digest(a)
	if len(got) != 12 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Errorf("digest %q", got)
	}
	if got == securityalerts.Digest(more) {
		t.Error("a bigger set kept the digest")
	}
}

func TestADigestIsTheFirstTwelveHexOfSHA256OverTheSortedKeys(t *testing.T) {
	if got := securityalerts.Digest([]string{"b", "a"}); got != "7e18f737311b" {
		t.Errorf("digest %q", got)
	}
}

func TestAnUnknownSeverityIsUnknown(t *testing.T) {
	if securityalerts.SeverityFromGithub("moderate") != securityalerts.Unknown || securityalerts.SeverityFromGithub("high") != securityalerts.High {
		t.Error("severity")
	}
}

func TestAMissingPatchedVersionSaysNoFixYet(t *testing.T) {
	v := fixture(t, "dependabot_alert.json")
	v["security_vulnerability"].(map[string]any)["first_patched_version"] = nil
	alert, err := securityalerts.ParseDependabot(raw(t, v))
	if err != nil {
		t.Fatal(err)
	}
	if line := alert.Line(day(2026, 11, 8)); !strings.Contains(line, "no fix yet") {
		t.Error(line)
	}
}

func TestARecordedDependabotAlertReadsIntoALine(t *testing.T) {
	alert, err := securityalerts.ParseDependabot(raw(t, fixture(t, "dependabot_alert.json")))
	if err != nil {
		t.Fatal(err)
	}
	if alert.Key() != "dependabot:37" {
		t.Errorf("key %q", alert.Key())
	}
	want := "#37 high toml (package-lock.json) GHSA-4qx8-w7c3-pj3p, fixed in 5.0.1, 13 days, https://github.com/technicalpickles/dotfiles/security/dependabot/37"
	if got := alert.Line(day(2026, 11, 8)); got != want {
		t.Errorf("line %q", got)
	}
}

func TestASecretValueNeverReachesTheLine(t *testing.T) {
	v := fixture(t, "secret_alert.json")
	if v["secret"] != "ghp_DO_NOT_LEAK" {
		t.Fatal("the fixture lost its secret")
	}
	alert, err := securityalerts.ParseSecret(raw(t, v))
	if err != nil {
		t.Fatal(err)
	}
	for _, shown := range []string{fmt.Sprintf("%#v", alert), alert.Line(day(2026, 11, 8))} {
		if strings.Contains(shown, "ghp_DO_NOT_LEAK") {
			t.Errorf("leaked: %s", shown)
		}
	}
	want := "#4 secret GitHub Personal Access Token, 1 days, https://github.com/technicalpickles/example/security/secret-scanning/4"
	if got := alert.Line(day(2026, 11, 8)); got != want {
		t.Errorf("line %q", got)
	}
}

func TestASecretWithoutADisplayNameFallsBackToItsType(t *testing.T) {
	v := fixture(t, "secret_alert.json")
	delete(v, "secret_type_display_name")
	alert, err := securityalerts.ParseSecret(raw(t, v))
	if err != nil {
		t.Fatal(err)
	}
	if line := alert.Line(day(2026, 11, 8)); !strings.Contains(line, "github_personal_access_token") {
		t.Error(line)
	}
}

func TestASecretWithNeitherNameIsRefused(t *testing.T) {
	v := fixture(t, "secret_alert.json")
	delete(v, "secret_type_display_name")
	delete(v, "secret_type")
	_, err := securityalerts.ParseSecret(raw(t, v))
	if err == nil || err.Error() != "missing secret_type_display_name and secret_type" {
		t.Errorf("%v", err)
	}
}

func TestAnAlertMissingAFieldNamesIt(t *testing.T) {
	for _, c := range []struct {
		drop func(map[string]any)
		want string
	}{
		{func(v map[string]any) { delete(v, "number") }, "missing number"},
		{func(v map[string]any) { v["number"] = -1 }, "missing number"},
		{func(v map[string]any) { v["number"] = 1.5 }, "missing number"},
		{func(v map[string]any) { delete(v["security_advisory"].(map[string]any), "severity") }, "missing security_advisory.severity"},
		{func(v map[string]any) { delete(v["dependency"].(map[string]any), "manifest_path") }, "missing dependency.manifest_path"},
		{func(v map[string]any) { delete(v, "html_url") }, "missing html_url"},
		{func(v map[string]any) { delete(v, "created_at") }, "missing created_at"},
	} {
		v := fixture(t, "dependabot_alert.json")
		c.drop(v)
		if _, err := securityalerts.ParseDependabot(raw(t, v)); err == nil || err.Error() != c.want {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	v := fixture(t, "dependabot_alert.json")
	v["created_at"] = "yesterday"
	if _, err := securityalerts.ParseDependabot(raw(t, v)); err == nil || !strings.HasPrefix(err.Error(), `created_at "yesterday": `) {
		t.Errorf("%v", err)
	}
	if _, err := securityalerts.ParseDependabot(json.RawMessage(`[1]`)); err == nil {
		t.Error("an array parsed")
	}
}

func TestNothingOverdueIsEmpty(t *testing.T) {
	alerts := []securityalerts.Alert{
		dependency(securityalerts.Critical, day(2026, 11, 7)),
		dependency(securityalerts.Medium, day(2026, 10, 20)),
		dependency(securityalerts.Low, day(2026, 8, 20)),
	}
	if got := securityalerts.Overdue(alerts, today); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func refused(status int) error {
	err := diag.Refused("GET /repos/technicalpickles/example/x", status)
	err.Cause = &github.Refusal{Status: status}
	return err
}

const example = "technicalpickles/example"

func TestVulnerabilityAlerts404IsOff(t *testing.T) {
	on, err := securityalerts.AlertsSwitch(refused(404), example)
	if err != nil || on {
		t.Errorf("on %v, err %v", on, err)
	}
}

func TestVulnerabilityAlertsAnEmpty204IsOn(t *testing.T) {
	on, err := securityalerts.AlertsSwitch(nil, example)
	if err != nil || !on {
		t.Errorf("on %v, err %v", on, err)
	}
}

func TestVulnerabilityAlerts403IsAnErrorNamingTheRepo(t *testing.T) {
	_, err := securityalerts.AlertsSwitch(refused(403), example)
	if err == nil || !strings.Contains(err.Error(), example) || !strings.Contains(err.Error(), "403") {
		t.Errorf("%v", err)
	}
}

func TestAnErrorThatIsNotARefusalIsNotOff(t *testing.T) {
	_, err := securityalerts.AlertsSwitch(errors.New("connection reset"), example)
	if err == nil || !strings.Contains(err.Error(), example) {
		t.Errorf("%v", err)
	}
}

func TestSecretScanning404IsOff(t *testing.T) {
	got, err := securityalerts.SecretAlerts(nil, refused(404), example)
	if err != nil || got.On {
		t.Errorf("%+v, err %v", got, err)
	}
}

func TestSecretScanning401IsAnErrorNamingTheRepo(t *testing.T) {
	_, err := securityalerts.SecretAlerts(nil, refused(401), example)
	if err == nil || !strings.Contains(err.Error(), example) || !strings.Contains(err.Error(), "401") {
		t.Errorf("%v", err)
	}
}

func TestSecretScanningAlertsParseIntoOn(t *testing.T) {
	got, err := securityalerts.SecretAlerts([]json.RawMessage{raw(t, fixture(t, "secret_alert.json"))}, nil, example)
	if err != nil || !got.On || len(got.Alerts) != 1 {
		t.Errorf("%+v, err %v", got, err)
	}
}

func TestSecretScanningAnUnreadableAlertFailsNamingTheRepo(t *testing.T) {
	_, err := securityalerts.SecretAlerts([]json.RawMessage{json.RawMessage(`{}`)}, nil, example)
	if err == nil || !strings.HasPrefix(err.Error(), example+": ") {
		t.Errorf("%v", err)
	}
}

func TestAMissingRepositoryNamesBothTiersFiles(t *testing.T) {
	msg := securityalerts.RepoMissing(refused(404), example).Error()
	if !strings.Contains(msg, example) || !strings.Contains(msg, "check tiers.toml and local/tiers.toml") {
		t.Error(msg)
	}
}

func TestAnotherRepositoryFailureKeepsTheStatus(t *testing.T) {
	msg := securityalerts.RepoMissing(refused(500), example).Error()
	if !strings.Contains(msg, example) || !strings.Contains(msg, "500") {
		t.Error(msg)
	}
}

type reads = []securityalerts.Repo

func read(private bool, dependabot, secrets securityalerts.Coverage) securityalerts.RepoRead {
	return securityalerts.RepoRead{Private: private, Dependabot: dependabot, Secrets: secrets}
}

func on(alerts ...securityalerts.Alert) securityalerts.Coverage {
	return securityalerts.Coverage{On: true, Alerts: alerts}
}

var off = securityalerts.Coverage{}

func cleanRead() securityalerts.RepoRead { return read(false, on(), on()) }

func TestFindingsFilesOneTaskPerRepositoryWithOverdueAlerts(t *testing.T) {
	overdueHigh := numbered(dependency(securityalerts.High, day(2026, 10, 1)), 7)
	freshHigh := numbered(dependency(securityalerts.High, day(2026, 11, 7)), 8)
	found, err := securityalerts.Findings(reads{
		{Name: "technicalpickles/dotfiles", Read: read(false, on(overdueHigh, freshHigh), on())},
		{Name: "technicalpickles/quiet", Read: cleanRead()},
	}, today)
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v, err %v", found, err)
	}
	if want := "scan:security-alerts/technicalpickles/dotfiles@" + securityalerts.Digest([]string{overdueHigh.Key()}); found[0].DeliveryID != want {
		t.Errorf("delivery %q, want %q", found[0].DeliveryID, want)
	}
	p := found[0].Payload
	if p.Kind != "overdue" || p.Project != "dotfiles" || p.Subject != "technicalpickles/dotfiles" || p.High != 1 {
		t.Errorf("%+v", p)
	}
}

func TestARepositoryWhoseOverdueAlertsWereAllFixedFilesNothing(t *testing.T) {
	fresh := numbered(dependency(securityalerts.High, day(2026, 11, 7)), 8)
	found, err := securityalerts.Findings(reads{{Name: "technicalpickles/dotfiles", Read: read(false, on(fresh), on())}}, today)
	if err != nil || len(found) != 0 {
		t.Errorf("%+v, err %v", found, err)
	}
}

func TestFindingsListsCoverageGapsOnce(t *testing.T) {
	found, err := securityalerts.Findings(reads{
		{Name: "technicalpickles/example-private", Read: read(true, off, off)},
		{Name: "technicalpickles/example-public", Read: read(false, on(), off)},
	}, today)
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v, err %v", found, err)
	}
	keys := []string{"dependabot-off:technicalpickles/example-private", "secret-scanning-off:technicalpickles/example-public"}
	if want := "scan:security-alerts/coverage@" + securityalerts.Digest(keys); found[0].DeliveryID != want {
		t.Errorf("delivery %q, want %q", found[0].DeliveryID, want)
	}
	p := found[0].Payload
	if p.Kind != "coverage" || p.Subject != "coverage" || p.Project != "brined-almanac" || p.DependabotOff != 1 || p.SecretScanningOff != 1 || p.Repositories != 2 {
		t.Errorf("%+v", p)
	}
	if want := "Dependabot alerts are off: technicalpickles/example-private\nSecret scanning is off: technicalpickles/example-public"; p.Lines != want {
		t.Errorf("lines %q", p.Lines)
	}
}

func TestARepositoryOffOnBothCountsOnceInRepositories(t *testing.T) {
	found, err := securityalerts.Findings(reads{{Name: "technicalpickles/example-public", Read: read(false, off, off)}}, today)
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v, err %v", found, err)
	}
	if p := found[0].Payload; p.DependabotOff != 1 || p.SecretScanningOff != 1 || p.Repositories != 1 {
		t.Errorf("%+v", p)
	}
}

func TestNoGapsAndNothingOverdueIsNoFindings(t *testing.T) {
	found, err := securityalerts.Findings(reads{
		{Name: "technicalpickles/dotfiles", Read: cleanRead()},
		{Name: "technicalpickles/example-private", Read: read(true, on(), off)},
	}, today)
	if err != nil || len(found) != 0 {
		t.Errorf("%+v, err %v", found, err)
	}
}

func TestOverdueSecretsAndAlertLinesReachTheBodyHighestSeverityFirst(t *testing.T) {
	found, err := securityalerts.Findings(reads{{
		Name: "technicalpickles/dotfiles",
		Read: read(false,
			on(numbered(dependency(securityalerts.Critical, day(2026, 11, 1)), 9)),
			on(secret(day(2026, 11, 7)))),
	}}, today)
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v, err %v", found, err)
	}
	p := found[0].Payload
	if p.Critical != 1 || p.Secrets != 1 {
		t.Errorf("%+v", p)
	}
	critical, secretAt := strings.Index(p.Lines, "#9 critical"), strings.Index(p.Lines, "#2 secret")
	if critical < 0 || secretAt < 0 || critical > secretAt {
		t.Error(p.Lines)
	}
}

func TestLinesAreRankedBySeverityThenNumber(t *testing.T) {
	found, err := securityalerts.Findings(reads{{
		Name: "technicalpickles/dotfiles",
		Read: read(false, on(
			numbered(dependency(securityalerts.Low, day(2026, 1, 1)), 3),
			numbered(dependency(securityalerts.Unknown, day(2026, 1, 1)), 4),
			numbered(dependency(securityalerts.Medium, day(2026, 1, 1)), 6),
			numbered(dependency(securityalerts.Medium, day(2026, 1, 1)), 5),
			numbered(dependency(securityalerts.High, day(2026, 1, 1)), 8),
			numbered(dependency(securityalerts.Critical, day(2026, 1, 1)), 9),
		), on(secret(today))),
	}}, today)
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v, err %v", found, err)
	}
	var order []string
	for _, line := range strings.Split(found[0].Payload.Lines, "\n") {
		order = append(order, strings.Fields(line)[0])
	}
	if got := strings.Join(order, " "); got != "#9 #8 #5 #6 #4 #3 #2" {
		t.Errorf("order %s", got)
	}
	p := found[0].Payload
	if p.Critical != 1 || p.High != 1 || p.Medium != 2 || p.Unknown != 1 || p.Low != 1 || p.Secrets != 1 {
		t.Errorf("%+v", p)
	}
}

func TestAnInvalidProjectNameFailsNamingTheRepository(t *testing.T) {
	_, err := securityalerts.Findings(reads{{
		Name: "technicalpickles/Bad..Name",
		Read: read(false, on(numbered(dependency(securityalerts.High, day(2026, 10, 1)), 7)), on()),
	}}, today)
	if err == nil || !strings.Contains(err.Error(), "technicalpickles/Bad..Name") {
		t.Errorf("%v", err)
	}
}

func overdueAndOff() securityalerts.RepoRead {
	return read(false, on(numbered(dependency(securityalerts.High, day(2026, 10, 1)), 7)), off)
}

func TestAFailingRepositoryIsSkippedAndHoldsTheCoverageTask(t *testing.T) {
	tick := securityalerts.NewTick([]securityalerts.Result{
		{Repo: "technicalpickles/dotfiles", Read: overdueAndOff()},
		{Repo: "technicalpickles/broken", Err: errors.New("technicalpickles/broken: HTTP 502")},
	})
	if len(tick.Failed) != 1 || !strings.Contains(tick.Failed[0].Error(), "technicalpickles/broken") {
		t.Fatalf("%v", tick.Failed)
	}
	found, err := tick.Findings(today)
	if err != nil || len(found) != 1 || !strings.HasPrefix(found[0].DeliveryID, "scan:security-alerts/technicalpickles/dotfiles@") {
		t.Errorf("%+v, err %v", found, err)
	}
}

func TestAFullReadFilesTheCoverageTask(t *testing.T) {
	tick := securityalerts.NewTick([]securityalerts.Result{{Repo: "technicalpickles/dotfiles", Read: overdueAndOff()}})
	found, err := tick.Findings(today)
	if err != nil || len(found) != 2 || !strings.HasPrefix(found[1].DeliveryID, "scan:security-alerts/coverage@") {
		t.Errorf("%+v, err %v", found, err)
	}
}

func TestEveryRepositoryFailingFailsTheScan(t *testing.T) {
	tick := securityalerts.NewTick([]securityalerts.Result{
		{Repo: "technicalpickles/a", Err: errors.New("technicalpickles/a: HTTP 401")},
		{Repo: "technicalpickles/b", Err: errors.New("technicalpickles/b: HTTP 401")},
	})
	_, err := tick.Findings(today)
	if err == nil || err.Error() != "technicalpickles/a: HTTP 401; technicalpickles/b: HTTP 401" {
		t.Errorf("%v", err)
	}
}

func TestNoRepositoriesIsNoFindings(t *testing.T) {
	found, err := securityalerts.NewTick(nil).Findings(today)
	if err != nil || len(found) != 0 {
		t.Errorf("%+v, err %v", found, err)
	}
}
