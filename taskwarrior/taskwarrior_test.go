package taskwarrior_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ivy/almanac/effect"
	"github.com/ivy/almanac/hostile"

	"github.com/technicalpickles/brined-almanac/taskwarrior"
)

func needTask(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("task"); err != nil {
		t.Fatal("these tests run the real `task` command (taskwarrior 3), which is not on PATH")
	}
}

func local(dir string) taskwarrior.Server {
	return taskwarrior.Server{LocalDir: filepath.Join(dir, "server")}
}

func open(t *testing.T, dir, name string) *taskwarrior.Taskwarrior {
	needTask(t)
	return taskwarrior.New(taskwarrior.Config{DataDir: filepath.Join(dir, name), Server: local(dir)})
}

var target = hostile.MustParseSlug("task:picklehome")

func issue(title, body string) effect.Issue {
	return effect.Issue{Title: title, Target: target, Body: hostile.New(body)}
}

func file(t *testing.T, tw *taskwarrior.Taskwarrior, key, title, body string) {
	t.Helper()
	if err := tw.File(context.Background(), key, issue(title, body)); err != nil {
		t.Fatal(err)
	}
}

func synced(t *testing.T, tw *taskwarrior.Taskwarrior) []taskwarrior.Task {
	t.Helper()
	if err := tw.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks, err := tw.Tasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func withKey(t *testing.T, tasks []taskwarrior.Task, key string) taskwarrior.Task {
	t.Helper()
	i := slices.IndexFunc(tasks, func(k taskwarrior.Task) bool { return k.Key == key })
	if i < 0 {
		t.Fatalf("no task with key %s in %+v", key, tasks)
	}
	return tasks[i]
}

func countKey(tasks []taskwarrior.Task, key string) int {
	n := 0
	for _, k := range tasks {
		if k.Key == key {
			n++
		}
	}
	return n
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestAFilingReachesTheOtherReplicaWithItsShape(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	file(t, container, "run-1:0", "Alerts open on picklehome", "line one\n\n```\nfenced\n```")

	task := withKey(t, synced(t, mac), "run-1:0")
	eq(t, "description", task.Description, "Alerts open on picklehome")
	eq(t, "project", task.Project, "picklehome")
	eq(t, "status", task.Status, "pending")
	eq(t, "tags", strings.Join(task.Tags, ","), "almanac")
	eq(t, "target", task.Target, "task:picklehome")
	eq(t, "annotations", len(task.Annotations), 1)
	eq(t, "annotation", task.Annotations[0].Description, "almanac run-1:0\nline one\n\n```\nfenced\n```")
	if task.Entry == "" || task.Modified == "" {
		t.Errorf("entry and modified should be set: %+v", task)
	}
}

func TestATitleThatLooksLikeTaskSyntaxStaysText(t *testing.T) {
	dir := t.TempDir()
	container := open(t, dir, "container")
	file(t, container, "run-1:0", "fix project:evil +urgent due:tomorrow", "b")

	task := withKey(t, synced(t, container), "run-1:0")
	eq(t, "description", task.Description, "fix project:evil +urgent due:tomorrow")
	eq(t, "project", task.Project, "picklehome")
	eq(t, "tags", strings.Join(task.Tags, ","), "almanac")
}

func TestTheSameKeyTwiceFilesOnce(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	for range 2 {
		file(t, container, "run-1:0", "title", "synthetic body")
	}
	tasks := synced(t, mac)
	eq(t, "keyed", countKey(tasks, "run-1:0"), 1)
	eq(t, "on the mac", len(tasks), 1)
	eq(t, "in the container", len(synced(t, container)), 1)
}

func TestADifferentKeyAddsExactlyOne(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	for _, key := range []string{"run-1:0", "run-1:1"} {
		file(t, container, key, "title", "synthetic body")
	}
	tasks := synced(t, mac)
	eq(t, "tasks", len(tasks), 2)
	withKey(t, tasks, "run-1:0")
	withKey(t, tasks, "run-1:1")
}

// The lost-volume case: the container's first sync learns of the task before it looks for it.
func TestATaskTheMacAlreadyHasIsNotFiledAgain(t *testing.T) {
	dir := t.TempDir()
	mac := open(t, dir, "mac")
	file(t, mac, "run-9:0", "title", "synthetic body")
	container := open(t, dir, "container")
	file(t, container, "run-9:0", "title", "synthetic body")
	eq(t, "keyed", countKey(synced(t, mac), "run-9:0"), 1)
}

func TestALongBodyLandsVerbatim(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	long := strings.Repeat("`a``b```c\n", 400)
	eq(t, "length", len(long), 4000)
	file(t, container, "run-2:0", "Long one", long)

	task := withKey(t, synced(t, mac), "run-2:0")
	eq(t, "annotation", task.Annotations[0].Description, "almanac run-2:0\n"+long)
	eq(t, "description", task.Description, "Long one")
}

// server is a regular file, so the local server fails to open or sync.
func TestAnUnreachableServerWritesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	container := open(t, dir, "container")
	err := container.File(context.Background(), "run-1:0", issue("title", "synthetic body"))
	if err == nil {
		t.Fatal("filing should fail")
	}
	if strings.Contains(err.Error(), "synthetic body") {
		t.Errorf("the error carries the body: %v", err)
	}
	tasks, err := container.Tasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "tasks", len(tasks), 0)
}

