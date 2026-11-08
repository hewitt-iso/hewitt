package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/session"
	"github.com/hewitt-iso/hewitt/workspace"
)

func TestPrintErrorHints(t *testing.T) {
	var out bytes.Buffer
	PrintError(&out, errors.New("plain"))
	if out.String() != "Error: plain\n" {
		t.Fatalf("plain = %q", out.String())
	}
	out.Reset()
	PrintError(&out, &unknownCommandError{Name: "attac", Path: "hewitt", Suggestions: []string{"attach"}})
	if out.String() != "Error: unknown command \"attac\" for \"hewitt\"\nHint: did you mean attach?\n" {
		t.Fatalf("hinted = %q", out.String())
	}
}

func TestMistypedCommandsExplainThemselves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	for _, tt := range []struct {
		args []string
		hint string
	}{
		{[]string{"attac"}, "did you mean attach?"},
		{[]string{"d0fbbd7794bd"}, "hewitt attach d0fbbd7794bd"},
		{[]string{"run", "d0fbbd7794bd"}, "hewitt attach d0fbbd7794bd"},
		{[]string{"stop"}, "hewitt ps lists them"},
		{[]string{"stop", "missing"}, "hewitt ps -a"},
	} {
		_, err := executeCommand(t, tt.args...)
		var out bytes.Buffer
		PrintError(&out, err)
		if err == nil || !strings.Contains(out.String(), "\nHint: ") || !strings.Contains(out.String(), tt.hint) {
			t.Errorf("%q: %s", tt.args, out.String())
		}
	}
}

func TestIdleWorkspaceIsExplained(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	if err := os.WriteFile(filepath.Join(home, "docker"), []byte("#!/bin/sh\nprintf 'true\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(filepath.Join(home, ".hewitt"))
	if err != nil {
		t.Fatal(err)
	}
	m := session.Metadata{ID: containerID, Name: "workspace-1", Agent: "workspace", Image: "test", CreatedAt: time.Now().UTC()}
	m.Mode, m.Terminal, m.WorkspaceKey = "new", "host", strings.Repeat("a", 64)
	m.Volume = &workspace.Volume{Name: "workspace-1", Volume: "hewitt-workspace-1", Repository: home, Objects: filepath.Join(home, "objects"), Path: "/hewitt/work/project", Base: strings.Repeat("a", 40), Branch: "main"}
	if err := store.Save(m); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand(t, "ps")
	if err != nil || !strings.Contains(out, "idle") || strings.Contains(out, " workspace ") {
		t.Fatalf("ps = %q, %v", out, err)
	}
	for _, args := range [][]string{{"attach", containerID[:12]}, {"attach", "workspace-1"}} {
		_, err := executeCommand(t, args...)
		var none *noAgentsError
		if !errors.As(err, &none) || none.Workspace != "workspace-1" || !strings.Contains(none.Hint(), "outside host tmux") {
			t.Fatalf("%q: %v", args, err)
		}
	}
	root := NewRootCommand("test")
	var stdout, stderr bytes.Buffer
	root.SetIn(fakeTTYReader{strings.NewReader("")})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"attach"})
	err = root.Execute()
	var none *noAgentsError
	if !errors.As(err, &none) || len(none.Idle) != 1 || !strings.Contains(err.Error(), "workspace-1 is running without one") {
		t.Fatalf("attach = %v", err)
	}
	t.Setenv("TMUX", "/tmp/test-host,1,0")
	t.Setenv("TMUX_PANE", "%1")
	_, release, err := store.RecordRun(m, "pi")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if out, err := executeCommand(t, "ps"); err != nil || !strings.Contains(out, "pi") || !strings.Contains(out, "running") {
		t.Fatalf("ps with agent = %q, %v", out, err)
	}
}

func TestMissingAgentHintNamesCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := executeCommand(t, "auth", "relogin")
	var missing *missingAgentError
	if !errors.As(err, &missing) || !strings.Contains(missing.Hint(), "hewitt auth relogin codex") {
		t.Fatalf("error = %v", err)
	}
}
