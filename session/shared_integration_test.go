//go:build integration

package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/profile"
)

func TestSharedWorkspaceLifecycle(t *testing.T) {
	image := os.Getenv("HEWITT_TEST_IMAGE")
	if image == "" {
		t.Skip("HEWITT_TEST_IMAGE is required")
	}
	runRepository(t)
	data := t.TempDir()
	tools, err := profile.PrepareTools(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tools, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	// Stub clients use real Git and terminal setup without agent accounts/network.
	for _, name := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(tools, "bin", name), []byte("#!/bin/sh\ngit branch --show-current\npwd\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	opts := AgentOptions{Agent: "codex", Image: image, DataDir: data, Name: "hewitt-test-" + strings.ToLower(rand.Text())}
	id, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(data)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := Stop(cleanup, store, id); err != nil {
			t.Error(err)
		}
		if _, err := Remove(cleanup, store, id, true); err != nil {
			t.Error(err)
		}
	})
	other := opts
	other.Agent = "claude"
	reused, err := RunAgent(ctx, other, io.Discard)
	if err != nil || reused != id {
		t.Fatalf("reuse %q: %v", reused, err)
	}
	var wg sync.WaitGroup
	for _, name := range []string{"codex", "claude"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			run := opts
			run.Agent = name
			var out, diagnostic bytes.Buffer
			if err := OpenWorkspace(ctx, store, id, run, strings.NewReader(""), &out, &diagnostic); err != nil {
				t.Errorf("%s: %v: %s", name, err, diagnostic.String())
				return
			}
			if !strings.Contains(out.String(), name+"-1") {
				t.Errorf("%s output: %s", name, out.String())
			}
		}(name)
	}
	wg.Wait()
	opts.Shell = true
	var out, diagnostic bytes.Buffer
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader("printf 'shell-ok\\n'\nexit\n"), &out, &diagnostic); err != nil {
		t.Fatalf("shell: %v: %s", err, diagnostic.String())
	}
	if !strings.Contains(out.String(), "shell-ok") {
		t.Fatalf("shell output: %s", out.String())
	}
	if running, err := (docker.Client{}).Running(ctx, id); err != nil || !running {
		t.Fatalf("container after processes exit: %v %v", running, err)
	}
	if _, err := Stop(ctx, store, id); err != nil {
		t.Fatal(err)
	}
	opts.Shell = false
	if restarted, err := RunAgent(ctx, opts, io.Discard); err != nil || restarted != id {
		t.Fatalf("restart %q: %v", restarted, err)
	}
}
