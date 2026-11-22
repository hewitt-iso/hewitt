package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/session"
)

func TestReadTasks(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("Fix Flaky_Test.md", "---\nagent: codex\n---\n\nFix the flaky test.\n")
	write("docs.md", "Update the docs.\n")
	write("notes.txt", "not a task")
	tasks, err := readTasks([]string{dir}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].name != "fix-flaky-test" || tasks[0].agent != "codex" || tasks[0].prompt != "Fix the flaky test." ||
		tasks[1].name != "docs" || tasks[1].agent != "claude" || tasks[1].prompt != "Update the docs." {
		t.Fatalf("tasks = %+v", tasks)
	}
	for _, tt := range []struct{ content, want string }{
		{"---\nagent: Bad_Agent\n---\nx", "is not an agent name"},
		{"---\nmodel: x\n---\nx", "front matter"},
		{"---\nagent: codex\nx", "no closing ---"},
		{"---\nagent: codex\n---\n", "has no prompt"},
	} {
		path := write("bad.md", tt.content)
		if _, err := readTasks([]string{path}, ""); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: error = %v, want %q", tt.content, err, tt.want)
		}
	}
	if err := os.Remove(filepath.Join(dir, "bad.md")); err != nil {
		t.Fatal(err)
	}
	write("fix-flaky-test.md", "again")
	if _, err := readTasks([]string{dir}, ""); err == nil || !strings.Contains(err.Error(), "both name branch fix-flaky-test") {
		t.Fatalf("duplicate names: %v", err)
	}
	if _, err := readTasks([]string{t.TempDir()}, ""); err == nil || !strings.Contains(err.Error(), "no task files") {
		t.Fatalf("empty directory: %v", err)
	}
}

func TestBatchRunsTasksOnTheirBranches(t *testing.T) {
	runRepository(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "")
	if err := profile.MarkOnboardedNoSync(filepath.Join(home, ".hewitt")); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(home, "calls")
	t.Setenv("HEWITT_TEST_CALLS", calls)
	// The fake agent prints the branch line of hewitt-agent; the task named
	// broken fails.
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_TEST_CALLS"
case "$1 $2" in
"volume inspect") printf 'No such volume' >&2; exit 1 ;;
"container create") printf '` + containerID + `\n' ;;
"container inspect") printf 'true\n' ;;
"container exec")
	for arg; do
		case "$arg" in HEWITT_TASK=*) task=${arg#HEWITT_TASK=} ;; esac
	done
	printf '[hewitt] Branch %s-1\n' "$task" >&2
	printf 'result of %s\n' "$task"
	[ "$task" != broken ] ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("tasks", 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"fix-test.md": "---\nagent: codex\n---\nFix it.", "broken.md": "Break it.", "docs.md": "Write docs."} {
		if err := os.WriteFile(filepath.Join("tasks", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := NewRootCommand("test")
	var out, diagnostic bytes.Buffer
	root.SetArgs([]string{"batch", "--agent", "claude", "-j", "2"})
	root.SetOut(&out)
	root.SetErr(&diagnostic)
	err := root.Execute()
	var batchErr *BatchError
	if !errors.As(err, &batchErr) || batchErr.Failed != 1 || batchErr.Total != 3 {
		t.Fatalf("error = %v\n%s", err, diagnostic.String())
	}
	for _, want := range []string{"3 tasks in workspace", "fix-test  codex     branch fix-test-1", "docs      claude    done in", "broken    claude    failed in"} {
		if !strings.Contains(diagnostic.String(), want) {
			t.Errorf("missing %q:\n%s", want, diagnostic.String())
		}
	}
	raw, _ := os.ReadFile(calls)
	if strings.Count(string(raw), "HEWITT_LAUNCH=") != 3 || !strings.Contains(string(raw), "/hewitt/agent/hewitt-agent-") || !strings.Contains(string(raw), " run codex") {
		t.Fatalf("calls:\n%s", raw)
	}

	store, err := session.NewStore(filepath.Join(home, ".hewitt"))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := store.Batches()
	if err != nil || len(ids) != 1 {
		t.Fatalf("batches = %v, %v", ids, err)
	}
	tasks, live, err := store.BatchTasks(ids[0])
	if err != nil || live || len(tasks) != 3 {
		t.Fatalf("tasks = %+v, live %v, %v", tasks, live, err)
	}
	for _, task := range tasks {
		want := session.TaskDone
		if task.Name == "broken" {
			want = session.TaskFailed
		}
		if task.State != want || task.Branch != task.Name+"-1" || task.Run == "" {
			t.Errorf("task = %+v", task)
		}
	}

	listing, err := executeCommand(t, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Batch " + ids[0], "fix-test  codex   fix-test-1  done", "broken    claude  broken-1    failed (exit 1)"} {
		if !strings.Contains(listing, want) {
			t.Errorf("missing %q:\n%s", want, listing)
		}
	}
	log, err := executeCommand(t, "tasks", ids[0], "docs")
	if err != nil || !strings.Contains(log, "result of docs") {
		t.Fatalf("log = %q, %v", log, err)
	}
}
