package session

import (
	"context"
	"io"
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
		if strings.Contains(call, "--entrypoint hewitt ") {
			// Only the gateway mounts the token, read-only.
			if !strings.Contains(call, "src="+secrets+",dst=/hewitt/secrets/claude,readonly") {
				t.Errorf("gateway without the token: %s", call)
			}
			continue
		}
		if strings.Contains(call, secrets) || strings.Contains(call, "real-secret-token") {
			t.Errorf("agent container receives the token: %s", call)
		}
		if strings.Contains(call, "CLAUDE_CODE_OAUTH_TOKEN="+claudePlaceholder) {
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
