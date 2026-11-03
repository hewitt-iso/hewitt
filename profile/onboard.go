package profile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const onboardedFile = ".onboarded"

// IsOnboarded reports whether initial onboarding has completed for dataDir.
// It never creates files or directories.
func IsOnboarded(dataDir string) bool {
	if !filepath.IsAbs(dataDir) {
		return false
	}
	info, err := os.Lstat(filepath.Join(dataDir, onboardedFile))
	return err == nil && info.Mode().IsRegular()
}

// MarkOnboarded records that initial onboarding has completed for dataDir.
func MarkOnboarded(dataDir string) error {
	if !filepath.IsAbs(dataDir) {
		return fmt.Errorf("profile data directory must be absolute")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	marker := filepath.Join(root, onboardedFile)
	if err := os.WriteFile(marker, []byte("1\n"), 0o600); err != nil {
		return fmt.Errorf("write onboarding marker: %w", err)
	}
	return nil
}

// DetectHostConfigs returns the names of host tool configurations (claude,
// codex, opencode, tmux) found in homeDir.
func DetectHostConfigs(homeDir string) []string {
	if !filepath.IsAbs(homeDir) {
		return nil
	}
	var found []string
	if hasAnyRegular(homeDir,
		".claude/CLAUDE.md",
		".claude/settings.json",
		".claude/skills",
		".claude/commands",
		".claude/agents",
	) || hasMCPServers(filepath.Join(homeDir, ".claude.json")) {
		found = append(found, "claude")
	}
	if hasAnyRegular(homeDir,
		".codex/AGENTS.md",
		".codex/instructions.md",
		".codex/config.toml",
		".codex/skills",
	) {
		found = append(found, "codex")
	}
	if hasAnyRegular(homeDir,
		".config/opencode/opencode.json",
		".config/opencode/opencode.jsonc",
		".config/opencode/AGENTS.md",
		".config/opencode/skill",
		".config/opencode/skills",
		".config/opencode/command",
		".config/opencode/agent",
	) {
		found = append(found, "opencode")
	}
	if hasAnyRegular(homeDir, ".tmux.conf", ".config/tmux/tmux.conf") {
		found = append(found, "tmux")
	}
	return found
}

// ImportHostConfigs copies non-credential host configurations (skills, MCP
// servers, rules, commands, and tmux.conf) from homeDir into the private Hewitt
// profiles under dataDir. Symlinks are ignored.
func ImportHostConfigs(dataDir, homeDir, profileID string) ([]string, error) {
	if !filepath.IsAbs(homeDir) {
		return nil, fmt.Errorf("home directory must be absolute")
	}
	if profileID == "" {
		profileID = "default"
	}
	dirs := map[string]string{}
	for _, client := range []string{"claude", "codex", "opencode"} {
		dir, err := Prepare(dataDir, client, profileID)
		if err != nil {
			return nil, err
		}
		dirs[client] = dir
	}

	var imported []string

	// 1. Claude Code configs (rules, settings, skills, commands, agents, MCP servers).
	claudeImported := false
	for _, rel := range []string{"CLAUDE.md", "settings.json", "skills", "commands", "agents"} {
		src := filepath.Join(homeDir, ".claude", rel)
		dst := filepath.Join(dirs["claude"], "claude", rel)
		copied, err := copySafe(src, dst)
		if err != nil {
			return nil, fmt.Errorf("import claude %s: %w", rel, err)
		}
		if copied {
			claudeImported = true
		}
	}
	if copied, err := importClaudeMCP(filepath.Join(homeDir, ".claude.json"), filepath.Join(dirs["claude"], "home", ".claude.json")); err != nil {
		return nil, fmt.Errorf("import claude MCP config: %w", err)
	} else if copied {
		claudeImported = true
	}
	if claudeImported {
		imported = append(imported, "claude")
	}

	// 2. Codex configs (rules, config.toml, skills).
	codexImported := false
	for _, rel := range []string{"AGENTS.md", "instructions.md", "config.toml", "skills"} {
		src := filepath.Join(homeDir, ".codex", rel)
		dst := filepath.Join(dirs["codex"], "codex", rel)
		copied, err := copySafe(src, dst)
		if err != nil {
			return nil, fmt.Errorf("import codex %s: %w", rel, err)
		}
		if copied {
			codexImported = true
		}
	}
	if codexImported {
		imported = append(imported, "codex")
	}

	// 3. OpenCode configs (opencode.json, rules, skills, commands, agents).
	opencodeImported := false
	for _, rel := range []string{"opencode.json", "opencode.jsonc", "AGENTS.md", "skill", "skills", "command", "agent"} {
		src := filepath.Join(homeDir, ".config", "opencode", rel)
		dst := filepath.Join(dirs["opencode"], "config", "opencode", rel)
		copied, err := copySafe(src, dst)
		if err != nil {
			return nil, fmt.Errorf("import opencode %s: %w", rel, err)
		}
		if copied {
			opencodeImported = true
		}
	}
	if opencodeImported {
		imported = append(imported, "opencode")
	}

	// 4. tmux configuration.
	tmuxSrc := filepath.Join(homeDir, ".tmux.conf")
	if !isRegularFile(tmuxSrc) {
		tmuxSrc = filepath.Join(homeDir, ".config", "tmux", "tmux.conf")
	}
	if isRegularFile(tmuxSrc) {
		for _, client := range []string{"claude", "codex", "opencode"} {
			dst := filepath.Join(dirs[client], "home", ".tmux.conf")
			if _, err := copySafe(tmuxSrc, dst); err != nil {
				return nil, fmt.Errorf("import tmux config: %w", err)
			}
		}
		imported = append(imported, "tmux")
	}

	return imported, nil
}

// DetectHostCredentials returns the names of agents (claude, codex, opencode)
// with existing credential files in homeDir.
func DetectHostCredentials(homeDir string) []string {
	if !filepath.IsAbs(homeDir) {
		return nil
	}
	var found []string
	if isRegularFile(filepath.Join(homeDir, ".claude", ".credentials.json")) {
		found = append(found, "claude")
	}
	if isRegularFile(filepath.Join(homeDir, ".codex", "auth.json")) {
		found = append(found, "codex")
	}
	if isRegularFile(filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")) {
		found = append(found, "opencode")
	}
	return found
}

// ImportHostCredentials copies existing host credential files for claude,
// codex, and opencode into their private Hewitt profile directories with mode
// 0600. Credential contents are never logged or returned.
func ImportHostCredentials(dataDir, homeDir, profileID string) ([]string, error) {
	if !filepath.IsAbs(homeDir) {
		return nil, fmt.Errorf("home directory must be absolute")
	}
	if profileID == "" {
		profileID = "default"
	}
	type credSpec struct {
		agent string
		src   string
		dst   func(profileDir string) string
	}
	specs := []credSpec{
		{
			agent: "claude",
			src:   filepath.Join(homeDir, ".claude", ".credentials.json"),
			dst:   func(dir string) string { return filepath.Join(dir, "claude", ".credentials.json") },
		},
		{
			agent: "codex",
			src:   filepath.Join(homeDir, ".codex", "auth.json"),
			dst:   func(dir string) string { return filepath.Join(dir, "codex", "auth.json") },
		},
		{
			agent: "opencode",
			src:   filepath.Join(homeDir, ".local", "share", "opencode", "auth.json"),
			dst:   func(dir string) string { return filepath.Join(dir, "data", "opencode", "auth.json") },
		},
	}
	var imported []string
	for _, spec := range specs {
		if !isRegularFile(spec.src) {
			continue
		}
		dir, err := Prepare(dataDir, spec.agent, profileID)
		if err != nil {
			return nil, err
		}
		copied, err := copySafe(spec.src, spec.dst(dir))
		if err != nil {
			return nil, fmt.Errorf("import %s credentials: %w", spec.agent, err)
		}
		if copied {
			imported = append(imported, spec.agent)
		}
	}
	return imported, nil
}

func hasAnyRegular(base string, rels ...string) bool {
	for _, rel := range rels {
		info, err := os.Lstat(filepath.Join(base, rel))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.Mode().IsRegular() || info.IsDir() {
			return true
		}
	}
	return false
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func hasMCPServers(path string) bool {
	if !isRegularFile(path) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	mcp, ok := raw["mcpServers"]
	return ok && len(mcp) > 2
}

// importClaudeMCP extracts only the mcpServers field from ~/.claude.json so
// MCP server definitions are imported without copying account state.
func importClaudeMCP(src, dst string) (bool, error) {
	if !isRegularFile(src) {
		return false, nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, nil
	}
	mcp, ok := raw["mcpServers"]
	if !ok || len(mcp) <= 2 {
		return false, nil
	}
	out, err := json.MarshalIndent(map[string]json.RawMessage{"mcpServers": mcp}, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, append(out, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// copySafe copies a regular file or directory tree from src to dst with
// 0700 directory permissions and 0600 file permissions, skipping any symlinks.
func copySafe(src, dst string) (bool, error) {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	if info.Mode().IsRegular() {
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return false, err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return false, err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return false, err
		}
		return true, nil
	}
	if !info.IsDir() {
		return false, nil
	}
	copiedAny := false
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
		copiedAny = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return copiedAny, nil
}
