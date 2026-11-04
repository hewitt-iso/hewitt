package terminal

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeTmux(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("PATH", dir)
	t.Setenv("TMUX_TEST_LOG", log)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_LOG"
case "$3" in
display-message) printf '$1\t@2\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestFocusExistingHostPane(t *testing.T) {
	for _, inside := range []bool{true, false} {
		t.Run(map[bool]string{true: "inside", false: "outside"}[inside], func(t *testing.T) {
			log := fakeTmux(t)
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			if inside {
				t.Setenv("TMUX", "/tmp/host,socket,123,0")
				t.Setenv("TMUX_PANE", "%9")
			}
			l := Location{Socket: "/tmp/host,socket", Pane: "%3"}
			if err := Focus(context.Background(), l, nil, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(log)
			calls := string(raw)
			for _, want := range []string{"display-message -p -t %3", "select-pane -t %3", "select-window -t @2"} {
				if !strings.Contains(calls, want) {
					t.Fatalf("missing %q: %s", want, calls)
				}
			}
			want := "attach-session -t $1"
			if inside {
				want = "switch-client -t $1"
			}
			if !strings.Contains(calls, want) {
				t.Fatalf("missing %q: %s", want, calls)
			}
			if strings.Contains(calls, "new-") {
				t.Fatalf("created a new terminal: %s", calls)
			}
		})
	}
}

func TestFocusRejectsUnavailableLocations(t *testing.T) {
	t.Setenv("TMUX", "")
	if err := Focus(context.Background(), Location{}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("plain terminal accepted")
	}
	log := fakeTmux(t)
	t.Setenv("TMUX", "/tmp/other,1,0")
	t.Setenv("TMUX_PANE", "%1")
	if err := Focus(context.Background(), Location{Socket: "/tmp/target", Pane: "%1"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("switched unrelated tmux client")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("changed tmux before checking its server")
	}
}
