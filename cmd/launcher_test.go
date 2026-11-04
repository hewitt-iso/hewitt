package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/profile"
)

func TestBareLauncherSelectsAgentsAndReusesWorkspace(t *testing.T) {
	runRepository(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := profile.MarkOnboardedNoSync(filepath.Join(home, ".hewitt")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "calls")
	t.Setenv("HEWITT_TEST_CALLS", log)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_TEST_CALLS"
case "$1 $2" in
"volume inspect") printf 'No such volume' >&2; exit 1 ;;
"container create") printf '` + containerID + `\n' ;;
"container inspect") printf 'true\n' ;;
"container exec") printf 'native agent output' ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for i, name := range agent.Names[:2] {
		root := NewRootCommand("test")
		var out, diagnostic bytes.Buffer
		input := "\n"
		if i == 1 {
			input = "2\n"
		}
		root.SetArgs(nil)
		root.SetIn(fakeTTYReader{strings.NewReader(input)})
		root.SetOut(&out)
		root.SetErr(&diagnostic)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "native agent output" {
			t.Fatalf("stdout = %q", out.String())
		}
		for _, want := range []string{"Select agent", "[hewitt] Reading project settings", "[hewitt] Opening " + name + " in workspace"} {
			if !strings.Contains(diagnostic.String(), want) {
				t.Fatalf("missing %q: %s", want, diagnostic.String())
			}
		}
		if i == 0 {
			steps := []string{"Reading project settings", "Preparing agent profiles", "Creating workspace volume", "Preparing volume permissions", "Initializing workspace repository", "Creating workspace container", "Starting workspace container", "Opening " + name}
			position := 0
			for _, step := range steps {
				offset := strings.Index(diagnostic.String()[position:], step)
				if offset < 0 {
					t.Fatalf("missing or out-of-order step %q: %s", step, diagnostic.String())
				}
				position += offset + len(step)
			}
		} else if !strings.Contains(diagnostic.String(), "Reusing workspace") {
			t.Fatalf("workspace not reused: %s", diagnostic.String())
		}
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "container create ") != 1 {
		t.Fatalf("created another container: %s", raw)
	}
	for _, name := range agent.Names[:2] {
		if !strings.Contains(string(raw), "hewitt-agent run "+name) {
			t.Fatalf("wrong launch command: %s", raw)
		}
	}
}

func TestLauncherCancellationBeforeWorkspaceSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home) // Neither Docker nor Git is available.
	data := filepath.Join(home, ".hewitt")
	if err := profile.MarkOnboardedNoSync(data); err != nil {
		t.Fatal(err)
	}
	root := NewRootCommand("test")
	var out, diagnostic bytes.Buffer
	root.SetArgs(nil)
	root.SetIn(fakeTTYReader{strings.NewReader("q")})
	root.SetOut(&out)
	root.SetErr(&diagnostic)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "agent selection cancelled") {
		t.Fatalf("cancel = %v", err)
	}
	if out.Len() != 0 || strings.Contains(diagnostic.String(), "Reading project settings") {
		t.Fatalf("launch started on cancellation: %s %s", out.String(), diagnostic.String())
	}
	if _, err := os.Stat(filepath.Join(data, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("workspace metadata created on cancel: %v", err)
	}
}
