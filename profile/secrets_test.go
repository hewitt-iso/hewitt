package profile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestImportRemovesSecretValues(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const pat = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	write(".claude.json", `{"mcpServers":{"github":{"command":"npx","args":["--api-key","plain-secret-1","--verbose"],"env":{"GITHUB_TOKEN":"`+pat+`","GITHUB_HOST":"github.com","LINEAR_API_KEY":"${LINEAR_API_KEY}"},"headers":{"Authorization":"Bearer plain-secret-2"}}}}`)
	write(".claude/settings.json", `{"env":{"OPENAI_API_KEY":"plain-secret-3","EDITOR":"vim"},"model":"opus"}`)
	write(".claude/skills/deploy/SKILL.md", "Deploy with --token=plain-secret-4 to https://bot:plain-secret-5@example.com/hook\n")
	write(".claude/skills/deploy/.env", "API_KEY=plain-secret-6\n")
	write(".claude/skills/deploy/.env.example", "API_KEY=\n")
	write(".claude/skills/deploy/server.pem", "-----BEGIN PRIVATE KEY-----\n")
	write(".codex/config.toml", "model = \"gpt-5\"\ncli_auth_credentials_store = \"file\"\n[mcp_servers.linear]\nbearer_token = \"plain-secret-7\"\nbearer_token_env_var = \"LINEAR_TOKEN\"\nenv = { SENTRY_AUTH_TOKEN = \"plain-secret-8\", REGION = \"eu\" }\n")
	write(".config/opencode/opencode.jsonc", "{\n  // providers\n  \"provider\": {\"openai\": {\"options\": {\"apiKey\": \"plain-secret-9\", \"baseURL\": \"https://api.openai.com\"}}},\n  \"theme\": \"dark\"\n}\n")
	write(".gitconfig", "[user]\n\tname = Test\n[http]\n\textraheader = AUTHORIZATION: basic plain-secret-10\n[url \"https://user:plain-secret-11@git.example.com/\"]\n\tinsteadOf = https://git.example.com/\n")
	write(".config/git/credentials", "https://user:plain-secret-12@github.com\n")
	write(".bashrc", "export PATH=$HOME/bin:$PATH\nexport ANTHROPIC_API_KEY=plain-secret-13\nexport GH_TOKEN=\"$(cat ~/.gh-token)\"\nalias ll='ls -l'\n")

	_, redacted, err := ImportHostConfigs(data, home, "default", false)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	err = filepath.WalkDir(filepath.Join(data, "profiles"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		all.WriteString(path + "\n" + string(b) + "\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got := all.String()
	if strings.Contains(got, "plain-secret") || strings.Contains(got, pat) {
		t.Fatalf("secret value imported:\n%s", got)
	}
	for _, kept := range []string{`"GITHUB_HOST": "github.com"`, `"${LINEAR_API_KEY}"`, `"--verbose"`, `"EDITOR":"vim"`, `cli_auth_credentials_store = "file"`,
		`bearer_token_env_var = "LINEAR_TOKEN"`, `REGION = "eu"`, `"baseURL": "https://api.openai.com"`, "// providers", "name = Test",
		"insteadOf = https://git.example.com/", "export PATH=$HOME/bin:$PATH", `GH_TOKEN="$(cat ~/.gh-token)"`, "alias ll='ls -l'",
		"skills/deploy/.env.example"} {
		if !strings.Contains(got, kept) {
			t.Errorf("non-secret setting lost: %q", kept)
		}
	}
	for _, missing := range []string{"skills/deploy/.env\n", "server.pem", "config/git/credentials"} {
		if strings.Contains(got, missing) {
			t.Errorf("credential file copied: %s", missing)
		}
	}
	for _, want := range []string{"~/.claude.json: GITHUB_TOKEN", "~/.claude.json: Authorization", "~/.claude.json: --api-key",
		"~/.claude/settings.json: OPENAI_API_KEY", "~/.codex/config.toml: bearer_token", "~/.codex/config.toml: SENTRY_AUTH_TOKEN",
		"~/.config/opencode/opencode.jsonc: apiKey", "~/.gitconfig: extraheader", "~/.gitconfig: URL credentials",
		"~/.bashrc: ANTHROPIC_API_KEY", "~/.claude/skills/deploy/.env: file not copied", "~/.config/git/credentials: file not copied"} {
		if !slices.Contains(redacted, want) {
			t.Errorf("redactions = %q, missing %q", redacted, want)
		}
	}

	// Keeping secrets copies the values but still never copies credential files.
	keep := t.TempDir()
	if _, redacted, err := ImportHostConfigs(keep, home, "default", true); err != nil || len(redacted) != 3 {
		t.Fatalf("keep redactions = %q, %v", redacted, err)
	}
	b, err := os.ReadFile(filepath.Join(keep, "profiles", "claude", "default", "home", ".claude.json"))
	if err != nil || !strings.Contains(string(b), pat) {
		t.Fatalf("kept secret missing: %s, %v", b, err)
	}
}
