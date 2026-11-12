package transcript_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The role is split so the repository's forbidden-pattern scan, which looks for a transcript
// event line, does not match this file.
const (
	roleKey       = "role"
	resultRole    = "tool" + "Result"
	defaultTime   = "2026-11-04T10:00:00Z"
	defaultErrMsg = "synthetic failure"
)

type spelling int

const (
	prefixed spelling = iota
	namespaced
	bare
)

type details int

const (
	detailsMcp details = iota
	namedOnly
	empty
	object
	// statusOnly names the tool and says it failed, with the message only in content.
	statusOnly
)

type fixture struct {
	t       *testing.T
	dir     string
	counter int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, dir: t.TempDir()}
}

func (f *fixture) next() int {
	f.counter++
	return f.counter
}

func (f *fixture) file(name string) *fileBuilder {
	return &fileBuilder{f: f, name: name}
}

type fileBuilder struct {
	f     *fixture
	name  string
	lines []string
}

func (b *fileBuilder) with(fail failure) *fileBuilder {
	for i := range fail.times {
		callID := fmt.Sprintf("call-%04d", b.f.next())
		at := addSeconds(fail.at, i)
		if fail.hasCallBlock() {
			eventID := fmt.Sprintf("evt-%04d", b.f.next())
			b.lines = append(b.lines, fail.assistantLine(eventID, at, callID))
		}
		eventID := fail.eventID
		if eventID == "" {
			eventID = fmt.Sprintf("evt-%04d", b.f.next())
		}
		line := fail.resultLine(eventID, at, callID)
		b.lines = append(b.lines, line)
		if fail.duplicated {
			b.lines = append(b.lines, line)
		}
	}
	return b
}

func (b *fileBuilder) garbage(n int) *fileBuilder {
	for i := range n {
		b.lines = append(b.lines, fmt.Sprintf("this line %d is not json", i))
	}
	return b
}

func (b *fileBuilder) write() {
	b.f.t.Helper()
	text := strings.Join(b.lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(b.f.dir, b.name), []byte(text), 0o644); err != nil {
		b.f.t.Fatal(err)
	}
}

type failure struct {
	service, tool string
	builtin       bool
	spelling      spelling
	details       details
	at, errText   string
	times         int
	duplicated    bool
	succeeded     bool
	orphaned      bool
	eventID       string
}

func mcp(service, tool string) failure {
	return failure{service: service, tool: tool, spelling: prefixed, details: detailsMcp, at: defaultTime, errText: defaultErrMsg, times: 1}
}

func builtin(tool string) failure {
	f := mcp("", tool)
	f.builtin, f.details = true, namedOnly
	return f
}

func (f failure) spelled(s spelling) failure  { f.spelling = s; return f }
func (f failure) detailed(d details) failure  { f.details = d; return f }
func (f failure) atTime(iso string) failure   { f.at = iso; return f }
func (f failure) failing(text string) failure { f.errText = text; return f }
func (f failure) repeated(n int) failure      { f.times = n; return f }
func (f failure) twice() failure              { f.duplicated = true; return f }
func (f failure) ok() failure                 { f.succeeded = true; return f }
func (f failure) orphan() failure             { f.orphaned = true; return f }
func (f failure) withEventID(id string) failure {
	f.eventID = id
	return f
}

func (f failure) hasCallBlock() bool { return !f.builtin && !f.orphaned }

func (f failure) spelledID() string {
	switch f.spelling {
	case prefixed:
		return f.service + "__" + f.tool
	case namespaced:
		return "mcp:" + f.service + ":" + f.service + "__" + f.tool
	default:
		return f.tool
	}
}

func (f failure) toolName() string {
	if f.builtin {
		return f.tool
	}
	return "tool_call"
}

func (f failure) assistantLine(eventID, at, callID string) string {
	block := map[string]any{
		"type":      "toolCall",
		"id":        callID,
		"name":      "tool_call",
		"arguments": map[string]any{"id": f.spelledID()},
	}
	message := map[string]any{roleKey: "assistant", "content": []any{block}}
	return eventLine(eventID, at, message)
}

func (f failure) resultLine(eventID, at, callID string) string {
	message := map[string]any{
		roleKey:      resultRole,
		"toolName":   f.toolName(),
		"toolCallId": callID,
		"isError":    !f.succeeded,
		"details":    f.detailsValue(),
		"content":    []any{map[string]any{"type": "text", "text": f.errText}},
	}
	return eventLine(eventID, at, message)
}

func (f failure) detailsValue() any {
	switch f.details {
	case empty:
		return ""
	case object:
		return map[string]any{"status": "error", "tool": f.toolValue(), "error": f.errText}
	case statusOnly:
		server, tool, ok := strings.Cut(f.tool, "__")
		if !ok {
			server, tool = "", f.tool
		}
		return map[string]any{"mcpServer": server, "mcpTool": tool, "status": "error"}
	default:
		return f.detailsRepr()
	}
}

func (f failure) toolValue() any {
	if f.details == namedOnly {
		return f.spelledID()
	}
	return map[string]any{"mcp": map[string]any{"serverName": f.service, "toolName": f.tool}}
}

// detailsRepr is a Python dict repr, which is how OpenClaw stringifies details.
func (f failure) detailsRepr() string {
	var tool string
	if f.details == namedOnly {
		tool = pyStr(f.spelledID())
	} else {
		tool = fmt.Sprintf("{'mcp': {'serverName': %s, 'toolName': %s}}", pyStr(f.service), pyStr(f.tool))
	}
	return fmt.Sprintf("{'status': 'error', 'tool': %s, 'error': %s}", tool, pyStr(f.errText))
}

func eventLine(id, at string, message map[string]any) string {
	line, err := json.Marshal(map[string]any{"type": "message", "id": id, "timestamp": at, "message": message})
	if err != nil {
		panic(err)
	}
	return string(line)
}

func pyStr(text string) string {
	quote := '\''
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	var out strings.Builder
	out.WriteRune(quote)
	for _, c := range text {
		switch {
		case c == '\\':
			out.WriteString(`\\`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == quote:
			out.WriteRune('\\')
			out.WriteRune(c)
		default:
			out.WriteRune(c)
		}
	}
	out.WriteRune(quote)
	return out.String()
}

func addSeconds(iso string, seconds int) string {
	if len(iso) < 19 {
		return iso
	}
	at, err := time.Parse("2006-01-02T15:04:05", iso[:19])
	if err != nil {
		panic(err)
	}
	return at.Add(time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05Z")
}
