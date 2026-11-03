package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOnboardMarker(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "hewitt-data")
	if IsOnboarded(dataDir) {
		t.Fatal("IsOnboarded = true before MarkOnboarded")
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("IsOnboarded created data directory: %v", err)
	}
	if err := MarkOnboarded(dataDir); err != nil {
		t.Fatal(err)
	}
	if !IsOnboarded(dataDir) {
		t.Fatal("IsOnboarded = false after MarkOnboarded")
	}
	info, err := os.Stat(filepath.Join(dataDir, ".onboarded"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("marker stat = %v, %v", info, err)
	}
}

func TestImportHostConfigsAndCredentials(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "hewitt-data")

	// Empty home detects nothing.
	if got := DetectHostConfigs(home); len(got) != 0 {
		t.Fatalf("DetectHostConfigs(empty) = %v", got)
	}
	if got := DetectHostCredentials(home); len(got) != 0 {
		t.Fatalf("DetectHostCredentials(empty) = %v", got)
	}

	// Populate host configs and credentials for Claude, Codex, OpenCode, and tmux.
	mustWrite := func(rel, content string) {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(".claude/CLAUDE.md", "# Claude rules\n")
	mustWrite(".claude/skills/my-skill/SKILL.md", "skill body\n")
	mustWrite(".claude/.credentials.json", `{"claudeAiOauth":{"accessToken":"secret-claude"}}`)
	mustWrite(".claude.json", `{"oauthAccount":"do-not-copy","mcpServers":{"fs":{"command":"npx"}}}`)
	mustWrite(".codex/AGENTS.md", "# Codex rules\n")
	mustWrite(".codex/config.toml", "model = \"gpt-5\"\n")
	mustWrite(".codex/auth.json", `{"tokens":{"access_token":"secret-codex"}}`)
	mustWrite(".config/opencode/opencode.json", `{"theme":"dark"}`)
	mustWrite(".config/opencode/skill/test/SKILL.md", "opencode skill\n")
	mustWrite(".local/share/opencode/auth.json", `{"github-copilot":{"access":"secret-opencode"}}`)
	mustWrite(".tmux.conf", "set -g mouse on\n")

	// Add a symlink inside .claude/skills to ensure symlinks are skipped safely.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".claude/skills/symlink.txt")); err != nil {
		t.Fatal(err)
	}

	if got, want := DetectHostConfigs(home), []string{"claude", "codex", "opencode", "tmux"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectHostConfigs = %v, want %v", got, want)
	}
	if got, want := DetectHostCredentials(home), []string{"claude", "codex", "opencode"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectHostCredentials = %v, want %v", got, want)
	}

	importedCfg, err := ImportHostConfigs(dataDir, home, "default")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "codex", "opencode", "tmux"}; !reflect.DeepEqual(importedCfg, want) {
		t.Fatalf("ImportHostConfigs = %v, want %v", importedCfg, want)
	}

	// Verify configs were copied and credentials were NOT copied by ImportHostConfigs.
	claudeProfile := filepath.Join(dataDir, "profiles", "claude", "default")
	if _, err := os.Stat(filepath.Join(claudeProfile, "claude", ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("ImportHostConfigs copied credentials: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(claudeProfile, "claude", "skills", "symlink.txt")); !os.IsNotExist(err) {
		t.Fatalf("ImportHostConfigs followed symlink: %v", err)
	}
	mcpData, err := os.ReadFile(filepath.Join(claudeProfile, "home", ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mcpData), "do-not-copy") || !strings.Contains(string(mcpData), "mcpServers") {
		t.Fatalf("MCP import leaked non-MCP fields: %s", mcpData)
	}
	for _, check := range []string{
		filepath.Join(claudeProfile, "claude", "CLAUDE.md"),
		filepath.Join(claudeProfile, "claude", "skills", "my-skill", "SKILL.md"),
		filepath.Join(claudeProfile, "home", ".tmux.conf"),
		filepath.Join(dataDir, "profiles", "codex", "default", "codex", "AGENTS.md"),
		filepath.Join(dataDir, "profiles", "codex", "default", "codex", "config.toml"),
		filepath.Join(dataDir, "profiles", "opencode", "default", "config", "opencode", "opencode.json"),
		filepath.Join(dataDir, "profiles", "opencode", "default", "config", "opencode", "skill", "test", "SKILL.md"),
	} {
		info, err := os.Stat(check)
		if err != nil {
			t.Fatalf("missing imported config %s: %v", check, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s perm = %o, want 0600", check, info.Mode().Perm())
		}
	}

	// Now import credentials and verify 0600 permissions.
	importedCreds, err := ImportHostCredentials(dataDir, home, "default")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "codex", "opencode"}; !reflect.DeepEqual(importedCreds, want) {
		t.Fatalf("ImportHostCredentials = %v, want %v", importedCreds, want)
	}
	for _, credPath := range []string{
		filepath.Join(dataDir, "profiles", "claude", "default", "claude", ".credentials.json"),
		filepath.Join(dataDir, "profiles", "codex", "default", "codex", "auth.json"),
		filepath.Join(dataDir, "profiles", "opencode", "default", "data", "opencode", "auth.json"),
	} {
		info, err := os.Stat(credPath)
		if err != nil {
			t.Fatalf("missing imported credential %s: %v", credPath, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s perm = %o, want 0600", credPath, info.Mode().Perm())
		}
	}
}
