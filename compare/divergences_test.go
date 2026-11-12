package compare_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The golden file lists each disagreement; docs/divergences.md must explain every one, and
// must not explain one that is gone.

type diffKey struct{ caseName, signature string }

var (
	runsLine = regexp.MustCompile(`^(runs: rust \(no run\), go "[^"]+" (?:ran|failed))\b`)
	idLine   = regexp.MustCompile(`^DV-[A-Z0-9-]+$`)
)

// signatureOf reduces a difference line to what a divergence row names. The detail of a runs
// line holds titles and error text, which stay out of the doc.
func signatureOf(line string) string {
	if m := runsLine.FindStringSubmatch(line); m != nil {
		return m[1]
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
			listed[diffKey{strings.TrimSpace(c), signature}] = id
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
		heading := "## " + id
		start := strings.Index(doc, heading)
		if start < 0 {
			t.Errorf("%s has rows but no %q section", id, heading)
			continue
		}
		section := doc[start+len(heading):]
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
