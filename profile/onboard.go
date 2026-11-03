package profile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// ShouldSyncConfigs reports whether host configs were opted into during
// onboarding and should be refreshed before starting a session.
func ShouldSyncConfigs(dataDir string) bool {
	if !filepath.IsAbs(dataDir) {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, onboardedFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(raw)) == "1"
}

// MarkOnboarded records that initial onboarding has completed for dataDir.
func MarkOnboarded(dataDir string) error {
	return markOnboardedWithValue(dataDir, "1\n")
}

// MarkOnboardedNoSync records that initial onboarding completed with a clean
// profile (host config sync disabled).
func MarkOnboardedNoSync(dataDir string) error {
	return markOnboardedWithValue(dataDir, "no-sync\n")
}

func markOnboardedWithValue(dataDir, val string) error {
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
	if err := os.WriteFile(marker, []byte(val), 0o600); err != nil {
		return fmt.Errorf("write onboarding marker: %w", err)
	}
	return nil
}

// DetectHostConfigs returns the names of host tool configurations (claude,
// codex, opencode, tmux, git, shell) found in homeDir. Top-level dotfile
// symlinks in homeDir are followed so symlinked dotfiles are detected.
func DetectHostConfigs(homeDir string) []string {
	if !filepath.IsAbs(homeDir) {
		return nil
	}
	var found []string
	if hasAnyTarget(homeDir,
		".claude/CLAUDE.md",
		".claude/settings.json",
		".claude/skills",
		".claude/commands",
		".claude/agents",
	) || hasMCPServers(filepath.Join(homeDir, ".claude.json")) {
		found = append(found, "claude")
	}
	if hasAnyTarget(homeDir,
		".codex/AGENTS.md",
		".codex/instructions.md",
		".codex/config.toml",
		".codex/skills",
		".codex/rules",
	) {
		found = append(found, "codex")
	}
	if hasAnyTarget(homeDir,
		".config/opencode/opencode.json",
		".config/opencode/opencode.jsonc",
		".config/opencode/tui.json",
		".config/opencode/tui.jsonc",
		".config/opencode/themes",
		".config/opencode/AGENTS.md",
		".config/opencode/skill",
		".config/opencode/skills",
		".config/opencode/command",
		".config/opencode/agent",
	) {
		found = append(found, "opencode")
	}
	if hasAnyTarget(homeDir, ".tmux.conf", ".config/tmux/tmux.conf", ".config/tmux") {
		found = append(found, "tmux")
	}
	if hasAnyTarget(homeDir, ".gitconfig", ".config/git/config") {
		found = append(found, "git")
	}
	return found
}

