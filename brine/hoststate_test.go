package brine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivy/almanac/wire"
	"github.com/ivy/almanac/workflow"

	"github.com/technicalpickles/brined-almanac/brine"
	"github.com/technicalpickles/brined-almanac/hoststate"
)

var now = time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)

func critical(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../tests/fixtures/host_state/mac.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hosts(t *testing.T) hoststate.Hosts {
	t.Helper()
	h, err := hoststate.ParseHosts("[[host]]\nname = \"mac\"\nproject = \"brined-almanac\"\n")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func dirWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func ago(d time.Duration) *time.Time {
	t := now.Add(-d)
	return &t
}

func scan(t *testing.T, dir string) []workflow.Found {
	return scanAfter(t, dir, ago(15*time.Minute))
}

func scanAfter(t *testing.T, dir string, previous *time.Time) []workflow.Found {
	t.Helper()
	return brine.ScanFindings(hosts(t), dir, now, previous, &hoststate.Episodes{})
}

func field(t *testing.T, found workflow.Found, name string) any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload[name]
}

func staleReport(t *testing.T) string {
	return strings.Replace(critical(t), `"generated_at": "2026-11-10T15:00:00Z"`, `"generated_at": "2026-11-10T14:29:00Z"`, 1)
}

func wantOne(t *testing.T, found []workflow.Found) workflow.Found {
	t.Helper()
	if len(found) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(found), found)
	}
	return found[0]
}

func firstEvent(t *testing.T) map[string]any {
	t.Helper()
	found := wantOne(t, scan(t, dirWith(t, map[string]string{"mac.json": critical(t)})))
	var payload map[string]any
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func parse(t *testing.T, payload map[string]any) (brine.HostFinding, error) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return wire.Parse[brine.HostFinding](raw)
}

func wantMalformed(t *testing.T, payload map[string]any, pointer string) {
	t.Helper()
	_, err := parse(t, payload)
	var malformed *wire.Malformed
	if !errors.As(err, &malformed) || malformed.Pointer != pointer {
		t.Fatalf("want malformed at %s, got %v", pointer, err)
	}
}

func TestACriticalReportScansToOneEvent(t *testing.T) {
	found := wantOne(t, scan(t, dirWith(t, map[string]string{"mac.json": critical(t)})))
	if found.DeliveryID != "mac/gh-auth/2026-11-10T14:50Z" {
		t.Errorf("delivery ID %q", found.DeliveryID)
	}
	if found.Kind != "host.state.finding" {
		t.Errorf("kind %q", found.Kind)
	}
	if field(t, found, "kind") != "check" || field(t, found, "project") != "brined-almanac" {
		t.Errorf("payload %s", found.Payload)
	}
}

func TestAHealthyReportScansToNothing(t *testing.T) {
	healthy := strings.Replace(critical(t), `"status": 2`, `"status": 0`, 1)
	if found := scan(t, dirWith(t, map[string]string{"mac.json": healthy})); len(found) != 0 {
		t.Fatalf("%+v", found)
	}
}

func TestADeclaredHostWithNoFileScansToStale(t *testing.T) {
	found := wantOne(t, scan(t, dirWith(t, nil)))
	if found.DeliveryID != "mac/stale/missing" || field(t, found, "kind") != "stale" {
		t.Fatalf("%s %s", found.DeliveryID, found.Payload)
	}
}

func TestAnUndeclaredReportFilesNothing(t *testing.T) {
	lab := strings.Replace(critical(t), `"host": "mac"`, `"host": "lab"`, 1)
	found := wantOne(t, scan(t, dirWith(t, map[string]string{"mac.json": critical(t), "lab.json": lab})))
	if found.DeliveryID != "mac/gh-auth/2026-11-10T14:50Z" {
		t.Fatal(found.DeliveryID)
	}
}

func TestAReportNamingAnotherHostScansToOneStalePair(t *testing.T) {
	other := strings.Replace(critical(t), `"host": "mac"`, `"host": "lab"`, 1)
	found := wantOne(t, scan(t, dirWith(t, map[string]string{"mac.json": other})))
	if field(t, found, "kind") != "stale" || field(t, found, "host") != "mac" {
		t.Fatal(string(found.Payload))
	}
}

func TestTheEventParsesBack(t *testing.T) {
	event, err := parse(t, firstEvent(t))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != brine.KindCheck || event.Subject.String() != "mac.gh-auth.2026-11-10T14:50Z" ||
		event.Check.String() != "gh-auth" || event.Status.String() != "critical" || event.Since.String() != "2026-11-10T14:50Z" {
		t.Fatalf("%+v", event)
	}
}

