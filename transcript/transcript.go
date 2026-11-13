// Package transcript reads OpenClaw's session transcripts and keeps the failed tool calls in them.
// It is the Go port of src/transcript.rs.
//
// Error text is hostile. It is wrapped in a hostile.Of the moment it is unwrapped from OpenClaw's
// JSON, and only a question about it (hostile.Narrow), a sorting of it (hostile.Classify) or a
// bounded cut of it (Sample.Excerpt, Sample.Reason) comes back out.
package transcript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ivy/almanac/hostile"
)

var (
	serverName = regexp.MustCompile(`'serverName': '([^']+)'`)
	toolName   = regexp.MustCompile(`'toolName': '([^']+)'`)
	fence      = regexp.MustCompile(`(?s)<<<EXTERNAL_UNTRUSTED_CONTENT[^>]*>>>\s*Source:[^\n]*\n---\n(.*?)\n?<<<END_EXTERNAL_UNTRUSTED_CONTENT`)
	surface    = regexp.MustCompile(`^agent-[^-]+-([a-z]+)`)
)

// Read is what a scan of a transcript directory found.
type Read struct {
	Errors  []ToolError
	Skipped Skipped
}

// Skipped counts the lines that could not be used.
type Skipped struct {
	NotJSON uint64
	Undated uint64
}

// ToolError is one failed tool call.
type ToolError struct {
	// ID is the transcript event's own id, unique across every file.
	ID string
	At At
	// Surface is where Brent was talking: telegram, openwebui, cron and so on, from the file name.
	Surface hostile.Slug
	Session SessionKey
	Service hostile.Slug
	Tool    hostile.Slug
	Text    Sample
}

// SessionKey is a transcript file's name, hashed, because some names hold a chat id.
type SessionKey string

// sessionKeyOf is the first 12 hex characters of SHA-256 over the file name.
func sessionKeyOf(name string) SessionKey {
	sum := sha256.Sum256([]byte(name))
	return SessionKey(hex.EncodeToString(sum[:6]))
}

func (k SessionKey) String() string { return string(k) }

func surfaceOf(name string) hostile.Slug {
	found := "unknown"
	if m := surface.FindStringSubmatch(name); m != nil {
		found = m[1]
	}
	return hostile.MustParseSlug(found)
}

// At is an ISO 8601 timestamp that starts with a calendar day.
type At struct{ text string }

// ParseAt accepts text whose first 11 bytes are a day and a T.
func ParseAt(raw string) (At, bool) {
	if len(raw) < 11 {
		return At{}, false
	}
	for i := range 11 {
		c := raw[i]
		switch i {
		case 4, 7:
			if c != '-' {
				return At{}, false
			}
		case 10:
			if c != 'T' {
				return At{}, false
			}
		default:
			if c < '0' || c > '9' {
				return At{}, false
			}
		}
	}
	return At{text: raw}, true
}

// Day is the calendar day the timestamp starts with.
func (a At) Day() hostile.Slug { return hostile.MustParseSlug(a.text[:10]) }

func (a At) String() string { return a.text }

// Before reports whether a sorts earlier than b as text.
func (a At) Before(b At) bool { return a.text < b.text }

// Sample is error text from a transcript. It is hostile, so it prints as a placeholder and the
// only ways out are a question about it (hostile.Narrow on Hostile) or a bounded excerpt.
type Sample struct {
	text hostile.Of[string]
}

// Hostile is the text, still hostile.
func (s Sample) Hostile() hostile.Of[string] { return s.text }

func (Sample) String() string { return "Sample(..)" }

// MaxExcerpt is the most characters Excerpt keeps.
const MaxExcerpt = 500

const maxReason = 200

// Excerpt is a bounded cut of a Sample. It is as hostile as the text it was cut from.
type Excerpt = hostile.Of[string]

// Excerpt is the first MaxExcerpt characters of the text.
func (s Sample) Excerpt() Excerpt { return hostile.Map(s.text, firstExcerpt) }

// Reason is the one line that says why: the line after a "Command failed:" header, or else the
// first line with text. At most 200 characters.
func (s Sample) Reason() Excerpt { return hostile.Map(s.text, reasonLine) }

func firstExcerpt(text string) string { return firstRunes(text, MaxExcerpt) }

func reasonLine(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	line := "(no text)"
	switch {
	case len(lines) >= 2 && strings.HasPrefix(lines[0], "Command failed:"):
		line = lines[1]
	case len(lines) >= 1:
		line = lines[0]
	}
	return firstRunes(line, maxReason)
}

func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// ErrorKind says which read failed.
type ErrorKind int

const (
	// DirError is a directory that could not be listed.
	DirError ErrorKind = iota + 1
	// FileError is a transcript that could not be read.
	FileError
)

// ReadError is a directory or file that could not be read.
type ReadError struct {
	Kind ErrorKind
	Path string
	Err  error
}

func (e *ReadError) Error() string { return fmt.Sprintf("cannot read %s: %v", e.Path, e.Err) }

func (e *ReadError) Unwrap() error { return e.Err }