// ImportHostConfigs copies non-credential host configurations (skills, MCP
// servers, rules, commands, tmux config, and gitconfig/bashrc) from homeDir
// into the private Hewitt profiles under dataDir. Top-level dotfile symlinks
// are resolved; symlinks inside walked subdirectories are skipped.
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
		copied, err := copySafe(src, dst, true)
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

	// 2. Codex configs (rules, sanitized config.toml, skills, UI state).
	codexImported := false
	for _, rel := range []string{"AGENTS.md", "instructions.md", "skills", "rules", ".codex-global-state.json"} {
		src := filepath.Join(homeDir, ".codex", rel)
		dst := filepath.Join(dirs["codex"], "codex", rel)
		copied, err := copySafe(src, dst, true)
		if err != nil {
			return nil, fmt.Errorf("import codex %s: %w", rel, err)
		}
		if copied {
			codexImported = true
		}
	}
	if copied, err := importCodexConfig(filepath.Join(homeDir, ".codex", "config.toml"), filepath.Join(dirs["codex"], "codex", "config.toml")); err != nil {
		return nil, fmt.Errorf("import codex config.toml: %w", err)
	} else if copied {
		codexImported = true
	}
	if codexImported {
		imported = append(imported, "codex")
	}

	// 3. OpenCode configs (opencode.json, rules, skills, commands, agents).
	opencodeImported := false
	for _, rel := range []string{"opencode.json", "opencode.jsonc", "tui.json", "tui.jsonc", "themes", "AGENTS.md", "skill", "skills", "command", "commands", "agent", "agents"} {
		src := filepath.Join(homeDir, ".config", "opencode", rel)
		dst := filepath.Join(dirs["opencode"], "config", "opencode", rel)
		copied, err := copySafe(src, dst, true)
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

	// 4. tmux configuration (~/.tmux.conf and ~/.config/tmux/).
	tmuxImported := false
	tmuxSrc := filepath.Join(homeDir, ".tmux.conf")
	if !isTargetFile(tmuxSrc) {
		tmuxSrc = filepath.Join(homeDir, ".config", "tmux", "tmux.conf")
	}
	if isTargetFile(tmuxSrc) {
		for _, client := range []string{"claude", "codex", "opencode"} {
			dst := filepath.Join(dirs[client], "home", ".tmux.conf")
			if copied, err := copySafe(tmuxSrc, dst, true); err != nil {
				return nil, fmt.Errorf("import tmux config: %w", err)
			} else if copied {
				tmuxImported = true
			}
		}
	}
	tmuxConfigDir := filepath.Join(homeDir, ".config", "tmux")
	if info, err := os.Stat(tmuxConfigDir); err == nil && info.IsDir() {
		for _, client := range []string{"claude", "codex", "opencode"} {
			dst := filepath.Join(dirs[client], "config", "tmux")
			if copied, err := copySafe(tmuxConfigDir, dst, true); err != nil {
				return nil, fmt.Errorf("import tmux config dir: %w", err)
			} else if copied {
				tmuxImported = true
			}
		}
	}
	if tmuxImported {
		imported = append(imported, "tmux")
	}

	// 5. Git & shell defaults (~/.gitconfig, ~/.config/git/config, ~/.bashrc).
	gitImported := false
	for _, client := range []string{"claude", "codex", "opencode"} {
		if copied, err := copySafe(filepath.Join(homeDir, ".gitconfig"), filepath.Join(dirs[client], "home", ".gitconfig"), true); err != nil {
			return nil, fmt.Errorf("import .gitconfig: %w", err)
		} else if copied {
			gitImported = true
		}
		if copied, err := copySafe(filepath.Join(homeDir, ".config", "git"), filepath.Join(dirs[client], "config", "git"), true); err != nil {
			return nil, fmt.Errorf("import git config dir: %w", err)
		} else if copied {
			gitImported = true
		}
		_, _ = copySafe(filepath.Join(homeDir, ".bashrc"), filepath.Join(dirs[client], "home", ".bashrc"), true)
		_, _ = copySafe(filepath.Join(homeDir, ".bashrc.d"), filepath.Join(dirs[client], "home", ".bashrc.d"), true)
		_, _ = copySafe(filepath.Join(homeDir, ".bash_aliases"), filepath.Join(dirs[client], "home", ".bash_aliases"), true)
	}
	if gitImported {
		imported = append(imported, "git")
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
	if isTargetFile(filepath.Join(homeDir, ".claude", ".credentials.json")) {
		found = append(found, "claude")
	}
	if isTargetFile(filepath.Join(homeDir, ".codex", "auth.json")) {
		found = append(found, "codex")
	}
	if isTargetFile(filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")) {
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
		if !isTargetFile(spec.src) {
			continue
		}
		dir, err := Prepare(dataDir, spec.agent, profileID)
		if err != nil {
			return nil, err
		}
		copied, err := copySafe(spec.src, spec.dst(dir), true)
		if err != nil {
			return nil, fmt.Errorf("import %s credentials: %w", spec.agent, err)
		}
		if copied {
			imported = append(imported, spec.agent)
		}
	}
	return imported, nil
}

func hasAnyTarget(base string, rels ...string) bool {
	for _, rel := range rels {
		info, err := os.Stat(filepath.Join(base, rel))
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() || info.IsDir() {
			return true
		}
	}
	return false
}

func isTargetFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func hasMCPServers(path string) bool {
	if !isTargetFile(path) {
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
	if !isTargetFile(src) {
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

// importCodexConfig copies ~/.codex/config.toml while stripping host-only
// desktop app sections ([marketplaces.*], [plugins.*], [mcp_servers.node_repl*])
// whose binaries/paths only exist in the host ChatGPT desktop app bundle.
func importCodexConfig(src, dst string) (bool, error) {
	if !isTargetFile(src) {
		return false, nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	var kept []string
	skipSection := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			header := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
			skipSection = strings.HasPrefix(header, "marketplaces.") ||
				strings.HasPrefix(header, "plugins.") ||
				strings.HasPrefix(header, "mcp_servers.node_repl") ||
				header == "desktop"
		}
		if !skipSection {
			kept = append(kept, line)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	out := strings.TrimSpace(strings.Join(kept, "\n")) + "\n"
	if err := os.WriteFile(dst, []byte(out), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// copySafe copies a file or directory tree from src to dst with 0700 directory
// permissions and 0600 file permissions. If allowRootSymlink is true, a
// top-level dotfile symlink is resolved; symlinks inside directories are
// always skipped.
func copySafe(src, dst string, allowRootSymlink bool) (bool, error) {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if !allowRootSymlink {
			return false, nil
		}
		resolved, err := filepath.EvalSymlinks(src)
		if err != nil {
			return false, nil
		}
		src = resolved
		info, err = os.Lstat(src)
		if err != nil {
			return false, nil
		}
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