func TestAPayloadWithAnotherKindIsMalformed(t *testing.T) {
	payload := firstEvent(t)
	payload["kind"] = "other"
	wantMalformed(t, payload, "/kind")
}

func TestAPayloadWithABadProjectIsMalformed(t *testing.T) {
	payload := firstEvent(t)
	payload["project"] = "Bad Name"
	wantMalformed(t, payload, "/project")
}

func TestACheckWithoutCheckStatusOrSinceIsMalformed(t *testing.T) {
	for _, name := range []string{"check", "status", "since"} {
		payload := firstEvent(t)
		delete(payload, name)
		wantMalformed(t, payload, "/"+name)
	}
}

func TestASinceThatIsNotASlugIsMalformed(t *testing.T) {
	payload := firstEvent(t)
	payload["since"] = "2026-11-10 14:50Z"
	wantMalformed(t, payload, "/since")
}

func TestAProjectThatIsASlugButNotATaskProjectIsMalformed(t *testing.T) {
	for _, kind := range []string{"check", "stale"} {
		payload := firstEvent(t)
		payload["kind"] = kind
		payload["project"] = "Bad"
		wantMalformed(t, payload, "/project")
	}
}

func TestASinceThatDoesNotSplitAtTIsMalformed(t *testing.T) {
	for _, kind := range []string{"check", "stale"} {
		for _, since := range []string{"T", "2026-11-10T", "T14:50Z", "2026-11-10"} {
			payload := firstEvent(t)
			payload["kind"] = kind
			payload["since"] = since
			wantMalformed(t, payload, "/since")
		}
	}
}

func TestAStaleFindingWithoutSinceParses(t *testing.T) {
	payload := firstEvent(t)
	payload["kind"] = "stale"
	delete(payload, "since")
	if event, err := parse(t, payload); err != nil || event.Since != nil {
		t.Fatalf("%+v %v", event, err)
	}
}

func TestTheFilingTargetsTheProjectAndSplitsTheSince(t *testing.T) {
	event, err := parse(t, firstEvent(t))
	if err != nil {
		t.Fatal(err)
	}
	filing, err := brine.FilingOf(event)
	if err != nil {
		t.Fatal(err)
	}
	if filing.Target.String() != "task:brined-almanac" || filing.Since.Date.String() != "2026-11-10" || filing.Since.Time.String() != "14:50Z" {
		t.Fatalf("%+v %+v", filing, filing.Since)
	}
}

func TestTheReportsDirIsEmptyWhenUnsetOrEmpty(t *testing.T) {
	unset := func(string) string { return "" }
	if dir := brine.HostReportsFrom(unset).Dir; dir != "" {
		t.Errorf("unset: %q", dir)
	}
	set := func(name string) string {
		if name == "HOST_STATE_REPORTS" {
			return "/host-state"
		}
		return ""
	}
	if dir := brine.HostReportsFrom(set).Dir; dir != "/host-state" {
		t.Errorf("set: %q", dir)
	}
}

func TestAStaleReportWithNoPreviousScanFilesNothing(t *testing.T) {
	if found := scanAfter(t, dirWith(t, map[string]string{"mac.json": staleReport(t)}), nil); len(found) != 0 {
		t.Fatalf("%+v", found)
	}
}

func TestAStaleReportAfterAScanFifteenMinutesAgoFilesStaleOnly(t *testing.T) {
	found := wantOne(t, scanAfter(t, dirWith(t, map[string]string{"mac.json": staleReport(t)}), ago(15*time.Minute)))
	if found.DeliveryID != "mac/stale/2026-11-10T14:29Z" || field(t, found, "kind") != "stale" {
		t.Fatalf("%s %s", found.DeliveryID, found.Payload)
	}
}

func TestAStaleReportAfterATwentyOneMinuteGapFilesNothing(t *testing.T) {
	if found := scanAfter(t, dirWith(t, map[string]string{"mac.json": staleReport(t)}), ago(21*time.Minute)); len(found) != 0 {
		t.Fatalf("%+v", found)
	}
}

func TestAStaleReportAfterAScanLaterThanNowFilesNothing(t *testing.T) {
	if found := scanAfter(t, dirWith(t, map[string]string{"mac.json": staleReport(t)}), ago(-5*time.Minute)); len(found) != 0 {
		t.Fatalf("%+v", found)
	}
}

func TestAMissingReportFilesWithNoPreviousScan(t *testing.T) {
	if found := wantOne(t, scanAfter(t, dirWith(t, nil), nil)); found.DeliveryID != "mac/stale/missing" {
		t.Fatal(found.DeliveryID)
	}
}

func TestAnInvalidReportFilesWithNoPreviousScan(t *testing.T) {
	found := wantOne(t, scanAfter(t, dirWith(t, map[string]string{"mac.json": "{"}), nil))
	if field(t, found, "kind") != "stale" {
		t.Fatal(string(found.Payload))
	}
}

