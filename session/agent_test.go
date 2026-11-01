package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const adapterTestID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestLoginAndRunUseSamePersistentProfile(t *testing.T) {
	bin, data := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin)
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$HEWITT_CALLS"
case "$2" in
create) printf '%s\n' '` + adapterTestID + `' ;;
start) if [ "$3" = "--attach" ]; then printf 'native login prompt'; fi ;;
wait) printf '0\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"claude", "codex"} {
		opts := AgentOptions{Agent: client, Image: "agent:test", Profile: "personal", DataDir: data}
		var out, stderr bytes.Buffer
		if err := Login(context.Background(), opts, strings.NewReader(""), &out, &stderr); err != nil {
			t.Fatal(err)
		}
		store, err := NewStore(data)
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := store.Get(adapterTestID)
		if err != nil || metadata.Profile != "personal" || metadata.Operation != "login" {
			t.Fatalf("login metadata: %+v, %v", metadata, err)
		}
		if out.String() != "native login prompt" || !strings.Contains(stderr.String(), adapterTestID) {
			t.Fatalf("login streams %q %q", out.String(), stderr.String())
		}
		state := filepath.Join(data, "profiles", client, "personal", client, "synthetic-state")
		if err := os.WriteFile(state, []byte("native"), 0o600); err != nil {
			t.Fatal(err)
		}
		if id, err := RunAgent(context.Background(), opts); err != nil || id != adapterTestID {
			t.Fatalf("run %q %v", id, err)
		}
		metadata, err = store.Get(adapterTestID)
		if err != nil || metadata.Profile != "personal" || metadata.Operation != "run" {
			t.Fatalf("run metadata: %+v, %v", metadata, err)
		}
		if b, err := os.ReadFile(state); err != nil || string(b) != "native" {
			t.Fatalf("lost state %q %v", b, err)
		}
		calls, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(state)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(calls), "src="+canonical+",dst=/hewitt/profile") != 2 {
			t.Fatalf("login/run did not share profile: %s", calls)
		}
	}
}

func TestPreparationRejectsInvalidInputBeforeCreatingProfile(t *testing.T) {
	for _, opts := range []AgentOptions{
		{Agent: "opencode", Image: "test", Profile: "default"},
		{Agent: "codex", Image: "--privileged", Profile: "default"},
		{Agent: "claude", Image: "test", Profile: "../escape"},
	} {
		opts.DataDir = filepath.Join(t.TempDir(), "absent")
		if _, err := RunAgent(context.Background(), opts); err == nil {
			t.Fatal("accepted invalid input")
		}
		if _, err := os.Stat(opts.DataDir); !os.IsNotExist(err) {
			t.Fatalf("created profile root: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunAgent(ctx, AgentOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
