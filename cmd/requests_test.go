package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/gateway"
	"github.com/hewitt-iso/hewitt/session"
)

func TestSuspiciousRequestApproval(t *testing.T) {
	runRepository(t)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
case "$1 $2" in
"volume inspect") printf 'No such volume' >&2; exit 1 ;;
"container create"|"network create") printf '` + containerID + `\n' ;;
"container inspect") printf 'true\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	cfg, _ := json.Marshal(config.Config{DataDir: data})
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(data, "tools", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "codex"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := executeCommand(t, "run", "codex", "--detach", "--isolation", "restricted", "--image", "agent:test", "--name", "srna", "--config", configPath); err != nil || out != containerID+"\n" {
		t.Fatalf("run = %q, %v", out, err)
	}
	store, err := session.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Get(containerID)
	if err != nil || m.Egress == nil || m.Egress.Open {
		t.Fatalf("restricted session gateway = %+v, %v", m.Egress, err)
	}
	// The gateway records a request to an unlisted host.
	request, _ := json.Marshal(gateway.Request{ID: "0123456789ab", Kind: "network", Target: "attacker.example:443", Created: time.Now()})
	if err := os.MkdirAll(filepath.Join(m.Egress.StateDir(), "requests"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Egress.StateDir(), "requests", "0123456789ab.json"), request, 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand("test")
	var stdout, stderr strings.Builder
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"ps", "--config", configPath})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "SRNA (1)") || !strings.Contains(stderr.String(), "hewitt approve") {
		t.Fatalf("ps = %q, %q", stdout.String(), stderr.String())
	}
	out, err := executeCommand(t, "requests", "--config", configPath)
	if err != nil || !strings.Contains(out, "0123456789ab  srna     network  attacker.example:443  SRNA") {
		t.Fatalf("requests = %q, %v", out, err)
	}
	if out, err := executeCommand(t, "approve", "0123456789ab", "--config", configPath); err != nil || out != "Approved network attacker.example:443 for session srna\n" {
		t.Fatalf("approve = %q, %v", out, err)
	}
	list, err := gateway.Requests(m.Egress.StateDir())
	if err != nil || len(list) != 1 || list[0].Decision != "allow" {
		t.Fatalf("decision = %+v, %v", list, err)
	}
	if out, _ := executeCommand(t, "requests", "--config", configPath); strings.Contains(out, "0123456789ab") {
		t.Fatalf("decided request listed as pending: %q", out)
	}
	if out, _ := executeCommand(t, "requests", "-a", "--config", configPath); !strings.Contains(out, "allow") {
		t.Fatalf("requests -a = %q", out)
	}
	if _, err := executeCommand(t, "deny", "ffffffffffff", "--config", configPath); err == nil || !strings.Contains(err.Error(), "no request") {
		t.Fatalf("unknown request = %v", err)
	}
}
