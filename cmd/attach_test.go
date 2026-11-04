package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/hewitt-iso/hewitt/workspace"
)

func TestAttach(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	dataDir := filepath.Join(home, "custom-data")
	store, err := session.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	m := session.Metadata{Terminal: "tmux", ID: containerID, Agent: "codex", Image: "codex:local", Name: "my-session", CreatedAt: time.Now().UTC()}
	if err := store.Save(m); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "sessions", containerID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(config.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\"\nprintf 'container stderr\\n' >&2\n"
	if err := os.WriteFile(filepath.Join(home, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{containerID, containerID[:12], "my-session"} {
		var out, diagnostics bytes.Buffer
		root := NewRootCommand("test")
		root.SetIn(strings.NewReader("agent input\n"))
		root.SetOut(&out)
		root.SetErr(&diagnostics)
		root.SetArgs([]string{"attach", ref, "--config", configPath})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "agent input\n" || diagnostics.String() != "container stderr\n" {
			t.Fatalf("streams = %q, %q", out.String(), diagnostics.String())
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("metadata changed: %v", err)
	}
}

func TestAttachLookupAndHelp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	for _, args := range [][]string{{"attach"}, {"attach", containerID, "extra"}, {"attach", "--privileged"}, {"attach", "short-id"}, {"attach", containerID}} {
		out, err := executeCommand(t, args...)
		if err == nil || out != "" || strings.Contains(err.Error(), "Docker CLI") {
			t.Fatalf("%q: output %q, error %v", args, out, err)
		}
	}
	out, err := executeCommand(t, "attach", "--help", "--config", filepath.Join(home, "missing.json"))
	if err != nil || !strings.Contains(out, "hewitt attach <session>") || strings.Contains(out, "not implemented") {
		t.Fatalf("help = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".hewitt")); !os.IsNotExist(err) {
		t.Fatalf("lookup created files: %v", err)
	}
}

func TestAttachSelectsIndividualWorkspaceAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	t.Setenv("TMUX", "/tmp/test-host,123,0")
	t.Setenv("TMUX_PANE", "%1")
	store, err := session.NewStore(filepath.Join(home, ".hewitt"))
	if err != nil {
		t.Fatal(err)
	}
	m := session.Metadata{ID: containerID, Name: "project", Agent: "workspace", Image: "test", CreatedAt: time.Now().UTC()}
	m.Mode, m.Terminal, m.WorkspaceKey = "new", "host", strings.Repeat("a", 64)
	m.Volume = &workspace.Volume{Name: "project", Volume: "hewitt-project", Repository: home, Objects: filepath.Join(home, "objects"), Path: "/hewitt/work/project", Base: strings.Repeat("a", 40), Branch: "main"}
	if err := store.Save(m); err != nil {
		t.Fatal(err)
	}
	// RecordRun uses the workspace identity; the records own no containers.
	first, release, err := store.RecordRun(m, "codex")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	t.Setenv("TMUX_PANE", "%2")
	second, releaseSecond, err := store.RecordRun(m, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	log := filepath.Join(home, "tmux-calls")
	t.Setenv("ATTACH_TEST_LOG", log)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$ATTACH_TEST_LOG"
case "$3" in display-message) printf '$1\t@2\n' ;; esac
`
	if err := os.WriteFile(filepath.Join(home, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand("test")
	var out, diagnostics bytes.Buffer
	root.SetIn(fakeTTYReader{strings.NewReader("2\n")})
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"attach"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Select a running agent", "codex", "claude", "project"} {
		if !strings.Contains(diagnostics.String(), want) {
			t.Fatalf("missing %q: %s", want, diagnostics.String())
		}
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", out.String())
	}
	runs, err := store.RunningAgents()
	if err != nil {
		t.Fatal(err)
	}
	selected := runs[1]
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "select-pane -t "+selected.Terminal.Pane) {
		t.Fatalf("wrong agent selected: %s", raw)
	}
	// Direct short IDs work without a picker or Docker executable.
	root = NewRootCommand("test")
	root.SetIn(strings.NewReader(""))
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"attach", first.ID[:12]})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	root = NewRootCommand("test")
	root.SetIn(fakeTTYReader{strings.NewReader("\n")})
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"attach", "project"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("two agents share a run ID")
	}
}
