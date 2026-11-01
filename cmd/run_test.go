package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const containerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRun(t *testing.T) {
	for _, tt := range []struct {
		name      string
		failure   string
		wantError string
		wantCalls string
	}{
		{name: "success", wantCalls: "create\nstart\n"},
		{name: "create failure", failure: "create", wantError: "create container", wantCalls: "create\n"},
		{name: "start failure", failure: "start", wantError: "container preserved", wantCalls: "create\nstart\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
			t.Setenv("PATH", dir)
			t.Setenv("HEWITT_TEST_FAILURE", tt.failure)
			calls := filepath.Join(dir, "calls")
			t.Setenv("HEWITT_TEST_CALLS", calls)
			script := `#!/bin/sh
printf '%s\n' "$2" >> "$HEWITT_TEST_CALLS"
if [ "$2" = "$HEWITT_TEST_FAILURE" ]; then
  printf 'test Docker failure\n' >&2
  exit 1
fi
printf '%s\n' '` + containerID + `'
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			out, err := executeCommand(t, "run", "codex", "--image", "my-codex:local", "--name", "hewitt-test")
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) || out != "" {
					t.Fatalf("output = %q, error = %v, want %q", out, err, tt.wantError)
				}
				if tt.failure == "start" && !strings.Contains(err.Error(), containerID) {
					t.Errorf("start failure did not identify retained container: %v", err)
				}
			} else if err != nil || out != containerID+"\n" {
				t.Fatalf("output = %q, error = %v", out, err)
			}
			got, err := os.ReadFile(calls)
			if err != nil || string(got) != tt.wantCalls {
				t.Fatalf("Docker calls = %q, %v; want %q", got, err, tt.wantCalls)
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
	for _, want := range []string{"--image", "--name", "Examples:", "No host files or credentials are mounted"} {
		if !strings.Contains(out, want) {
			t.Errorf("help = %q, want %q", out, want)
		}
	}
}