type truth struct {
	status  int
	invalid bool
	silent  bool
}

func status(s int) truth { return truth{status: s} }

// tasksFiled replays minutes of a host whose one check follows at, with the collector writing
// every 5 minutes as checks/collect does and a scan every 15, and counts the tasks filed.
func tasksFiled(t *testing.T, minutes int, at func(m int) truth) int {
	t.Helper()
	start := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	path := filepath.Join(dir, "mac.json")
	var last *struct {
		status int
		since  time.Time
	}
	episodes := &hoststate.Episodes{}
	filed := map[string]bool{}
	for m := range minutes {
		at_ := start.Add(time.Duration(m) * time.Minute)
		if m%5 == 0 {
			switch truth := at(m); {
			case truth.invalid:
				if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, at_, at_); err != nil {
					t.Fatal(err)
				}
			case truth.silent:
			default:
				since := at_
				if last != nil && last.status == truth.status {
					since = last.since
				}
				last = &struct {
					status int
					since  time.Time
				}{truth.status, since}
				report := fmt.Sprintf(`{"host":"mac","generated_at":%q,"interval_seconds":300,"checks":[{"name":"c","status":%d,"since":%q,"output":""}]}`,
					at_.Format(time.RFC3339), truth.status, since.Format(time.RFC3339))
				if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if m%15 == 7 && m > 15 {
			previous := at_.Add(-15 * time.Minute)
			for _, found := range brine.ScanFindings(hosts(t), dir, at_, &previous, episodes) {
				filed[found.DeliveryID] = true
			}
		}
	}
	return len(filed)
}

func within(m, from, to int) bool { return m >= from && m < to }

func TestASecondOutageTheSameDayFilesAgain(t *testing.T) {
	got := tasksFiled(t, 600, func(m int) truth {
		if within(m, 60, 110) || within(m, 240, 300) {
			return status(2)
		}
		return status(0)
	})
	if got != 2 {
		t.Fatal(got)
	}
}

func TestAnOutageAcrossMidnightFilesOnce(t *testing.T) {
	got := tasksFiled(t, 60*40, func(m int) truth {
		if within(m, 60, 60*37) {
			return status(2)
		}
		return status(0)
	})
	if got != 1 {
		t.Fatal(got)
	}
}

func TestACheckFlappingInsideTheHoldFilesOnce(t *testing.T) {
	got := tasksFiled(t, 540, func(m int) truth {
		if m >= 30 && m%40 < 30 {
			return status(2)
		}
		return status(0)
	})
	if got != 1 {
		t.Fatal(got)
	}
}

func TestACheckWobblingBetweenCriticalAndUnknownFilesOnce(t *testing.T) {
	got := tasksFiled(t, 240, func(m int) truth {
		switch {
		case m < 30:
			return status(0)
		case (m/5)%2 == 0:
			return status(2)
		default:
			return status(3)
		}
	})
	if got != 1 {
		t.Fatal(got)
	}
}

func TestACheckFlappingFasterThanTheThresholdFilesOnce(t *testing.T) {
	got := tasksFiled(t, 420, func(m int) truth {
		if m >= 30 && (m/5)%2 == 0 {
			return status(2)
		}
		return status(0)
	})
	if got != 1 {
		t.Fatal(got)
	}
}

func TestACheckHoveringBetweenWarningAndCriticalFilesCritical(t *testing.T) {
	got := tasksFiled(t, 240, func(m int) truth {
		switch {
		case m < 30:
			return status(0)
		case (m/20)%2 == 0:
			return status(1)
		default:
			return status(2)
		}
	})
	if got != 1 {
		t.Fatal(got)
	}
}

func TestAWarningThatTurnsCriticalFilesBoth(t *testing.T) {
	got := tasksFiled(t, 60*34, func(m int) truth {
		switch {
		case m < 60:
			return status(0)
		case m < 60*31:
			return status(1)
		default:
			return status(2)
		}
	})
	if got != 2 {
		t.Fatal(got)
	}
}

func TestAnUnreadableReportRewrittenEveryRunFilesOnce(t *testing.T) {
	got := tasksFiled(t, 240, func(m int) truth {
		if within(m, 30, 210) {
			return truth{invalid: true}
		}
		return status(0)
	})
	if got != 1 {
		t.Fatal(got)
	}
}

func TestACollectorThatStopsTwiceInADayFilesTwice(t *testing.T) {
	got := tasksFiled(t, 480, func(m int) truth {
		if within(m, 60, 120) || within(m, 240, 300) {
			return truth{silent: true}
		}
		return status(0)
	})
	if got != 2 {
		t.Fatal(got)
	}
}
