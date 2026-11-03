// Package agent describes native coding clients independently of Docker and Cobra.
package agent

import (
	"fmt"
	"slices"
	"strings"
)

// ProfilePath is the stable container-visible mount point for native client state.
const ProfilePath = "/hewitt/profile"

// DefaultImage is the unified workspace image used when no image is specified.
const DefaultImage = "ghcr.io/hewitt-iso/hewitt:latest"

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
	// DefaultImage is empty when the caller must select an image explicitly.
	DefaultImage() string
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

// Names lists the implemented adapters in display order. Callers that mount
// profiles or build allowlists for every agent iterate over it.
var Names = []string{"claude", "codex", "opencode", "pi", "antigravity"}

// Lookup selects an implemented adapter. Unsupported clients fail explicitly.
func Lookup(name string) (Adapter, error) {
	switch name {
	case "claude":
		return Claude{}, nil
	case "codex":
		return Codex{}, nil
	case "opencode":
		return OpenCode{}, nil
	case "pi":
		return Pi{}, nil
	case "antigravity":
		return Antigravity{}, nil
	default:
		return nil, fmt.Errorf("agent %q has no implemented adapter (supported: %s)", name, strings.Join(Names, ", "))
	}
}

// Claude uses the unmodified Claude Code binary and its own subscription login.
type Claude struct{}

func (Claude) DefaultImage() string { return DefaultImage }

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

func (Codex) DefaultImage() string { return DefaultImage }

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

// OpenCode uses its native provider login (opencode auth login): for example
// GitHub Copilot device login or ChatGPT Plus/Pro, or provider API keys from an
// env file. Its credentials live in XDG_DATA_HOME/opencode/auth.json, which is
// inside the profile.
type OpenCode struct{}

func (OpenCode) DefaultImage() string { return DefaultImage }

func (OpenCode) Name() string       { return "opencode" }
func (OpenCode) Run() Invocation    { return openCodeInvocation() }
func (OpenCode) Login() Invocation  { return openCodeInvocation("auth", "login") }
func (OpenCode) Status() Invocation { return openCodeInvocation("auth", "list") }
func (OpenCode) Logout() Invocation { return openCodeInvocation("auth", "logout") }
func (OpenCode) ConfigDir() string  { return ProfilePath + "/config/opencode" }
func (OpenCode) InstructionFile() string {
	return ProfilePath + "/config/opencode/AGENTS.md"
}
func (OpenCode) TokenVariable() string { return "" }
func (OpenCode) Hosts() []string {
	return []string{
		"opencode.ai", "models.dev",
		"github.com", "api.github.com", "*.githubcopilot.com",
		"api.openai.com", "chatgpt.com", "auth.openai.com",
	}
}

func openCodeInvocation(args ...string) Invocation {
	// Provider keys baked into images are removed; keys from an env file are
	// kept by Invocation.Allow. Disabling default plugins would hide the
	// built-in subscription logins, so that override is removed too.
	return invocation("opencode", args, []string{"OPENCODE_DISABLE_AUTOUPDATE=1"}, []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY",
		"OPENROUTER_API_KEY", "XAI_API_KEY", "GITHUB_TOKEN", "GITLAB_TOKEN",
		"OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT",
	})
}

// Pi (pi.dev) logs in only through its /login command; hewitt-agent slash
// starts it and types the command. Credentials live in PI_CODING_AGENT_DIR,
// inside the profile, as auth.json (0600).
type Pi struct{}

func (Pi) DefaultImage() string    { return DefaultImage }
func (Pi) Name() string            { return "pi" }
func (Pi) Run() Invocation         { return piInvocation("pi") }
func (Pi) Login() Invocation       { return piInvocation("slash", "pi", "/login") }
func (Pi) Status() Invocation      { return piInvocation("pi", "--list-models") }
func (Pi) Logout() Invocation      { return piInvocation("slash", "pi", "/logout") }
func (Pi) ConfigDir() string       { return ProfilePath + "/pi" }
func (Pi) InstructionFile() string { return ProfilePath + "/pi/AGENTS.md" }
func (Pi) TokenVariable() string   { return "" }
func (Pi) Hosts() []string {
	return []string{
		"registry.npmjs.org", "github.com", "api.github.com", "objects.githubusercontent.com",
		"api.anthropic.com", "claude.ai", "console.anthropic.com",
		"api.openai.com", "auth.openai.com", "chatgpt.com",
		"*.githubcopilot.com", "accounts.google.com", "oauth2.googleapis.com",
		"www.googleapis.com", "cloudcode-pa.googleapis.com", "generativelanguage.googleapis.com",
	}
}

func piInvocation(binary string, args ...string) Invocation {
	return invocation(binary, args, []string{"PI_CODING_AGENT_DIR=" + ProfilePath + "/pi", "PI_SKIP_VERSION_CHECK=1"}, []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"OPENROUTER_API_KEY", "XAI_API_KEY", "GROQ_API_KEY", "MISTRAL_API_KEY",
		"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "PI_CODING_AGENT_SESSION_DIR",
	})
}

// Antigravity runs Google's agy CLI. agy keeps its login in the Secret Service
// keyring, so hewitt-agent starts a private D-Bus session with gnome-keyring
// whose keyring file lives in the profile's XDG_DATA_HOME. Plain agy opens
// its sign-in menu when the profile has no login.
type Antigravity struct{}

func (Antigravity) DefaultImage() string    { return DefaultImage }
func (Antigravity) Name() string            { return "antigravity" }
func (Antigravity) Run() Invocation         { return antigravityInvocation("agy") }
func (Antigravity) Login() Invocation       { return antigravityInvocation("agy") }
func (Antigravity) Status() Invocation      { return antigravityInvocation("agy", "models") }
func (Antigravity) Logout() Invocation      { return antigravityInvocation("slash", "agy", "/logout") }
func (Antigravity) ConfigDir() string       { return ProfilePath + "/home/.gemini" }
func (Antigravity) InstructionFile() string { return ProfilePath + "/home/.gemini/GEMINI.md" }
func (Antigravity) TokenVariable() string   { return "" }
func (Antigravity) Hosts() []string {
	return []string{
		"accounts.google.com", "oauth2.googleapis.com", "www.googleapis.com",
		"cloudcode-pa.googleapis.com", "daily-cloudcode-pa.googleapis.com",
		"generativelanguage.googleapis.com", "antigravity.google",
		"antigravity-cli-auto-updater-974169037036.us-central1.run.app",
	}
}

func antigravityInvocation(binary string, args ...string) Invocation {
	return invocation(binary, args, nil, []string{
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "ANTIGRAVITY_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS",
		"DBUS_SESSION_BUS_ADDRESS",
	})
}

func invocation(binary string, args, env, unset []string) Invocation {
	// env -u removes conflicting credentials even if baked into an image's ENV.
	// Arguments are passed directly, never interpolated into a shell command.
	command := make([]string, 0, len(unset)*2+2+len(args))
	for _, key := range unset {
		command = append(command, "-u", key)
	}
	command = append(command, "hewitt-agent", binary)
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