func TestAnAskIsAQuestionTaskWithItsPull(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	q := effect.Question{Question: "Merge this?", Target: target, Pull: 42, Body: hostile.New("why")}
	if err := container.Ask(context.Background(), "run-2:0", q); err != nil {
		t.Fatal(err)
	}
	task := withKey(t, synced(t, mac), "run-2:0")
	eq(t, "description", task.Description, "Merge this?")
	eq(t, "tags", strings.Join(task.Tags, ","), "almanac,question")
	eq(t, "pull", task.Pull, "42")
}

func TestAnAskWithoutAPullHasNoPullAttribute(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	q := effect.Question{Question: "Anything else?", Target: target, Body: hostile.New("why")}
	if err := container.Ask(context.Background(), "run-3:0", q); err != nil {
		t.Fatal(err)
	}
	task := withKey(t, synced(t, mac), "run-3:0")
	eq(t, "tags", strings.Join(task.Tags, ","), "almanac,question")
	eq(t, "pull", task.Pull, "")
}

func TestATargetThatIsNotATaskIsRefused(t *testing.T) {
	dir := t.TempDir()
	container := open(t, dir, "container")
	err := container.File(context.Background(), "run-1:0",
		effect.Issue{Title: "t", Target: hostile.MustParseSlug("technicalpickles/picklehome"), Body: hostile.New("b")})
	if err == nil || !strings.Contains(err.Error(), "is not a task: target") {
		t.Fatalf("got %v", err)
	}
}

func checker(dir string) taskwarrior.Config {
	return taskwarrior.Config{DataDir: filepath.Join(dir, "check"), Server: local(dir)}
}

func TestASyncCheckPullsTheExistingHistoryIntoAFreshReplica(t *testing.T) {
	dir := t.TempDir()
	mac := open(t, dir, "mac")
	file(t, mac, "run-1:0", "Alerts open", "b")
	file(t, mac, "run-1:1", "More alerts", "b")
	n, err := taskwarrior.SyncCheck(context.Background(), checker(dir))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "pulled", n, 2)
}

func TestASyncCheckWritesNothingToTheServer(t *testing.T) {
	dir := t.TempDir()
	mac := open(t, dir, "mac")
	file(t, mac, "run-1:0", "Alerts open", "b")
	if _, err := taskwarrior.SyncCheck(context.Background(), checker(dir)); err != nil {
		t.Fatal(err)
	}
	eq(t, "tasks", len(synced(t, open(t, dir, "other"))), 1)
}

func TestASyncCheckAgainstAnEmptyHistoryFailsBecauseNothingWasDecrypted(t *testing.T) {
	needTask(t)
	_, err := taskwarrior.SyncCheck(context.Background(), checker(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
}

func TestASyncCheckAgainstAnUnreachableServerFails(t *testing.T) {
	needTask(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskwarrior.SyncCheck(context.Background(), checker(dir)); err == nil {
		t.Fatal("a sync check against a file should fail")
	}
}

// KNOWN FAILING, pins steading-ai/almanac#34 and brined-almanac#43. The library keys an effect
// "{run_id}:{i}" from the run_seq sequence, so a store reset or restore reissues "run-1:0" for a
// different finding. The tracker then sees a task already carrying that key and drops the new
// finding without an error. Passes once keys stay unique for the life of the tracker.
func TestAReusedKeyForADifferentFindingIsStillFiled(t *testing.T) {
	dir := t.TempDir()
	container, mac := open(t, dir, "container"), open(t, dir, "mac")
	file(t, container, "run-1:0", "Alerts open on picklehome", "first database, first finding")
	file(t, container, "run-1:0", "Disk full on picklehome", "fresh database after a reset, a different finding")
	titles := []string{}
	for _, task := range synced(t, mac) {
		titles = append(titles, task.Description)
	}
	if !slices.Contains(titles, "Disk full on picklehome") {
		t.Fatalf("the second finding never reached the replica, tasks: %q", titles)
	}
}
