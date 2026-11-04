package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/agent"
)

func TestAgentExitError(t *testing.T) {
	interrupted := &AgentExitError{Agent: "pi", Workspace: "w", Code: 130, Err: &exec.ExitError{}}
	if !errors.Is(interrupted, errInterrupted) {
		t.Fatal("Ctrl-C exit is not treated as interrupted")
	}
	for code, want := range map[int]string{127: "could not start pi", 137: "killed", 2: "exited with status 2"} {
		err := &AgentExitError{Agent: "pi", Workspace: "w", Code: code, Err: &exec.ExitError{}}
		if !strings.Contains(err.Error(), want) || err.Hint() == "" || errors.Is(err, errInterrupted) {
			t.Errorf("code %d: %v (%s)", code, err, err.Hint())
		}
	}
	if err := (&NotFoundError{Ref: "x"}); !errors.Is(err, os.ErrNotExist) || err.Hint() == "" {
		t.Fatalf("not found = %v", err)
	}
}

// imageDocker installs a fake Docker whose image and container image IDs come
// from HEWITT_IMAGE and HEWITT_CONTAINER_IMAGE; calls are logged.
func imageDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_CALLS"
case "$*" in
"image inspect"*) printf '%s\n' "$HEWITT_IMAGE" ;;
"image pull"*) [ -z "$HEWITT_PULL_FAIL" ] || { printf 'Error response from daemon: dial tcp: lookup ghcr.io: no such host\n' >&2; exit 1; } ;;
"volume inspect"*) printf 'No such volume' >&2; exit 1 ;;
"container create"*)
 n=0
 if [ -f "$HEWITT_CALLS.count" ]; then n=$(cat "$HEWITT_CALLS.count"); fi
 n=$((n + 1)); printf '%s' "$n" > "$HEWITT_CALLS.count"
 printf '%064x\n' "$n" ;;
"container inspect --format {{.Image}}"*) printf '%s\n' "$HEWITT_CONTAINER_IMAGE" ;;
"container inspect"*) printf 'true\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestRefreshImage(t *testing.T) {
	log := imageDocker(t)
	ctx := context.Background()
	for _, tt := range []struct {
		image, local, fail string
		pull               bool
		warning            string
	}{
		{image: agent.DefaultImage, local: "sha256:a 1", pull: true},
		{image: agent.DefaultImage, local: "sha256:a 0"},
		{image: agent.DefaultImage, local: ""},
		{image: "hewitt:local", local: "sha256:a 1"},
		{image: agent.DefaultImage, local: "sha256:a 1", fail: "1", pull: true, warning: "using the local copy"},
	} {
		os.Remove(log)
		t.Setenv("HEWITT_IMAGE", tt.local)
		t.Setenv("HEWITT_PULL_FAIL", tt.fail)
		var stderr bytes.Buffer
		if err := refreshImage(ctx, tt.image, &stderr); err != nil {
			t.Fatalf("%+v: %v", tt, err)
		}
		calls, _ := os.ReadFile(log)
		if strings.Contains(string(calls), "image pull") != tt.pull || !strings.Contains(stderr.String(), tt.warning) {
			t.Errorf("%+v: calls = %s, stderr = %s", tt, calls, stderr.String())
		}
	}
}

func TestOutdatedWorkspaceContainerIsReplaced(t *testing.T) {
	runRepository(t)
	log := imageDocker(t)
	ctx := context.Background()
	data := t.TempDir()
	t.Setenv("HEWITT_IMAGE", "sha256:old 0")
	t.Setenv("HEWITT_CONTAINER_IMAGE", "sha256:old")
	opts := AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data}
	first, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := RunAgent(ctx, opts, io.Discard); err != nil || again != first {
		t.Fatalf("current image reuse = %q, %v", again, err)
	}
	t.Setenv("HEWITT_IMAGE", "sha256:new 0")
	var stderr bytes.Buffer
	replaced, err := RunAgent(ctx, opts, &stderr)
	if err != nil || replaced == first {
		t.Fatalf("replacement = %q, %v", replaced, err)
	}
	store, _ := NewStore(data)
	records, err := store.List()
	if err != nil || len(records) != 1 || records[0].ID != replaced || records[0].Volume == nil || records[0].Volume.Name != "workspace-1" {
		t.Fatalf("records = %+v, %v", records, err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "container rm -- "+first) || strings.Contains(string(calls), "volume rm") || strings.Count(string(calls), "volume create") != 1 {
		t.Fatalf("calls = %s", calls)
	}
	if !strings.Contains(stderr.String(), "older agent:test") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}
