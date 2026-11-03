package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestStartTerminalArguments(t *testing.T) {
	log := fakeTerminalDocker(t, "exit 0")
	command := []string{"env", "-u", "OPENAI_API_KEY", "codex", "literal $(touch /tmp/unwanted)"}
	if err := (Client{}).StartTerminal(context.Background(), testID, command); err != nil {
		t.Fatal(err)
	}
	want := append(terminalExec(testID, "show-options", "-s"), terminalExec(testID, terminalKeys...)...)
	want = append(want, terminalExec(testID, "new-session", "-d", "-s", "hewitt", "--")...)
	want = append(want, command...)
	want = append(want, ";", "set-option", "-s", "exit-empty", "on")
	if got := readArgs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestStartTerminalFailurePreservesContainer(t *testing.T) {
	log := fakeTerminalDocker(t, `case "$*" in
 *new-session*) printf 'cannot launch agent\n' >&2; exit 17 ;;
 esac`)
	err := (Client{}).StartTerminal(context.Background(), testID, []string{"env", "codex"})
	if err == nil || !strings.Contains(err.Error(), testID) || !strings.Contains(err.Error(), "container preserved") || !strings.Contains(err.Error(), "cannot launch agent") {
		t.Fatalf("error = %v", err)
	}
	if got := readArgs(t, log); strings.Contains(strings.Join(got, " "), "container rm") {
		t.Fatal("removed container")
	}
}

func TestStartTerminalCancellation(t *testing.T) {
	log := fakeTerminalDocker(t, "exit 1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (Client{}).StartTerminal(ctx, testID, []string{"env", "codex"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("invoked Docker after cancellation")
	}
}

func TestAttachTerminalReconnects(t *testing.T) {
	t.Setenv("TERM", "")
	log := fakeTerminalDocker(t, "printf 'existing screen'")
	var out bytes.Buffer
	for range 2 {
		if err := (Client{}).AttachTerminal(context.Background(), testID, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	one := []string{"container", "exec", "--interactive", "--tty", "--env", "TERM=xterm-256color", "--", testID, "tmux", "-N", "-S", terminalSocket, "attach-session", "-t", "=hewitt"}
	if got := readArgs(t, log); !reflect.DeepEqual(got, append(one, one...)) {
		t.Fatalf("args = %q", got)
	}
	if out.String() != "existing screenexisting screen" {
		t.Fatalf("screen = %q", out.String())
	}
}

func fakeTerminalDocker(t *testing.T, body string) string {
	t.Helper()
	log := fakeDocker(t, `printf '%s\0' "$@" >> "$HEWITT_TERMINAL_CALLS"`+"\n"+body) + ".calls"
	t.Setenv("HEWITT_TERMINAL_CALLS", log)
	return log
}

func TestAttachTerminalPreservesHostType(t *testing.T) {
	for _, terminal := range []string{"xterm-kitty", "tmux-256color", "screen-256color", "dumb", ""} {
		t.Run(terminal, func(t *testing.T) {
			t.Setenv("TERM", terminal)
			log := fakeTerminalDocker(t, "exit 0")
			if err := (Client{}).AttachTerminal(context.Background(), testID, nil, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			want := terminal
			if want == "" || want == "dumb" {
				want = "xterm-256color"
			}
			args := readArgs(t, log)
			if args[5] != "TERM="+want {
				t.Fatalf("terminal environment = %q", args)
			}
		})
	}
}

func TestExternalTerminalConfiguredBeforeLaunch(t *testing.T) {
	log := fakeTerminalDocker(t, "exit 0")
	if err := (Client{}).StartExternalTerminal(context.Background(), testID, "/tmp/hewitt-test.sock", "/workspace", []string{"env", "codex"}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(readArgs(t, log), " ")
	config, launch := strings.Index(args, "default-terminal tmux-256color"), strings.Index(args, "new-session")
	if config < 0 || launch < config {
		t.Fatalf("agent started before terminal configuration: %s", args)
	}
	if strings.Contains(args, "bind-key -n C-c") || strings.Contains(args, "bind-key -n C-d") {
		t.Fatalf("native CLI keys intercepted: %s", args)
	}
}
