package compare_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The golden file lists each disagreement; docs/divergences.md must explain every one, and
// must not explain one that is gone.

type diffKey struct{ caseName, signature string }

var idLine = regexp.MustCompile(`^DV-[A-Z0-9-]+$`)

const quoted = `"(?:[^"\\]|\\.)*"`

var (
	runsLine  = regexp.MustCompile(`^runs: rust (.+), go (.+)$`)
	oneRun    = regexp.MustCompile(`^(` + quoted + `) (ran|stopped|failed|budget-tripped)(?: \((` + quoted + `)\))?(?: \[((?:[^"\]]|` + quoted + `)*)\])?`)
	oneEffect = regexp.MustCompile(`^([^ ;"]+)(?: ` + quoted + ` to (` + quoted + `))?`)

	effectList  = `\[((?:[^"\]]|` + quoted + `)*)\]`
	reasonLine  = regexp.MustCompile(`^(run \d+ reason): rust (\(none\)|` + quoted + `), go (\(none\)|` + quoted + `)$`)
	effectsLine = regexp.MustCompile(`^(run \d+ effects): rust (\(none\)|` + effectList + `), go (\(none\)|` + effectList + `)$`)
)

// consume applies one element pattern to a "; "-separated list and requires the whole text to
// be covered, so a run or an effect cannot hide behind the first one.
func consume(text string, re *regexp.Regexp, each func(m []string) string) ([]string, bool) {
	var out []string
	for {
		m := re.FindStringSubmatch(text)
		if m == nil {
			return nil, false
		}
		out = append(out, each(m))
		text = text[len(m[0]):]
		if text == "" {
			return out, true
		}
		if !strings.HasPrefix(text, "; ") {
			return nil, false
		}
		text = text[2:]
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// effectsSignature keeps every effect as kind and target (a title carries fixture text and
// stays out). A list it cannot read in full is not parsed at all.
func effectsSignature(list string) (string, bool) {
	effects, ok := consume(list, oneEffect, func(e []string) string {
		if e[2] != "" {
			return e[1] + " to " + e[2]
		}
		return e[1]
	})
	return strings.Join(effects, "; "), ok
}

// runListSignature keeps every run's workflow and result, a hash of the failure reason (the
// reason can quote fixture text, so the doc carries only its hash), and every effect as kind
// and target.
func runListSignature(list string) (string, bool) {
	if list == "(no run)" {
		return list, true
	}
	allOK := true
	runs, ok := consume(list, oneRun, func(m []string) string {
		sig := m[1] + " " + m[2]
		if m[3] != "" {
			sig += " (reason#" + shortHash(m[3]) + ")"
		}
		if m[4] != "" {
			effects, ok := effectsSignature(m[4])
			if !ok {
				allOK = false
			}
			sig += " [" + effects + "]"
		}
		return sig
	})
	return strings.Join(runs, "; "), ok && allOK
}

func hashedReason(side string) string {
	if side == "(none)" {
		return side
	}
	return "reason#" + shortHash(side)
}

// effectsSide reduces one side of a "run N effects:" line, the text after "rust " or "go ".
func effectsSide(side string) (string, bool) {
	if side == "(none)" {
		return side, true
	}
	effects, ok := effectsSignature(side[1 : len(side)-1])
	return "[" + effects + "]", ok
}

// signatureOf reduces a difference line to what a divergence row names. Text that can carry
// fixture content never reaches the doc: a runs line goes through runListSignature, a reason
// line is hashed, an effects line keeps kind and target. A line of those kinds that does not
// parse becomes "<kind>: unparsed <hash>", so it cannot match a listed row by accident. Every
// other line holds only enums, counts and keys.
func signatureOf(line string) string {
	if m := runsLine.FindStringSubmatch(line); m != nil {
		// Both sides may hold ", go " inside a quoted string, so try each split.
		whole := m[1] + ", go " + m[2]
		for i := 0; i+5 <= len(whole); i++ {
			if whole[i:i+5] != ", go " {
				continue
			}
			rust, ok1 := runListSignature(whole[:i])
			golang, ok2 := runListSignature(whole[i+5:])
			if ok1 && ok2 {
				return fmt.Sprintf("runs: rust %s, go %s", rust, golang)
			}
		}
		return "runs: unparsed " + shortHash(line)
	}
	if m := reasonLine.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf("%s: rust %s, go %s", m[1], hashedReason(m[2]), hashedReason(m[3]))
	}
	if strings.HasPrefix(line, "run ") && strings.Contains(line, " reason:") {
		return "reason line: unparsed " + shortHash(line)
	}
	if m := effectsLine.FindStringSubmatch(line); m != nil {
		rust, ok1 := effectsSide(m[2])
		golang, ok2 := effectsSide(m[4])
		if ok1 && ok2 {
			return fmt.Sprintf("%s: rust %s, go %s", m[1], rust, golang)
		}
	}
	if strings.HasPrefix(line, "run ") && strings.Contains(line, " effects:") {
		return "effects line: unparsed " + shortHash(line)
	}
	return line
}

func goldenDifferences(t *testing.T) map[diffKey]bool {
	t.Helper()
	text, err := os.ReadFile("testdata/compare.golden")
	if err != nil {
		t.Fatal(err)
	}
	found := map[diffKey]bool{}
	current := ""
	for _, line := range strings.Split(string(text), "\n") {
		switch {
		case strings.HasPrefix(line, "DISAGREE  "):
			current = strings.TrimPrefix(line, "DISAGREE  ")
		case strings.HasPrefix(line, "          ") && current != "":
			found[diffKey{current, signatureOf(strings.TrimSpace(line))}] = true
		default:
			current = ""
		}
	}
	return found
}

func listedDivergences(t *testing.T) (map[diffKey]string, string) {
	t.Helper()
	raw, err := os.ReadFile("../docs/divergences.md")
	if err != nil {
		t.Fatal(err)
	}
	return parseDivergences(t, string(raw)), string(raw)
}

func parseDivergences(t *testing.T, doc string) map[diffKey]string {
	t.Helper()
	const open = "```divergences\n"
	start := strings.Index(doc, open)
	if start < 0 {
		t.Fatal("docs/divergences.md has no divergences block")
	}
	body := doc[start+len(open):]
	end := strings.Index(body, "```")
	if end < 0 {
		t.Fatal("docs/divergences.md: the divergences block is not closed")
	}
	listed := map[diffKey]string{}
	rows := strings.TrimSpace(body[:end])
	if rows == "" {
		return listed
	}
	for _, row := range strings.Split(rows, "\n") {
		cols := strings.Split(row, " | ")
		if len(cols) != 3 || !idLine.MatchString(strings.TrimSpace(cols[0])) {
			t.Fatalf("docs/divergences.md: malformed row %q, want: id | signature | cases", row)
		}
		id, signature := strings.TrimSpace(cols[0]), strings.TrimSpace(cols[1])
		for _, c := range strings.Split(cols[2], ",") {
			key := diffKey{strings.TrimSpace(c), signature}
			if prior, dup := listed[key]; dup {
				t.Fatalf("docs/divergences.md: %s listed twice (%s and %s)", key.caseName, prior, id)
			}
			listed[key] = id
		}
	}
	return listed
}

func sortedKeys(keys []diffKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.caseName + ": " + k.signature
	}
	sort.Strings(out)
	return out
}

