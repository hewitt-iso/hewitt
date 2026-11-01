package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
)

func TestLoginUsesSelectedConfigAndProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
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
wait) printf '0\n' ;;
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
		{name: "success", wantCalls: "create\nstart\n"},
		{name: "create failure", failure: "create", wantError: "create container", wantCalls: "create\n"},
		{name: "start failure", failure: "start", wantError: "container preserved", wantCalls: "create\nstart\n"},
		{name: "custom data directory", customDir: true, wantCalls: "create\nstart\n"},
		{name: "metadata failure", customDir: true, blockedDir: true, wantError: "startup not attempted", wantCalls: "create\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
			t.Setenv("PATH", dir)
			t.Setenv("HEWITT_TEST_FAILURE", tt.failure)
			dataDir := filepath.Join(dir, ".hewitt")
			args := []string{"run", "codex", "--image", "my-codex:local", "--name", "hewitt-test"}
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
			got, err := os.ReadFile(calls)
			if err != nil || string(got) != tt.wantCalls {
				t.Fatalf("Docker calls = %q, %v; want %q", got, err, tt.wantCalls)
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
			} else if err != nil || m.ID != containerID || m.Agent != "codex" || m.Image != "my-codex:local" || m.Name != "hewitt-test" || m.CreatedAt.Before(before) || m.CreatedAt.After(time.Now()) {
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
		{args: []string{"run"}, want: "accepts 1 arg(s)"},
		{args: []string{"run", "codex", "extra"}, want: "accepts 1 arg(s)"},
		{args: []string{"run", "codex"}, want: `required flag(s) "image"`},
		{args: []string{"run", "codex", "--image", ""}, want: "image must be nonempty"},
		{args: []string{"run", "codex", "--image=--privileged"}, want: "image must be nonempty"},
		{args: []string{"run", "codex", "--image", "test", "--name", "bad name"}, want: "container name must be nonempty"},
		{args: []string{"run", "codex", "--image", "test", "--profile", "../escape"}, want: "profile must"},
		{args: []string{"run", "opencode", "--image", "test"}, want: "no implemented adapter"},
		{args: []string{"login", "codex"}, want: `required flag(s) "image"`},
		{args: []string{"login", "claude", "--image", "test", "--profile", ""}, want: "profile must"},
		{args: []string{"run", "codex", "--image", "test"}, want: "Docker CLI is installed"},
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
	for _, want := range []string{"--image", "--name", "Examples:", "no repository or host credentials are imported"} {
		if !strings.Contains(out, want) {
			t.Errorf("help = %q, want %q", out, want)
		}
	}
	if _, err := executeCommand(t, "login", "--help", "--config", filepath.Join(home, "missing.json")); err != nil {
		t.Fatalf("login help requires configuration: %v", err)
	}
}
