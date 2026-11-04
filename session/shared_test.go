package session

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharedWorkspaceReuseAndForegroundLaunch(t *testing.T) {
	runRepository(t)
	bin, data := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_CALLS"
case "$1 $2" in
"volume inspect") printf 'No such volume' >&2; exit 1 ;;
"container create")
 n=0
 if [ -f "$HEWITT_CALLS.count" ]; then n=$(cat "$HEWITT_CALLS.count"); fi
 n=$((n + 1)); printf '%s' "$n" > "$HEWITT_CALLS.count"
 printf '%064x\n' "$n" ;;
"container inspect") printf 'false\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data}
	id, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	opts.Agent = "claude"
	reused, err := RunAgent(ctx, opts, io.Discard)
	if err != nil || reused != id {
		t.Fatalf("reuse = %q, %v", reused, err)
	}
	store, _ := NewStore(data)
	records, err := store.List()
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %v, %v", records, err)
	}
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	opts.Shell = true
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Attach(ctx, store, id, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "host terminals") {
		t.Fatalf("attach = %v", err)
	}
	raw, _ := os.ReadFile(log)
	calls := string(raw)
	if strings.Count(calls, "container create ") != 1 || strings.Count(calls, "container start ") != 2 {
		t.Fatalf("lifecycle = %s", calls)
	}
	for _, want := range []string{"--entrypoint sleep", "agent:test infinity", "HEWITT_HOST_TERMINAL=1", "hewitt-agent run claude", "hewitt-agent run shell", "/hewitt/profiles/codex", "/hewitt/profiles/claude"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q: %s", want, calls)
		}
	}
	if strings.Contains(calls, "tmux") {
		t.Fatalf("nested tmux: %s", calls)
	}
	opts.Separate = true
	separate, err := RunAgent(ctx, opts, io.Discard)
	if err != nil || separate == id {
		t.Fatalf("separate = %q, %v", separate, err)
	}
	opts.Separate = false
	again, err := RunAgent(ctx, opts, io.Discard)
	if err != nil || again != id {
		t.Fatalf("default workspace changed after separate run: %q, %v", again, err)
	}
	if _, err := Stop(ctx, store, id); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceKeyAndLock(t *testing.T) {
	runRepository(t)
	data := t.TempDir()
	opts := AgentOptions{Agent: "codex", DataDir: data}
	s, err := mergeSettings(opts)
	if err != nil {
		t.Fatal(err)
	}
	key, unlock, err := lockWorkspace(context.Background(), data, s)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, release, err := lockWorkspace(ctx, data, s); err == nil {
		release()
		t.Fatal("concurrent workspace creation was not serialized")
	}
	unlock()
	opts.Agent = "claude"
	s, _ = mergeSettings(opts)
	other, release, err := lockWorkspace(context.Background(), data, s)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if key != other {
		t.Fatal("agent selection changed workspace identity")
	}
	for _, mutate := range []func(*settings){
		func(s *settings) { s.Profile = "other" },
		func(s *settings) { s.Isolation = "restricted" },
		func(s *settings) { s.Name = "another" },
		func(s *settings) { s.Image = "another:image" },
	} {
		changed := s
		mutate(&changed)
		different, release, err := lockWorkspace(context.Background(), data, changed)
		if err != nil {
			t.Fatal(err)
		}
		release()
		if key == different {
			t.Fatal("incompatible settings share workspace identity")
		}
	}
}
