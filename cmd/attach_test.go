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
	m := session.Metadata{ID: containerID, Agent: "codex", Image: "codex:local", CreatedAt: time.Now().UTC()}
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
	var out, diagnostics bytes.Buffer
	root := NewRootCommand("test")
	root.SetIn(strings.NewReader("agent input\n"))
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"attach", containerID, "--config", configPath})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "agent input\n" || diagnostics.String() != "container stderr\n" {
		t.Fatalf("streams = %q, %q", out.String(), diagnostics.String())
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
	if err != nil || !strings.Contains(out, "Ctrl-P") || strings.Contains(out, "not implemented") {
		t.Fatalf("help = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".hewitt")); !os.IsNotExist(err) {
		t.Fatalf("lookup created files: %v", err)
	}
}
