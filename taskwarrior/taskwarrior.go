// Package taskwarrior files findings as taskwarrior tasks through the `task` command, on a
// replica that syncs with a TaskChampion server.
//
// Tasks are created with `task import` from JSON on stdin, never from command-line arguments:
// `task` parses its arguments for attributes and tags, so a title like "fix project:x +y" would
// become something else. Everything this package hands `task` as an argument is its own.
package taskwarrior

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"
	"github.com/ivy/almanac/hostile/reveal"

	"github.com/technicalpickles/brined-almanac/routing"
)

const (
	// KeyAttr is the UDA holding the delivery key a task was filed under.
	KeyAttr = "almanac_key"
	// TargetAttr is the UDA holding the target the task was filed for.
	TargetAttr = "almanac_target"
	// PullAttr is the UDA holding the pull request a question is about.
	PullAttr = "almanac_pull"
	// Tag is the tag every filed task carries.
	Tag = "almanac"
	// QuestionTag is the tag a question carries.
	QuestionTag = "question"
)

// Server is where the replica syncs: exactly one of LocalDir or Remote.
type Server struct {
	LocalDir string
	Remote   *Remote
}

type Remote struct {
	URL              string
	ClientID         string
	EncryptionSecret string
}

type Config struct {
	DataDir string
	Server  Server
}

// Taskwarrior is effect.Tracker on a taskwarrior replica. Calls are serialized, since each one
// syncs and reads the whole replica before it writes.
type Taskwarrior struct {
	config Config
	mu     sync.Mutex
}

var _ effect.Tracker = (*Taskwarrior)(nil)

func New(config Config) *Taskwarrior { return &Taskwarrior{config: config} }

// Annotation is a note on a task.
type Annotation struct {
	Entry       string `json:"entry"`
	Description string `json:"description"`
}

// Task is a task as `task export` and `task import` write it, with the fields almanac uses.
type Task struct {
	UUID        string       `json:"uuid"`
	Description string       `json:"description"`
	Status      string       `json:"status"`
	Entry       string       `json:"entry"`
	Modified    string       `json:"modified"`
	Project     string       `json:"project,omitempty"`
	Tags        []string     `json:"tags,omitempty"`
	Annotations []Annotation `json:"annotations,omitempty"`
	Key         string       `json:"almanac_key,omitempty"`
	Target      string       `json:"almanac_target,omitempty"`
	Pull        string       `json:"almanac_pull,omitempty"`
}

func (t *Taskwarrior) File(ctx context.Context, key string, issue effect.Issue) error {
	return t.deliver(ctx, key, issue.Target, issue.Title, issue.Body, false, 0)
}

func (t *Taskwarrior) Ask(ctx context.Context, key string, question effect.Question) error {
	return t.deliver(ctx, key, question.Target, question.Question, question.Body, true, question.Pull)
}

// Sync syncs the replica with the server.
func (t *Taskwarrior) Sync(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sync(ctx)
}

// Tasks is every task in the replica, as of its last sync.
func (t *Taskwarrior) Tasks(ctx context.Context) ([]Task, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tasks(ctx)
}

// SyncCheck pulls the server's whole history into the replica at config.DataDir, which must be
// new or empty, and returns how many tasks arrived. A replica with no local changes has nothing
// to push, so this reads without writing. Pulling forces every version through decryption, which
// is what proves the client id and encryption secret match the history. An empty history proves
// nothing, so it is an error.
func SyncCheck(ctx context.Context, config Config) (int, error) {
	t := New(config)
	if err := t.Sync(ctx); err != nil {
		return 0, err
	}
	tasks, err := t.Tasks(ctx)
	if err != nil {
		return 0, err
	}
	if len(tasks) == 0 {
		return 0, errors.New("the server's history is empty, so decryption was not exercised")
	}
	return len(tasks), nil
}

func (t *Taskwarrior) deliver(ctx context.Context, key string, target hostile.Slug, title string, body hostile.Of[string], question bool, pull uint64) error {
	project, isTask, err := routing.FromTarget(target.String())
	if err != nil {
		return err
	}
	if !isTask {
		return fmt.Errorf("%s is not a task: target", target)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.sync(ctx); err != nil {
		return err
	}
	known, err := t.tasks(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(known, func(k Task) bool { return k.Key == key }) {
		return nil
	}

	uuid, err := newUUID()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	task := Task{
		UUID:        uuid,
		Description: title,
		Status:      "pending",
		Entry:       now,
		Modified:    now,
		Project:     project.String(),
		Tags:        []string{Tag},
		Annotations: []Annotation{{Entry: now, Description: "almanac " + key + "\n" + reveal.Value(body)}},
		Key:         key,
		Target:      target.String(),
	}
	if question {
		task.Tags = append(task.Tags, QuestionTag)
	}
	if pull != 0 {
		task.Pull = strconv.FormatUint(pull, 10)
	}
	batch, err := json.Marshal([]Task{task})
	if err != nil {
		return err
	}
	// The task's own text is left out of the error: stderr may echo it.
	if _, err := t.run(ctx, batch, "import", "-"); err != nil {
		return errors.New("could not commit the taskwarrior task")
	}
	return t.sync(ctx)
}

func (t *Taskwarrior) sync(ctx context.Context) error {
	// The local server does not create its directory; create it as the replica's is.
	if dir := t.config.Server.LocalDir; dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return errors.New("could not open the taskwarrior server")
		}
	}
	if _, err := t.run(ctx, nil, "sync"); err != nil {
		return fmt.Errorf("taskwarrior sync failed: %w", err)
	}
	return nil
}

func (t *Taskwarrior) tasks(ctx context.Context) ([]Task, error) {
	out, err := t.run(ctx, nil, "export")
	if err != nil {
		return nil, fmt.Errorf("could not read the taskwarrior replica: %w", err)
	}
	var tasks []Task
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("could not read the taskwarrior replica: %w", err)
	}
	return tasks, nil
}

// run calls `task` on this replica, with a taskrc it writes first so the server settings,
// secret included, never appear on a command line. A failure carries the first line of stderr.
func (t *Taskwarrior) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	rc, err := t.writeRC()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "task", append([]string{"rc:" + rc}, args...)...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TASKRC=") || strings.HasPrefix(kv, "TASKDATA=")
	})
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if line := firstLine(stderr.Bytes()); line != "" {
			return nil, errors.New(line)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

func (t *Taskwarrior) writeRC() (string, error) {
	dir := t.config.DataDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errors.New("could not open the taskwarrior replica directory")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "data.location=%s\n", dir)
	switch s := t.config.Server; {
	case s.Remote != nil:
		fmt.Fprintf(&b, "sync.server.url=%s\nsync.server.client_id=%s\nsync.encryption_secret=%s\n",
			s.Remote.URL, s.Remote.ClientID, s.Remote.EncryptionSecret)
	case s.LocalDir != "":
		fmt.Fprintf(&b, "sync.local.server_dir=%s\n", s.LocalDir)
	default:
		return "", errors.New("no taskwarrior server is configured")
	}
	for _, uda := range []string{KeyAttr, TargetAttr, PullAttr} {
		fmt.Fprintf(&b, "uda.%s.type=string\n", uda)
	}
	b.WriteString("confirmation=off\nverbose=nothing\nhooks=off\nnews.version=99\n")
	rc := filepath.Join(dir, "taskrc")
	if err := os.WriteFile(rc, []byte(b.String()), 0o600); err != nil {
		return "", errors.New("could not write the taskwarrior configuration")
	}
	return rc, nil
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return line
}

func newUUID() (string, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", err
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]), nil
}
