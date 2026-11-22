package session

import (
	"context"
	"encoding/base64"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/profile"
)

func TestOpenPortsListsNonHTTPSEntries(t *testing.T) {
	got := openPorts([]string{"pypi.org", "mirror.local:443", "collector.local:4318"})
	if len(got) != 1 || got[0] != "collector.local:4318" {
		t.Fatalf("openPorts = %q", got)
	}
}

func TestClaudeReceivesPlaceholderBehindGateway(t *testing.T) {
	runRepository(t)
	log := fakeToolsDocker(t)
	data := t.TempDir()
	secrets, err := profile.PrepareSecrets(data, "claude", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.SaveToken(secrets, "CLAUDE_CODE_OAUTH_TOKEN", "real-secret-token"); err != nil {
		t.Fatal(err)
	}
	installTool(t, data, "claude")
	ctx := context.Background()
	opts := AgentOptions{Agent: "claude", Image: "agent:test", DataDir: data}
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
	placeholders := 0
	for _, call := range calls(t, log) {
		if strings.Contains(call, " observe /hewitt/observer/config.json") {
			// The observer holds file access; it receives no credentials.
			if strings.Contains(call, secrets) || strings.Contains(call, "real-secret-token") {
				t.Errorf("observer receives the token: %s", call)
			}
			continue
		}
		if strings.Contains(call, " gateway /hewitt/gateway/config.json") {
			// Only the gateway mounts the token, read-only.
			if !strings.Contains(call, "src="+secrets+",dst=/hewitt/secrets/claude,readonly") {
				t.Errorf("gateway without the token: %s", call)
			}
			continue
		}
		if strings.Contains(call, secrets) || strings.Contains(call, "real-secret-token") {
			t.Errorf("agent container receives the token: %s", call)
		}
		if strings.Contains(call, "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-hewitt-gateway-managed") {
			placeholders++
			if !strings.Contains(call, "NODE_EXTRA_CA_CERTS="+gatewayCA) && !strings.HasPrefix(call, "container exec ") {
				t.Errorf("placeholder without the gateway CA: %s", call)
			}
		}
	}
	// The workspace exec and the legacy container each get the placeholder.
	if placeholders != 2 {
		t.Fatalf("placeholders = %d", placeholders)
	}
}

func TestTaskReachesAgentLaunch(t *testing.T) {
	runRepository(t)
	log := fakeToolsDocker(t)
	data := t.TempDir()
	installTool(t, data, "codex")
	ctx := context.Background()
	task := "fix the failing test\nthen run 'go test ./...' -v"
	opts := AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data, Prompt: task, Headless: true}
	id, err := RunAgent(ctx, opts, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(data)
	var stderr strings.Builder
	if err := OpenWorkspace(ctx, store, id, opts, strings.NewReader("ignored"), io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	var exec string
	for _, call := range calls(t, log) {
		if strings.HasPrefix(call, "container exec ") {
			exec = call
		}
	}
	_, encoded, _ := strings.Cut(exec, "--env HEWITT_LAUNCH=")
	encoded, _, _ = strings.Cut(encoded, " ")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || strings.Contains(exec, "--tty") || !regexp.MustCompile(` /hewitt/agent/hewitt-agent-[0-9a-f]{16} run codex$`).MatchString(exec) {
		t.Fatalf("exec = %s, %v", exec, err)
	}
	// The task reaches codex exec, its headless mode, as one argument.
	argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if !slices.Contains(argv, "exec") || !slices.Equal(argv[len(argv)-2:], []string{"--", task}) {
		t.Fatalf("launch = %q", argv)
	}
	if strings.Contains(exec, "fix the failing test") {
		t.Fatalf("task passed in clear text: %s", exec)
	}
	if !strings.Contains(stderr.String(), "codex finished in workspace") || !strings.Contains(stderr.String(), "hewitt sync") {
		t.Fatalf("headless summary = %q", stderr.String())
	}
}
