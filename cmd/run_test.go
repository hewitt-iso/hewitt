package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
)

func TestLoginUsesSelectedConfigAndProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	data := filepath.Join(dir, "custom-data")
	configPath := filepath.Join(dir, "config.json")
	encoded, err := json.Marshal(map[string]string{"data_dir": data})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$2" in
create) printf '%s\n' '` + containerID + `' ;;
start) printf 'native login' ;;
inspect) printf '%s\n' '{"Status":"exited","ExitCode":0}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"login", "codex", "--config", configPath, "--image", "codex:test", "--profile", "work"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "native login" {
		t.Fatalf("output %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(data, "profiles", "codex", "work", "codex")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), containerID) {
		t.Fatalf("missing container ID: %q", stderr.String())
	}
}

const containerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRun(t *testing.T) {
	for _, tt := range []struct {
		name       string
		failure    string
		wantError  string
		wantCalls  string
		customDir  bool
		blockedDir bool
	}{
		{name: "success", wantCalls: "create\nstart\nexec\nexec\nexec\n"},
		{name: "create failure", failure: "create", wantError: "create container", wantCalls: "create\n"},
		{name: "start failure", failure: "start", wantError: "container preserved", wantCalls: "create\nstart\n"},
		{name: "custom data directory", customDir: true, wantCalls: "create\nstart\nexec\nexec\nexec\n"},
		{name: "metadata failure", customDir: true, blockedDir: true, wantError: "startup not attempted", wantCalls: "create\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runRepository(t)
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("HEWITT_TEST_FAILURE", tt.failure)
			dataDir := filepath.Join(dir, ".hewitt")
			args := []string{"run", "codex", "--detach", "--image", "my-codex:local", "--name", "hewitt-test", "--repo", ".", "--base", "HEAD"}
			if tt.customDir {
				dataDir = filepath.Join(dir, "custom-data")
				cfg, err := json.Marshal(config.Config{DataDir: dataDir})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "config.json")
				if err := os.WriteFile(path, cfg, 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--config", path)
			}
			if tt.blockedDir {
				if err := os.MkdirAll(dataDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dataDir, "sessions"), []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HEWITT_TEST_METADATA", filepath.Join(dataDir, "sessions", containerID+".json"))
			calls := filepath.Join(dir, "calls")
			t.Setenv("HEWITT_TEST_CALLS", calls)
			script := `#!/bin/sh
printf '%s\n' "$2" >> "$HEWITT_TEST_CALLS"
if [ "$2" = start ] && [ ! -f "$HEWITT_TEST_METADATA" ]; then
  printf 'metadata must be saved before start\n' >&2
  exit 1
fi
if [ "$2" = "$HEWITT_TEST_FAILURE" ]; then
  printf 'test Docker failure\n' >&2
  exit 1
fi
printf '%s\n' '` + containerID + `'
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			out, err := executeCommand(t, args...)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) || out != "" {
					t.Fatalf("output = %q, error = %v, want %q", out, err, tt.wantError)
				}
				if (tt.failure == "start" || tt.blockedDir) && !strings.Contains(err.Error(), containerID) {
					t.Errorf("start failure did not identify retained container: %v", err)
				}
			} else if err != nil || out != containerID+"\n" {
				t.Fatalf("output = %q, error = %v", out, err)
			}
			commandErr := err
			got, err := os.ReadFile(calls)
			if err != nil || string(got) != tt.wantCalls {
				t.Fatalf("Docker calls = %q, %v; want %q", got, err, tt.wantCalls)
			}
			worktrees, workErr := os.ReadDir(filepath.Join(dataDir, "worktrees"))
			if workErr != nil || len(worktrees) != 1 {
				t.Fatalf("worktree not preserved: %v, %v", worktrees, workErr)
			}
			if tt.wantError != "" && !strings.Contains(commandErr.Error(), filepath.Join(dataDir, "worktrees")) {
				t.Fatalf("failure did not identify worktree: %v", commandErr)
			}
			if tt.blockedDir {
				got, err := os.ReadFile(filepath.Join(dataDir, "sessions"))
				if err != nil || string(got) != "untouched" {
					t.Fatalf("metadata failure changed existing file: %q, %v", got, err)
				}
				return
			}
			store, err := session.NewStore(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			m, err := store.Get(containerID)
			if tt.failure == "create" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("create failure wrote metadata: %+v, %v", m, err)
				}
			} else if err != nil || m.Terminal != "tmux" || m.ID != containerID || m.Agent != "codex" || m.Image != "my-codex:local" || m.Name != "hewitt-test" || m.CreatedAt.Before(before) || m.CreatedAt.After(time.Now()) {
				t.Fatalf("saved metadata = %+v, %v", m, err)
			}
		})
	}
}

func TestRunValidationAndHelpWithoutDocker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{args: []string{"run"}, want: "select an agent"},
		{args: []string{"run", "codex", "extra"}, want: "accepts at most 1 arg(s)"},
		{args: []string{"run", "codex"}, want: "select an image"},
		{args: []string{"run", "codex", "--image", ""}, want: "select an image"},
		{args: []string{"run", "codex", "--image=--privileged"}, want: "image must be nonempty"},
		{args: []string{"run", "codex", "--image", "test", "--name", "bad name"}, want: "container name must be nonempty"},
		{args: []string{"run", "codex", "--image", "test", "--profile", "../escape"}, want: "profile must"},
		{args: []string{"run", "gemini", "--image", "test"}, want: "no implemented adapter"},
		{args: []string{"login", "codex"}, want: `required flag(s) "image"`},
		{args: []string{"login", "claude", "--image", "test", "--profile", ""}, want: "profile must"},
		{args: []string{"run", "codex", "--image", "test"}, want: "locate repository"},
	} {
		out, err := executeCommand(t, tt.args...)
		if err == nil || !strings.Contains(err.Error(), tt.want) || out != "" {
			t.Errorf("%q: output = %q, error = %v; want %q", tt.args, out, err, tt.want)
		}
	}
	out, err := executeCommand(t, "run", "--help", "--config", filepath.Join(home, "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--image", "--name", "Examples:", "no host credentials are imported"} {
		if !strings.Contains(out, want) {
			t.Errorf("help = %q, want %q", out, want)
		}
	}
	if _, err := executeCommand(t, "login", "--help", "--config", filepath.Join(home, "missing.json")); err != nil {
		t.Fatalf("login help requires configuration: %v", err)
	}
}

func TestRunAttachesByDefault(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			runRepository(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))
			script := `#!/bin/sh
case "$2" in
create) printf '%s\n' '` + containerID + `' ;;
exec) case "$*" in
 *attach-session*) printf 'existing agent screen'; `
			if failure {
				script += "exit 17"
			}
			script += ` ;;
esac ;;
esac
`
			if err := os.WriteFile(filepath.Join(home, "docker"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			root := NewRootCommand("test")
			root.SetOut(&out)
			root.SetErr(&diagnostics)
			root.SetIn(strings.NewReader(""))
			root.SetArgs([]string{"run", "codex", "--image", "agent:test"})
			err := root.Execute()
			if (err != nil) != failure {
				t.Fatalf("error = %v", err)
			}
			if out.String() != "existing agent screen" || !strings.Contains(diagnostics.String(), containerID) {
				t.Fatalf("streams %q %q", out.String(), diagnostics.String())
			}
			store, err := session.NewStore(filepath.Join(home, ".hewitt"))
			if err != nil {
				t.Fatal(err)
			}
			if m, err := store.Get(containerID); err != nil || m.Terminal != "tmux" {
				t.Fatalf("preserved record %+v, %v", m, err)
			}
		})
	}
}

func runRepository(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prepare repository: %s: %v", out, err)
		}
	}
	t.Chdir(dir)
}
