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
	log := fakeTerminalDocker(t, "printf 'existing screen'")
	var out bytes.Buffer
	for range 2 {
		if err := (Client{}).AttachTerminal(context.Background(), testID, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	one := []string{"container", "exec", "--interactive", "--tty", "--env", "TERM=xterm-256color", "--env", "LANG=C.UTF-8", "--", testID, "tmux", "-u", "-N", "-S", terminalSocket, "attach-session", "-t", "=hewitt"}
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