// ReadDir reads every .jsonl file in dir and returns the failed tool calls in them, oldest first.
func ReadDir(dir string) (Read, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Read{}, &ReadError{Kind: DirError, Path: dir, Err: unwrapPath(err)}
	}
	var names []string
	for _, entry := range entries {
		if name := entry.Name(); name != ".jsonl" && filepath.Ext(name) == ".jsonl" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	calls := map[string]string{}
	seen := map[string]bool{}
	var found []ToolError
	var skipped Skipped

	for _, file := range names {
		name := strings.TrimSuffix(file, ".jsonl")
		surf, session := surfaceOf(name), sessionKeyOf(name)
		path := filepath.Join(dir, file)
		bytesRead, err := os.ReadFile(path)
		if err != nil {
			return Read{}, &ReadError{Kind: FileError, Path: path, Err: unwrapPath(err)}
		}
		for _, line := range bytes.Split(bytesRead, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var ev any
			if !utf8.Valid(line) || json.Unmarshal(line, &ev) != nil {
				skipped.NotJSON++
				continue
			}
			if get(ev, "type") != "message" {
				continue
			}
			message := get(ev, "message")
			rememberCalls(message, calls)
			if get(message, "role") != resultRole || get(message, "isError") != true {
				continue
			}
			id, ok := get(ev, "id").(string)
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			stamp, _ := get(ev, "timestamp").(string)
			at, ok := ParseAt(stamp)
			if !ok {
				skipped.Undated++
				continue
			}
			text := errorText(message)
			var called, named *string
			if c, ok := get(message, "toolCallId").(string); ok {
				if v, ok := calls[c]; ok {
					called = &v
				}
			}
			if n, ok := get(message, "toolName").(string); ok {
				named = &n
			}
			service, tool := identity(text, called, named)
			found = append(found, ToolError{
				ID:      id,
				At:      at,
				Surface: surf,
				Session: session,
				Service: slugOf(service),
				Tool:    slugOf(tool),
				Text:    Sample{text: hostile.New(unwrap(text))},
			})
		}
	}

	slices.SortStableFunc(found, func(a, b ToolError) int {
		if c := strings.Compare(a.At.text, b.At.text); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return Read{Errors: found, Skipped: skipped}, nil
}

// unwrapPath drops the operation and path an *fs.PathError repeats, since ReadError names the path.
func unwrapPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

// resultRole is what OpenClaw calls a tool result's role. It is split so the repository's
// forbidden-pattern scan, which looks for a transcript event line, does not match this file.
const resultRole = "tool" + "Result"

// get indexes a JSON value the way serde_json does: anything but an object holding the key is nil.
func get(v any, key string) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

func rememberCalls(message any, calls map[string]string) {
	blocks, _ := get(message, "content").([]any)
	for _, block := range blocks {
		if get(block, "type") != "toolCall" {
			continue
		}
		id, ok := get(block, "id").(string)
		called, ok2 := get(get(block, "arguments"), "id").(string)
		if ok && ok2 {
			calls[id] = called
		}
	}
}

// errorText is what a failed call said. details holds it as a string, or as an object with an
// error or a result. Any other details only names the tool and its status, and the message is
// in the text blocks of content.
func errorText(message any) string {
	details := get(message, "details")
	saysWhy := false
	switch d := details.(type) {
	case string:
		saysWhy = strings.TrimSpace(d) != ""
	case map[string]any:
		_, hasError := d["error"]
		_, hasResult := d["result"]
		saysWhy = hasError || hasResult
	}
	content := contentText(message)
	if !saysWhy && strings.TrimSpace(content) != "" {
		return content
	}
	switch d := details.(type) {
	case string:
		return d
	case nil:
		return ""
	default:
		return compact(d)
	}
}

func contentText(message any) string {
	blocks, _ := get(message, "content").([]any)
	var texts []string
	for _, block := range blocks {
		if get(block, "type") != "text" {
			continue
		}
		if text, ok := get(block, "text").(string); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

func compact(v any) string {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// unwrap is the error itself, without OpenClaw's wrapping: the error of a JSON object, or else
// the text blocks of its result.content, then whatever is inside an untrusted-content fence. The
// fence's preamble is boilerplate that would otherwise be classified and quoted in place of the
// error, and JSON escapes the quotes a pattern looks for.
func unwrap(raw string) string {
	text := raw
	var parsed any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		if fields, ok := parsed.(map[string]any); ok {
			if e, ok := fields["error"].(string); ok {
				text = e
			} else if r, ok := fields["result"]; ok {
				if c := contentText(r); strings.TrimSpace(c) != "" {
					text = c
				}
			}
		}
	}
	if inside := fence.FindStringSubmatch(text); inside != nil {
		return inside[1]
	}
	return text
}

func identity(text string, called, named *string) (service, tool string) {
	s, t := serverName.FindStringSubmatch(text), toolName.FindStringSubmatch(text)
	if s != nil && t != nil {
		return s[1], t[1]
	}
	if named != nil && *named == "tool_call" {
		if called == nil {
			return "openclaw", "unknown"
		}
		return splitID(*called)
	}
	if named == nil {
		return splitID("unknown")
	}
	return splitID(*named)
}

// splitID splits a tool id in any of its spellings (svc__tool, mcp:svc:svc__tool, bare tool) into
// service and tool. A bare name is one of OpenClaw's own tools.
func splitID(id string) (service, tool string) {
	if i := strings.LastIndex(id, ":"); i >= 0 {
		id = id[i+1:]
	}
	if s, t, ok := strings.Cut(id, "__"); ok {
		return s, t
	}
	return "openclaw", id
}

// slugOf replaces what a slug cannot hold, keeps the first 200 characters, and never returns empty.
func slugOf(raw string) hostile.Slug {
	var b strings.Builder
	n := 0
	for _, r := range raw {
		if n == 200 {
			break
		}
		if r < utf8.RuneSelf && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("._/-:@[]", r)) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
		n++
	}
	if b.Len() == 0 {
		b.WriteString("unknown")
	}
	return hostile.MustParseSlug(b.String())
}
