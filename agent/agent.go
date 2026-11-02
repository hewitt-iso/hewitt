// Package agent describes native coding clients independently of Docker and Cobra.
package agent

import (
	"fmt"
	"slices"
)

// ProfilePath is the stable container-visible mount point for native client state.
const ProfilePath = "/hewitt/profile"

// Invocation contains only non-secret launch configuration. Credentials belong
// to the native client's files, never to this structure.
type Invocation struct {
	Entrypoint string
	Args       []string
	Env        []string
}

// Allow keeps keys that the invocation would otherwise remove with env -u, so
// credentials supplied explicitly by the user, such as an API key from an env
// file, reach the client. Other conflicting variables are still removed.
func (inv Invocation) Allow(keys ...string) Invocation {
	args := make([]string, 0, len(inv.Args))
	for i := 0; i < len(inv.Args); i++ {
		if inv.Args[i] == "-u" && i+1 < len(inv.Args) && slices.Contains(keys, inv.Args[i+1]) {
			i++
			continue
		}
		args = append(args, inv.Args[i])
	}
	inv.Args = args
	return inv
}

// Adapter supplies native run and subscription-login commands for one client.
// Implementations do not read credentials or perform OAuth/refresh themselves.
type Adapter interface {
	Name() string
	Run() Invocation
	Login() Invocation
	Status() Invocation
	Logout() Invocation
	// ConfigDir is the container path where the client reads user-level rules,
	// commands, and settings; InstructionFile is its main rules file there.
	ConfigDir() string
	InstructionFile() string
	// TokenVariable names the environment variable that carries a long-lived
	// token printed by Login, or is empty when the client stores its own login.
	TokenVariable() string
	// Hosts are the provider endpoints a restricted session's egress proxy allows.
	Hosts() []string
}

// Lookup selects an implemented adapter. Unsupported clients fail explicitly.
func Lookup(name string) (Adapter, error) {
	switch name {
	case "claude":
		return Claude{}, nil
	case "codex":
		return Codex{}, nil
	default:
		return nil, fmt.Errorf("agent %q has no implemented adapter (supported: claude, codex)", name)
	}
}

// Claude uses the unmodified Claude Code binary and its own subscription login.
type Claude struct{}

func (Claude) Name() string    { return "claude" }
func (Claude) Run() Invocation { return claudeInvocation() }

// Login runs setup-token: the official subscription flow that prints a
// long-lived OAuth token. Sessions share it without refresh-token rotation.
func (Claude) Login() Invocation  { return claudeInvocation("setup-token") }
func (Claude) Status() Invocation { return claudeInvocation("auth", "status") }
func (Claude) Logout() Invocation { return claudeInvocation("auth", "logout") }
func (Claude) ConfigDir() string  { return ProfilePath + "/claude" }
func (Claude) TokenVariable() string {
	return "CLAUDE_CODE_OAUTH_TOKEN"
}
func (Claude) Hosts() []string {
	return []string{"api.anthropic.com", "claude.ai", "platform.claude.com", "console.anthropic.com"}
}
func (Claude) InstructionFile() string {
	return ProfilePath + "/claude/CLAUDE.md"
}

func claudeInvocation(args ...string) Invocation {
	return invocation("claude", args, []string{"CLAUDE_CONFIG_DIR=" + ProfilePath + "/claude"}, []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR",
		"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	})
}

// Codex uses a dedicated native file store so login survives container recreation.
type Codex struct{}

func (Codex) Name() string       { return "codex" }
func (Codex) Run() Invocation    { return codexInvocation() }
func (Codex) Login() Invocation  { return codexInvocation("login", "--device-auth") }
func (Codex) Status() Invocation { return codexInvocation("login", "status") }
func (Codex) Logout() Invocation { return codexInvocation("logout") }
func (Codex) ConfigDir() string  { return ProfilePath + "/codex" }
func (Codex) TokenVariable() string {
	return ""
}
func (Codex) Hosts() []string {
	return []string{"api.openai.com", "chatgpt.com", "auth.openai.com"}
}
func (Codex) InstructionFile() string {
	return ProfilePath + "/codex/AGENTS.md"
}

func codexInvocation(args ...string) Invocation {
	command := []string{"-c", `cli_auth_credentials_store="file"`, "-c", `forced_login_method="chatgpt"`}
	command = append(command, args...)
	return invocation("codex", command, []string{"CODEX_HOME=" + ProfilePath + "/codex"}, []string{
		"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_BASE_URL",
		"OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_WORKLOAD_IDENTITY_PROVIDER",
	})
}

func invocation(binary string, args, env, unset []string) Invocation {
	// env -u removes conflicting credentials even if baked into an image's ENV.
	// Arguments are passed directly, never interpolated into a shell command.
	command := make([]string, 0, len(unset)*2+1+len(args))
	for _, key := range unset {
		command = append(command, "-u", key)
	}
	command = append(command, binary)
	command = append(command, args...)
	base := []string{
		"HOME=" + ProfilePath + "/home",
		"XDG_CONFIG_HOME=" + ProfilePath + "/config",
		"XDG_DATA_HOME=" + ProfilePath + "/data",
		"XDG_STATE_HOME=" + ProfilePath + "/state",
		"XDG_CACHE_HOME=" + ProfilePath + "/cache",
	}
	return Invocation{Entrypoint: "env", Args: command, Env: append(base, env...)}
}