func TestEveryGoldenDifferenceIsListedInDivergences(t *testing.T) {
	found := goldenDifferences(t)
	listed, _ := listedDivergences(t)
	var unexplained []diffKey
	for k := range found {
		if _, ok := listed[k]; !ok {
			unexplained = append(unexplained, k)
		}
	}
	if len(unexplained) > 0 {
		t.Errorf("differences in compare.golden that docs/divergences.md does not list:\n  %s",
			strings.Join(sortedKeys(unexplained), "\n  "))
	}
}

func TestEveryListedDivergenceStillAppearsInTheGolden(t *testing.T) {
	found := goldenDifferences(t)
	listed, _ := listedDivergences(t)
	var stale []diffKey
	for k := range listed {
		if !found[k] {
			stale = append(stale, k)
		}
	}
	if len(stale) > 0 {
		t.Errorf("divergences listed in docs/divergences.md that compare.golden no longer shows:\n  %s",
			strings.Join(sortedKeys(stale), "\n  "))
	}
}

func TestEveryDivergenceHasASectionWithAStatusAndAnOwner(t *testing.T) {
	listed, doc := listedDivergences(t)
	ids := map[string]bool{}
	for _, id := range listed {
		ids[id] = true
	}
	for id := range ids {
		heading := regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(id) + `(?::.*)?$`)
		loc := heading.FindStringIndex(doc)
		if loc == nil {
			t.Errorf("%s has rows but no %q section", id, "## "+id)
			continue
		}
		section := doc[loc[1]:]
		if next := strings.Index(section, "\n## "); next >= 0 {
			section = section[:next]
		}
		for _, field := range []string{"**Status:**", "**Owner:**"} {
			if !strings.Contains(section, field) {
				t.Errorf("%s: section has no %s line", id, field)
			}
		}
	}
}

