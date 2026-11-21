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
	// The gateway and observer record requests; two carry an agent connection.
	run := strings.Repeat("ab", 32)
	write := func(id, kind, target, run, agent string, at time.Time) {
		t.Helper()
		request, _ := json.Marshal(gateway.Request{ID: id, Kind: kind, Target: target, Run: run, Agent: agent, Created: at})
		if err := os.MkdirAll(filepath.Join(m.Egress.StateDir(), "requests"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.Egress.StateDir(), "requests", id+".json"), request, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("0123456789ab", "network", "attacker.example:443", "", "", now)
	write("0123456789ac", "file", "/hewitt/profiles/claude/claude/.credentials.json", run, "codex", now.Add(time.Second))
	write("0123456789ad", "network", "exfil.example:443", run, "codex", now.Add(2*time.Second))

	root := NewRootCommand("test")
	var stdout, stderr strings.Builder
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"ps", "--config", configPath})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "SRNA (3)") || !strings.Contains(stderr.String(), "hewitt approve, deny, or abort") {
		t.Fatalf("ps = %q, %q", stdout.String(), stderr.String())
	}
	out, err := executeCommand(t, "requests", "--config", configPath)
	for _, want := range []string{"1  srna     -      network  attacker.example:443", "2  srna     codex  file     /hewitt/profiles/claude/claude/.credentials.json", "3  srna     codex  network  exfil.example:443"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("requests = %q, %v; want %q", out, err, want)
		}
	}
	// A request arriving after the list was shown takes the next number.
	write("0123456789ae", "network", "late.example:443", "", "", now.Add(-time.Hour))
	if out, err := executeCommand(t, "requests", "--config", configPath); err != nil || !strings.Contains(out, "4  srna     -      network  late.example:443") || !strings.Contains(out, "1  srna     -      network  attacker.example:443") {
		t.Fatalf("renumbered requests = %q, %v", out, err)
	}
	if _, err := executeCommand(t, "approve", "1-2", "9", "--config", configPath); err == nil || !strings.Contains(err.Error(), "#9") {
		t.Fatalf("missing number decided something: %v", err)
	}
	if list, _ := gateway.Requests(m.Egress.StateDir()); list[1].Decision != "" {
		t.Fatalf("partial batch applied: %+v", list)
	}
	out, err = executeCommand(t, "approve", "1-2", "--config", configPath)
	if err != nil || out != "Approved #1 network attacker.example:443 in srna\nApproved #2 file /hewitt/profiles/claude/claude/.credentials.json in srna (codex)\n" {
		t.Fatalf("approve = %q, %v", out, err)
	}
	calls := filepath.Join(dir, "calls")
	t.Setenv("HEWITT_CALLS", calls)
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_CALLS"
printf '41 42\n'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = executeCommand(t, "abort", "3,4", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), "request #4 was denied, but it names no agent") || out != "Aborted #3 network exfil.example:443 in srna (codex)\n" {
		t.Fatalf("abort = %q, %v", out, err)
	}
	raw, _ := os.ReadFile(calls)
	if !strings.HasPrefix(string(raw), "container exec -- "+containerID+" sh -c ") || !strings.HasSuffix(strings.TrimSpace(string(raw)), " sh "+run) {
		t.Fatalf("abort exec = %q", raw)
	}
	decided := map[string]string{}
	list, err := gateway.Requests(m.Egress.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		decided[r.Target] = r.Decision
	}
	if decided["attacker.example:443"] != "allow" || decided["exfil.example:443"] != "deny" || decided["late.example:443"] != "deny" {
		t.Fatalf("decisions = %v", decided)
	}
	if out, _ := executeCommand(t, "requests", "--config", configPath); strings.Contains(out, "SRNA") {
		t.Fatalf("decided requests listed as pending: %q", out)
	}
	if out, _ := executeCommand(t, "requests", "-a", "--config", configPath); !strings.Contains(out, "allow") || !strings.Contains(out, "deny") {
		t.Fatalf("requests -a = %q", out)
	}
}
