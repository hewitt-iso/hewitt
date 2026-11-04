package docker

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestExecInteractiveUsesDirectArguments(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	log := fakeTerminalDocker(t, "exit 0")
	command := []string{"hewitt-agent", "run", "literal $(touch /tmp/unwanted)"}
	if err := (Client{}).ExecInteractive(context.Background(), testID, command, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"container", "exec", "--interactive", "--env", "TERM=xterm-256color", "--", testID}, command...)
	if got := readArgs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}