func TestAnEmptyDivergencesBlockIsLegal(t *testing.T) {
	if got := parseDivergences(t, "# x\n\n```divergences\n```\n"); len(got) != 0 {
		t.Errorf("want no rows, got %v", got)
	}
}

func TestSignatureOfReducesFixtureText(t *testing.T) {
	const secret = "SECRET-FIXTURE-TEXT"
	tests := []struct {
		name, line, want string
	}{
		{"stopped run keeps its reason hash",
			`runs: rust "w" stopped ("` + secret + `"), go (no run)`,
			"runs: rust \"w\" stopped (reason#" + shortHash(`"`+secret+`"`) + "), go (no run)"},
		{"budget-tripped run parses",
			`runs: rust "w" budget-tripped, go "w" ran`,
			`runs: rust "w" budget-tripped, go "w" ran`},
		{"effects keep kind and target, not title",
			`runs: rust "w" ran [file "` + secret + `" to "t/r"], go (no run)`,
			`runs: rust "w" ran [file to "t/r"], go (no run)`},
		{"reason line is hashed",
			`run 1 reason: rust "` + secret + `", go (none)`,
			"run 1 reason: rust reason#" + shortHash(`"`+secret+`"`) + ", go (none)"},
		{"effects line drops titles",
			`run 2 effects: rust [file "` + secret + `" to "t/r"; skip], go (none)`,
			`run 2 effects: rust [file to "t/r"; skip], go (none)`},
		{"unreadable reason line is hashed whole",
			`run 1 reason: rust ` + secret + `, go (none)`,
			"reason line: unparsed " + shortHash(`run 1 reason: rust `+secret+`, go (none)`)},
		{"unreadable effects line is hashed whole",
			`run 1 effects: rust [file ` + secret + `], go (none)`,
			"effects line: unparsed " + shortHash(`run 1 effects: rust [file `+secret+`], go (none)`)},
		{"other lines pass through",
			`ingest: rust "rejected", go "queued"`,
			`ingest: rust "rejected", go "queued"`},
	}
	for _, tc := range tests {
		got := signatureOf(tc.line)
		if got != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.name, got, tc.want)
		}
		if strings.Contains(got, secret) {
			t.Errorf("%s: signature carries fixture text: %s", tc.name, got)
		}
	}
}

func TestUnreadableEffectsDoNotCollapseIntoOneSignature(t *testing.T) {
	a := signatureOf(`runs: rust "w" ran [file "x" to "y" junk], go (no run)`)
	b := signatureOf(`runs: rust "w" ran [file "x" to "z" junk], go (no run)`)
	for _, sig := range []string{a, b} {
		if !strings.HasPrefix(sig, "runs: unparsed ") {
			t.Errorf("want the whole line unparsed, got %s", sig)
		}
	}
	if a == b {
		t.Errorf("different unreadable lines share one signature: %s", a)
	}
}
