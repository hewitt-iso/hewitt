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
		{name: "unsupported agent", args: []string{"run", "unknown"}, want: "unknown agent"},
		{name: "empty agent", args: []string{"run", ""}, want: "unknown agent"},
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
	for _, name := range []string{"attach", "stop", "rm", "logs"} {
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
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if len(args) > 0 && (args[0] == "run" || args[0] == "auth") && strings.HasPrefix(line, "[hewitt] ") {
			continue
		}
		if line != "" {
			t.Errorf("command printed diagnostics instead of returning them: %q", stderr.String())
			break
		}
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

type fakeTTYReader struct {
	*strings.Reader
}

func (fakeTTYReader) isTerminal() bool { return true }

func TestInteractiveSessionPicker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)

	secondID := strings.Repeat("b", 64)
	script := `#!/bin/sh
case "$2" in
inspect)
  if [ "$6" = "` + containerID + `" ]; then printf 'true\n'; else printf 'false\n'; fi
  ;;
stop) exit 0 ;;
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
	first := session.Metadata{
		ID: containerID, Agent: "codex", Image: "codex:local",
		Name: "first-session", CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
	}
	second := session.Metadata{
		ID: secondID, Agent: "claude", Image: "claude:local",
		Name: "second-session", CreatedAt: time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC),
	}
	for _, m := range []session.Metadata{first, second} {
		if err := store.Save(m); err != nil {
			t.Fatal(err)
		}
	}

	// Select running session with Enter in stop (only first is running).
	var stdout, stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetIn(fakeTTYReader{strings.NewReader("\n")})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"stop"})
	if err := root.Execute(); err != nil {
		t.Fatalf("interactive stop: %v", err)
	}
	if stdout.String() != containerID+"\n" {
		t.Fatalf("interactive stop stdout = %q, want %s", stdout.String(), containerID)
	}
	if !strings.Contains(stderr.String(), containerID[:12]) || !strings.Contains(stderr.String(), "first-session") {
		t.Fatalf("interactive stop picker stderr = %q", stderr.String())
	}

	// Remove stopped session using arrow-down / number selection when multiple candidates exist.
	// Without docker on PATH, both sessions have status "unknown" and appear in picker.
	t.Setenv("PATH", t.TempDir())
	for _, input := range []string{"\x1b[B\r", "j\n", "2\n"} {
		stdout.Reset()
		stderr.Reset()
		root = NewRootCommand("test")
		root.SetIn(fakeTTYReader{strings.NewReader(input)})
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"stop"})
		err = root.Execute()
		// Docker is absent so stop fails after picking secondID; verify secondID was selected.
		if err == nil || !strings.Contains(err.Error(), secondID) {
			t.Fatalf("input %q: error = %v, want stop attempt for %s", input, err, secondID)
		}
	}

	// Cancellation via 'q' or Esc returns a cancellation error without side effects.
	for _, input := range []string{"q", "\x1b"} {
		stdout.Reset()
		stderr.Reset()
		root = NewRootCommand("test")
		root.SetIn(fakeTTYReader{strings.NewReader(input)})
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"stop"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("input %q: error = %v, want cancelled", input, err)
		}
	}
}

func TestSessionCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)

	// Completion script generation must work even when config is broken.
	broken := filepath.Join(home, "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand(t, "completion", "bash", "--config", broken)
	if err != nil || !strings.Contains(out, "bash completion") {
		t.Fatalf("completion bash = %q, %v", out, err)
	}

	store, err := session.NewStore(filepath.Join(home, ".hewitt"))
	if err != nil {
		t.Fatal(err)
	}
	m := session.Metadata{
		ID: containerID, Agent: "codex", Image: "codex:local",
		Name: "my-codex", CreatedAt: time.Now().UTC(),
	}
	if err := store.Save(m); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"__complete", "attach", ""})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "my-codex") || !strings.Contains(stdout.String(), containerID[:12]) {
		t.Fatalf("__complete attach = %q", stdout.String())
	}
}
