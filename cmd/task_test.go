package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTaskValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	long := filepath.Join(home, "long.md")
	if err := os.WriteFile(long, []byte(strings.Repeat("x", maxTask+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "codex", "--headless"}, "--headless needs a task"},
		{[]string{"run", "--headless", "-p", "fix it"}, "--headless needs an agent"},
		{[]string{"run", "codex", "-p", "fix it", "--prompt-file", long}, "either -p or --prompt-file"},
		{[]string{"run", "codex", "-p", "fix it", "--detach"}, "cannot be combined with --detach"},
		{[]string{"run", "codex", "--prompt-file", long}, "longer than 96 KiB"},
		{[]string{"run", "codex", "--prompt-file", filepath.Join(home, "missing.md")}, "read task"},
		{[]string{"run", "codex", "-p", "fix it", "--workspace", "copy"}, "tasks (-p) require the managed volume workspace"},
	} {
		if _, err := executeCommand(t, tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: error = %v, want %q", tt.args, err, tt.want)
		}
	}
}
