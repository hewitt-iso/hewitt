package session

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/images"
)

const myAgent = `version: 1
name: my-agent
binary: myagent
install: {npm: my-agent-cli}
unset_env: [MY_API_KEY]
commands: {run: [--yes], login: [login], status: [whoami], logout: [logout]}
task: {interactive: ["{prompt}"], headless: [-p, "{prompt}"]}
config_dir: "{profile}/my"
instructions: "{profile}/my/RULES.md"
hosts: [api.example.com]
`

func writeManifest(t *testing.T, data string) {
	t.Helper()
	dir := filepath.Join(data, "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "my-agent.yaml"), []byte(myAgent), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUserAgentJoinsExistingWorkspace adds an agent after its workspace was
// created: the next run replaces the container, which then mounts the new
// agent's profile, and launches the agent its manifest describes.
func TestUserAgentJoinsExistingWorkspace(t *testing.T) {
	runRepository(t)
	log := fakeToolsDocker(t)
	data := t.TempDir()
	installTool(t, data, "codex")
	ctx := context.Background()
	opts := AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data}
	id, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(data)
	writeManifest(t, data)
	opts.Agent = "my-agent"
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader(""), io.Discard, io.Discard); !errors.As(err, new(*OutdatedWorkspaceError)) {
		t.Fatalf("open in old workspace: %v", err)
	}
	var stderr strings.Builder
	id, err = RunAgent(ctx, opts, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "has no profile for my-agent; replacing its container") {
		t.Fatalf("stderr = %s", stderr.String())
	}
	installTool(t, data, "myagent")
	opts.Prompt, opts.Headless = "go", true
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var create, exec string
	for _, call := range calls(t, log) {
		switch {
		case strings.HasPrefix(call, "container create ") && strings.Contains(call, "sleep"):
			create = call
		case strings.HasPrefix(call, "container exec "):
			exec = call
		}
	}
	if !strings.Contains(create, ",dst=/hewitt/profiles/my-agent ") {
		t.Fatalf("create = %s", create)
	}
	_, spec, _ := strings.Cut(exec, "--env HEWITT_LAUNCH=")
	spec, _, _ = strings.Cut(spec, " ")
	raw, _ := base64.StdEncoding.DecodeString(spec)
	if !strings.HasSuffix(string(raw), "\x00myagent\x00--yes\x00-p\x00go\x00") || !strings.HasPrefix(string(raw), "env\x00-u\x00MY_API_KEY\x00") {
		t.Fatalf("launch = %q", raw)
	}
}

func TestAgentScriptIsWrittenOnce(t *testing.T) {
	data := t.TempDir()
	mount, path, err := agentScript(data)
	if err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(mount.Source, filepath.Base(path))
	info, err := os.Stat(host)
	if err != nil || info.Mode().Perm() != 0o500 || mount.Target != agentDir || !mount.ReadOnly || filepath.Dir(path) != agentDir {
		t.Fatalf("script %s: %v, %v, mount %+v", path, info, err, mount)
	}
	data2, _ := os.ReadFile(host)
	if string(data2) != string(images.Agent) {
		t.Fatal("script differs from the embedded hewitt-agent")
	}
	if _, again, err := agentScript(data); err != nil || again != path {
		t.Fatalf("second write: %s, %v", again, err)
	}
}

// TestWorkspaceWithoutMountedAgentIsReplaced reuses a workspace that an
// older Hewitt created: it ran the image's hewitt-agent, so it is replaced.
func TestWorkspaceWithoutMountedAgentIsReplaced(t *testing.T) {
	runRepository(t)
	fakeToolsDocker(t)
	data := t.TempDir()
	installTool(t, data, "codex")
	ctx := context.Background()
	opts := AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data}
	id, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(data)
	m, _ := store.Get(id)
	m.MountsAgent = false
	if err := store.Save(m); err != nil {
		t.Fatal(err)
	}
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader(""), io.Discard, io.Discard); !errors.As(err, new(*OutdatedWorkspaceError)) {
		t.Fatalf("open: %v", err)
	}
	var stderr strings.Builder
	if _, err := RunAgent(ctx, opts, &stderr); err != nil || !strings.Contains(stderr.String(), "predates the hewitt-agent that Hewitt mounts; replacing its container") {
		t.Fatalf("rerun: %v, %s", err, stderr.String())
	}
}
