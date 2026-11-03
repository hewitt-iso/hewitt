package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/session"
)

func TestSessionCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	for _, tt := range []struct {
		name string
		arg  string
	}{
		{name: "logs", arg: "session-id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{tt.name}
			if tt.arg != "" {
				args = append(args, tt.arg)
			}
			t.Run("execution", func(t *testing.T) {
				out, err := executeCommand(t, args...)
				want := "hewitt " + tt.name + " is not implemented yet"
				if err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
				if out != "" {
					t.Errorf("unexpected stdout: %q", out)
				}
			})
			t.Run("help", func(t *testing.T) {
				// No required argument or valid config should be needed for help.
				out, err := executeCommand(t, tt.name, "--help", "--config", filepath.Join(home, "missing.json"))
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"hewitt " + tt.name, "Not implemented yet", "Examples:", "--config"} {
					if !strings.Contains(out, want) {
						t.Errorf("help = %q, want containing %q", out, want)
					}
				}
			})
			t.Run("extra argument", func(t *testing.T) {
				_, err := executeCommand(t, append(args, "extra")...)
				if err == nil || strings.Contains(err.Error(), "not implemented") {
					t.Fatalf("expected argument validation error, got %v", err)
				}
			})
			if tt.arg != "" {
				t.Run("missing argument", func(t *testing.T) {
					_, err := executeCommand(t, tt.name)
					if err == nil || !strings.Contains(err.Error(), "accepts 1 arg(s), received 0") {
						t.Fatalf("expected missing argument error, got %v", err)
					}
				})
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("commands created unexpected files: %v", entries)
	}
}

func TestSessionCommandInputs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	broken := filepath.Join(home, "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "empty image override", args: []string{"run", "claude", "--image", ""}, want: "image must be nonempty"},
		{name: "opencode requires image", args: []string{"run", "opencode"}, want: "select an image"},
		{name: "unsupported agent", args: []string{"run", "unknown"}, want: "invalid argument"},
		{name: "empty agent", args: []string{"run", ""}, want: "invalid argument"},
		{name: "unknown flag", args: []string{"logs", "session-id", "--unknown"}, want: "unknown flag"},
		{name: "config before command", args: []string{"--config", broken, "ps"}, want: "parse config"},
		{name: "config after command", args: []string{"ps", "--config", broken}, want: "parse config"},
		{name: "missing explicit config", args: []string{"ps", "--config", filepath.Join(home, "missing.json")}, want: "read config"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := executeCommand(t, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
			if out != "" {
				t.Errorf("unexpected stdout: %q", out)
			}
		})
	}
	for _, name := range []string{"attach", "stop", "rm", "logs", "diff"} {
		for _, arg := range []string{"", " \t "} {
			_, err := executeCommand(t, name, arg)
			if err == nil || err.Error() != "session must not be empty" {
				t.Errorf("%s with %q: expected empty session error, got %v", name, arg, err)
			}
		}
	}
	out, err := executeCommand(t, "--config", broken, "help", "run")
	if err != nil || !strings.Contains(out, "Planned agents:") {
		t.Fatalf("help run with broken config: output = %q, error = %v", out, err)
	}
}

func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	if stderr.Len() != 0 {
		t.Errorf("command printed diagnostics instead of returning them: %q", stderr.String())
	}
	return stdout.String(), err
}

func TestStopAndRemoveWithShortIDAndName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	script := `#!/bin/sh
case "$2" in
stop) exit 0 ;;
inspect) printf 'false\n' ;;
rm) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(filepath.Join(home, ".hewitt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{containerID[:12], "named-session"} {
		m := session.Metadata{
			ID: containerID, Agent: "codex", Image: "codex:local",
			Name: "named-session", CreatedAt: time.Now().UTC(),
		}
		if err := store.Save(m); err != nil {
			t.Fatal(err)
		}
		out, err := executeCommand(t, "stop", ref)
		if err != nil || out != containerID+"\n" {
			t.Fatalf("stop %q = %q, %v; want %s", ref, out, err, containerID)
		}
		out, err = executeCommand(t, "rm", ref)
		if err != nil || out != containerID+"\n" {
			t.Fatalf("rm %q = %q, %v; want %s", ref, out, err, containerID)
		}
	}
}
