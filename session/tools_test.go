package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
)

// fakeToolsDocker records each Docker call on one line and creates containers.
func fakeToolsDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_CALLS"
case "$1 $2" in
"volume inspect") printf 'No such volume' >&2; exit 1 ;;
"container create") printf '%s\n' '` + adapterTestID + `' ;;
"container inspect")
 case "$*" in
 *ExitCode*|*json*) printf '%s\n' '{"Status":"exited","ExitCode":0}' ;;
 *) printf 'false\n' ;;
 esac ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return log
}

// builtin returns a built-in agent manifest.
func builtin(t *testing.T, name string) agent.Manifest {
	t.Helper()
	r, err := agent.Load("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func TestAgentContainersMountToolCacheReadOnly(t *testing.T) {
	runRepository(t)
	log := fakeToolsDocker(t)
	data := t.TempDir()
	ctx := context.Background()
	opts := AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data}
	id, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(data)
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	legacy := AgentOptions{Agent: "claude", Image: "agent:test", DataDir: data, Workspace: "worktree", Danger: []string{"host-paths"}}
	if _, err := RunAgent(ctx, legacy, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Authenticate(ctx, AgentOptions{Agent: "opencode", Image: "agent:test", Profile: "default", DataDir: data}, "status", nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	creates, installs := 0, map[string]bool{}
	for _, call := range calls(t, log) {
		if !strings.Contains(call, ",dst=/hewitt/tools") {
			continue
		}
		switch {
		case strings.HasPrefix(call, "container create "):
			creates++
			if !strings.Contains(call, ",dst=/hewitt/tools,readonly ") {
				t.Errorf("agent container mounts a writable tool cache: %s", call)
			}
		case strings.HasPrefix(call, "container run --rm "):
			_, spec, _ := strings.Cut(call, "--env HEWITT_INSTALL=")
			spec, _, _ = strings.Cut(spec, " ")
			raw, _ := base64.StdEncoding.DecodeString(spec)
			args := strings.Split(string(raw), "\x00")
			if len(args) > 1 && strings.HasSuffix(call, " agent:test install") {
				installs[args[1]] = true
			}
			if strings.Contains(call, ",dst=/hewitt/tools,readonly") {
				t.Errorf("installer cannot write the tool cache: %s", call)
			}
			for _, leak := range []string{"/hewitt/profile", "/hewitt/work", "--env-file", "--network"} {
				if strings.Contains(call, leak) {
					t.Errorf("installer receives %s: %s", leak, call)
				}
			}
		default:
			t.Errorf("unexpected tool cache mount: %s", call)
		}
	}
	if creates != 3 {
		t.Errorf("agent containers with the tool cache = %d, want 3", creates)
	}
	for _, agent := range []string{"codex", "claude", "opencode"} {
		if !installs[agent] {
			t.Errorf("%s was not installed before launch: %v", agent, installs)
		}
	}
}

func TestEnsureToolSkipsInstalledAgentWithoutLock(t *testing.T) {
	log := fakeToolsDocker(t)
	data := t.TempDir()
	installTool(t, data, "agy")
	// Another installation holds the lock; installed agents must not wait.
	unlock, err := lockTools(context.Background(), data, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ensureTool(ctx, data, "agent:test", builtin(t, "antigravity"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); got[0] != "" {
		t.Fatalf("installed agent ran Docker: %v", got)
	}
}

func TestConcurrentInstallsRunOnce(t *testing.T) {
	log := fakeToolsDocker(t)
	data := t.TempDir()
	unlock, err := lockTools(context.Background(), data, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	done := make(chan error, 1)
	codex := builtin(t, "codex")
	go func() { done <- ensureTool(context.Background(), data, "agent:test", codex, &stderr) }()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("installation did not wait for the lock: %v", err)
	default:
	}
	// The lock holder finishes the same installation; the waiter rechecks.
	installTool(t, data, "codex")
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); got[0] != "" {
		t.Fatalf("agent installed twice: %v", got)
	}
}

func installTool(t *testing.T, data, binary string) {
	t.Helper()
	dir := filepath.Join(data, "tools", "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, binary), nil, 0o700); err != nil {
		t.Fatal(err)
	}
}
