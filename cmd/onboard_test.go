package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/session"
)

func TestFirstRunOnboarding(t *testing.T) {
	runRepository(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Seed host configs and credentials.
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "skills", "demo", "SKILL.md"), []byte("demo skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g mouse on\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
if [ "$1 $2" = "volume inspect" ]; then printf 'Error: No such volume\n' >&2; exit 1; fi
case "$2" in
create) printf '%s\n' '` + containerID + `' ;;
exec) case "$*" in
 *attach-session*) printf 'workspace attached' ;;
esac ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	// 1. Cancelling onboarding with 'q' does not mark onboarded.
	var stdout, stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetIn(fakeTTYReader{strings.NewReader("q")})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "onboarding cancelled") {
		t.Fatalf("cancelled onboarding error = %v", err)
	}
	dataDir := filepath.Join(home, ".hewitt")
	if profile.IsOnboarded(dataDir) {
		t.Fatal("cancelled onboarding marked dataDir onboarded")
	}

	// 2. Complete onboarding via bare `hewitt`:
	// Step 1: Enter (1 = Yes, import configs)
	// Step 2: Enter (1 = Yes, Git worktree)
	// Step 3: Enter (1 = Yes, copy host credentials)
	stdout.Reset()
	stderr.Reset()
	root = NewRootCommand("test")
	root.SetIn(fakeTTYReader{strings.NewReader("\n\n\n")})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("onboarding execute: %v", err)
	}
	if !profile.IsOnboarded(dataDir) {
		t.Fatal("expected dataDir to be marked onboarded")
	}
	if stdout.String() != "workspace attached" {
		t.Fatalf("stdout = %q, want workspace attached", stdout.String())
	}
	for _, want := range []string{
		"[1/3] Import configs",
		"Imported configs: claude, tmux",
		"[2/3] Create new workspace?",
		"[3/3] Share credentials?",
		"Shared credentials: claude",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want containing %q", stderr.String(), want)
		}
	}

	// Verify imported files in profile.
	for _, rel := range []string{
		"profiles/claude/default/claude/skills/demo/SKILL.md",
		"profiles/claude/default/claude/.credentials.json",
		"profiles/claude/default/home/.tmux.conf",
	} {
		if _, err := os.Stat(filepath.Join(dataDir, rel)); err != nil {
			t.Errorf("missing imported file %s: %v", rel, err)
		}
	}

	// Verify session was created with a volume workspace.
	store, err := session.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Get(containerID)
	if err != nil || m.Volume == nil || m.Workspace != nil {
		t.Fatalf("session metadata = %+v, %v", m, err)
	}

	// 3. Subsequent interactive run of bare `hewitt` does not re-run onboarding.
	stdout.Reset()
	stderr.Reset()
	root = NewRootCommand("test")
	root.SetIn(fakeTTYReader{strings.NewReader("")})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "[1/3]") || !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("second run re-triggered onboarding: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestOnboardingFinishWithoutWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)

	// Step 1: 2 (No configs), Step 2: 3 (No workspace), Step 3: 3 (Skip credentials)
	var stdout, stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetIn(fakeTTYReader{strings.NewReader("2\n3\n3\n")})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("onboarding without workspace: %v", err)
	}
	if !profile.IsOnboarded(filepath.Join(home, ".hewitt")) {
		t.Fatal("expected dataDir to be marked onboarded")
	}
	if !strings.Contains(stderr.String(), "Onboarding complete.") {
		t.Fatalf("stderr = %q, want Onboarding complete", stderr.String())
	}
}
