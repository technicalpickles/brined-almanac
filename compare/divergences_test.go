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
	oneRun    = regexp.MustCompile(`^(` + quoted + `) (ran|failed)(?: \((` + quoted + `)\))?(?: \[((?:[^"\]]|` + quoted + `)*)\])?`)
	oneEffect = regexp.MustCompile(`^([^ ;"]+)(?: ` + quoted + ` to (` + quoted + `))?`)
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

// runListSignature keeps every run's workflow and result, a hash of the failure reason (the
// reason can quote fixture text, so the doc carries only its hash), and every effect as kind
// and target (a title carries fixture text and stays out).
func runListSignature(list string) (string, bool) {
	if list == "(no run)" {
		return list, true
	}
	runs, ok := consume(list, oneRun, func(m []string) string {
		sig := m[1] + " " + m[2]
		if m[3] != "" {
			sig += " (reason#" + shortHash(m[3]) + ")"
		}
		if m[4] != "" {
			effects, ok := consume(m[4], oneEffect, func(e []string) string {
				if e[2] != "" {
					return e[1] + " to " + e[2]
				}
				return e[1]
			})
			if !ok {
				return "[unparsed effects]"
			}
			sig += " [" + strings.Join(effects, "; ") + "]"
		}
		return sig
	})
	return strings.Join(runs, "; "), ok
}

// signatureOf reduces a difference line to what a divergence row names. A runs line is kept
// whole except for text that can carry fixture content (see runListSignature); any other line is
// already free of it.
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
	doc := string(raw)
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
	for _, row := range strings.Split(strings.TrimSpace(body[:end]), "\n") {
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
	return listed, doc
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
