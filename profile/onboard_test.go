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
	mustWrite(".codex/rules/default.rules", "rule1\n")
	mustWrite(".codex/config.toml", "model = \"gpt-5\"\n\n[marketplaces.openai-bundled]\nsource = \"/host/only\"\n\n[tui]\ntheme = \"dark\"\n")
	mustWrite(".codex/auth.json", `{"tokens":{"access_token":"secret-codex"}}`)
	mustWrite(".config/opencode/opencode.json", `{"theme":"dark"}`)
	mustWrite(".config/opencode/skill/test/SKILL.md", "opencode skill\n")
	mustWrite(".local/share/opencode/auth.json", `{"github-copilot":{"access":"secret-opencode"}}`)
	mustWrite(".gitconfig", "[user]\n\tname = Test\n")

	// Symlink ~/.config/tmux/tmux.conf -> dotfiles/tmux.conf (top-level dotfile symlink must be followed).
	dotfilesTmux := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(dotfilesTmux, []byte("set -g mouse on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "tmux"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfilesTmux, filepath.Join(home, ".config", "tmux", "tmux.conf")); err != nil {
		t.Fatal(err)
	}

	// Add a symlink inside .claude/skills to ensure symlinks inside walked dirs are skipped safely.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".claude/skills/symlink.txt")); err != nil {
		t.Fatal(err)
	}

	if got, want := DetectHostConfigs(home), []string{"claude", "codex", "opencode", "tmux", "git"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectHostConfigs = %v, want %v", got, want)
	}
	if got, want := DetectHostCredentials(home), []string{"claude", "codex", "opencode"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectHostCredentials = %v, want %v", got, want)
	}

	importedCfg, err := ImportHostConfigs(dataDir, home, "default")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude", "codex", "opencode", "tmux", "git"}; !reflect.DeepEqual(importedCfg, want) {
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
		filepath.Join(claudeProfile, "home", ".gitconfig"),
		filepath.Join(dataDir, "profiles", "codex", "default", "codex", "AGENTS.md"),
		filepath.Join(dataDir, "profiles", "codex", "default", "codex", "rules", "default.rules"),
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
	codexCfg, err := os.ReadFile(filepath.Join(dataDir, "profiles", "codex", "default", "codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(codexCfg), "marketplaces") || !strings.Contains(string(codexCfg), `theme = "dark"`) {
		t.Fatalf("codex config.toml not sanitized: %s", codexCfg)
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

func TestImportOpenCodeTUIOnly(t *testing.T) {
	for _, name := range []string{"tui.json", "tui.jsonc", "themes/custom.json"} {
		t.Run(name, func(t *testing.T) {
			home, data := t.TempDir(), t.TempDir()
			src := filepath.Join(home, ".config", "opencode", name)
			if err := os.MkdirAll(filepath.Dir(src), 0700); err != nil {
				t.Fatal(err)
			}
			content := []byte(`{"theme":"custom","keybinds":{"leader":"ctrl+a"}}`)
			if err := os.WriteFile(src, content, 0600); err != nil {
				t.Fatal(err)
			}
			if got := DetectHostConfigs(home); !reflect.DeepEqual(got, []string{"opencode"}) {
				t.Fatalf("detected = %v", got)
			}
			if _, err := ImportHostConfigs(data, home, "personal"); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(data, "profiles", "opencode", "personal", "config", "opencode", name)
			got, err := os.ReadFile(dst)
			if err != nil || string(got) != string(content) {
				t.Fatalf("import = %q, %v", got, err)
			}
		})
	}
}
