package docker

import (
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestExecInteractiveUsesDirectArguments(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	log := fakeTerminalDocker(t, "exit 0")
	command := []string{"hewitt-agent", "run", "literal $(touch /tmp/unwanted)"}
	if err := (Client{}).ExecInteractive(context.Background(), testID, nil, nil, command, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"container", "exec", "--interactive", "--env", "TERM=xterm-256color", "--", testID}, command...)
	if got := readArgs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}

func TestExecInteractivePassesHintsToTheDockerCLIOnly(t *testing.T) {
	log := fakeTerminalDocker(t, `printf '%s' "$HERDR_AGENT" > "$HEWITT_TERMINAL_CALLS.env"`)
	client := Client{Env: []string{"HERDR_AGENT=codex"}}
	if err := client.ExecInteractive(context.Background(), testID, nil, nil, []string{"hewitt-agent"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if env, _ := os.ReadFile(log + ".env"); string(env) != "codex" {
		t.Fatalf("Docker CLI environment HERDR_AGENT = %q", env)
	}
	for _, arg := range readArgs(t, log) {
		if strings.Contains(arg, "HERDR_AGENT") {
			t.Fatalf("hint reached the container: %q", readArgs(t, log))
		}
	}
	if err := (Client{Env: []string{"=x"}}).ExecInteractive(context.Background(), testID, nil, nil, []string{"x"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("accepted an invalid environment entry")
	}
}
