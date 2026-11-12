package compare

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Filed is one filing as a side's LogTracker printed it: the tracker key, with the id of the run
// that filed it written {run}, and the body.
type Filed struct {
	Key  string `json:"key"`
	Body Body   `json:"body"`
}

// Body is a filed body's lines. It carries the event's text, so it is only ever shown as a line
// count and a hash.
type Body []string

// Hash is the start of the body's sha256, enough to tell two bodies apart in a report.
func (b Body) Hash() string {
	sum := sha256.Sum256([]byte(strings.Join(b, "\n")))
	return hex.EncodeToString(sum[:4])
}

func (b Body) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Lines  int    `json:"lines"`
		Sha256 string `json:"sha256"`
	}{len(b), b.Hash()})
}

// filingLine starts each filing a LogTracker prints. The title follows it on the same line, and
// the body on the lines after, up to the next filing.
const filingLine = "FILE key="

// filing is one filing as printed, its key not yet tied to a run.
type filing struct {
	key  string
	body Body
}

// parseFilings reads what a LogTracker printed. Both sides print a filing as
// "FILE key=K target=T title=Q", a newline, the body and a newline. The title is left out, since
// the effects already compare it and the two sides quote it differently (Rust {:?}, Go %q). A
// body line that starts "FILE key=" reads as a filing of its own, the same way on both sides.
func parseFilings(text string) ([]filing, error) {
	if text == "" {
		return nil, nil
	}
	var out []filing
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, filingLine); ok {
			key, _, _ := strings.Cut(rest, " ")
			out = append(out, filing{key: key, body: Body{}})
			continue
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("printed %d byte(s) before the first filing", len(text))
		}
		last := &out[len(out)-1]
		last.body = append(last.body, line)
	}
	return out, nil
}

// fileUnder gives each filing to the run whose id starts its key. runIDs are the runs' ids, in
// the order of runs, and empty for an outcome with no run.
func fileUnder(runs []Run, runIDs []string, filings []filing) error {
	for _, f := range filings {
		at := -1
		for i, id := range runIDs {
			if id != "" && strings.HasPrefix(f.key, id+":") {
				at = i
			}
		}
		if at < 0 {
			return fmt.Errorf("filed under key %s, which is no run's", strconv.Quote(f.key))
		}
		runs[at].Filed = append(runs[at].Filed, Filed{Key: "{run}" + strings.TrimPrefix(f.key, runIDs[at]), Body: f.body})
	}
	return nil
}

// bodyDifference names the lines two bodies differ on, by position, and each body's hash.
func bodyDifference(rust, golang Body) string {
	var lines []string
	for i := range max(len(rust), len(golang)) {
		if i >= len(rust) || i >= len(golang) || rust[i] != golang[i] {
			lines = append(lines, strconv.Itoa(i+1))
		}
	}
	const shown = 10
	more := ""
	if len(lines) > shown {
		more = fmt.Sprintf(" and %d more", len(lines)-shown)
		lines = lines[:shown]
	}
	return fmt.Sprintf("line(s) %s%s differ (rust %d line(s) %s, go %d line(s) %s)",
		strings.Join(lines, ", "), more, len(rust), rust.Hash(), len(golang), golang.Hash())
}
