package agent

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuiltinManifests(t *testing.T) {
	r, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Names(); !slices.Equal(got, []string{"claude", "codex", "opencode", "pi", "antigravity"}) {
		t.Fatalf("names = %v", got)
	}
	if got := r.TaskAgents(); !slices.Equal(got, []string{"claude", "codex", "opencode", "pi"}) {
		t.Fatalf("task agents = %v", got)
	}
	for _, m := range r.All() {
		if m.ImageFor() != DefaultImage || m.Herdr == "" {
			t.Errorf("%s: image %q, herdr %q", m.Name, m.ImageFor(), m.Herdr)
		}
	}
	claude, _ := r.Lookup("claude")
	if claude.Token == nil || claude.Token.Variable != "CLAUDE_CODE_OAUTH_TOKEN" || !slices.Equal(claude.Token.Hosts, []string{"api.anthropic.com"}) {
		t.Fatalf("claude token = %+v", claude.Token)
	}
	if _, err := r.Lookup("gemini"); !errors.As(err, new(*UnknownAgentError)) {
		t.Fatalf("Lookup(gemini) = %v", err)
	}
}

// profileEnv is the start of every launch in the profile at p.
func profileEnv(p string) []string { return ProfileEnv(p) }

// TestLaunchesMatchTheAdapters pins each built-in client's command lines to
// the ones Hewitt's adapters and hewitt-agent built before manifests.
func TestLaunchesMatchTheAdapters(t *testing.T) {
	r, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	const p = "/hewitt/profiles/x"
	claudeUnset := []string{"-u", "ANTHROPIC_API_KEY", "-u", "ANTHROPIC_AUTH_TOKEN", "-u", "ANTHROPIC_BASE_URL",
		"-u", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "-u", "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR",
		"-u", "CLAUDE_CODE_USE_BEDROCK", "-u", "CLAUDE_CODE_USE_VERTEX", "-u", "CLAUDE_CODE_USE_FOUNDRY"}
	claudeEnv := append(profileEnv(p), "CLAUDE_CONFIG_DIR="+p+"/claude", "DISABLE_AUTOUPDATER=1")
	codexUnset := []string{"-u", "OPENAI_API_KEY", "-u", "CODEX_API_KEY", "-u", "CODEX_ACCESS_TOKEN", "-u", "OPENAI_BASE_URL",
		"-u", "OPENAI_IDENTITY_TOKEN_FILE", "-u", "OPENAI_WORKLOAD_IDENTITY_PROVIDER"}
	codexEnv := append(profileEnv(p), "CODEX_HOME="+p+"/codex")
	login := []string{"-c", `cli_auth_credentials_store="file"`, "-c", `forced_login_method="chatgpt"`}
	trust := []string{"-c", `projects."/work".trust_level="trusted"`}
	opencodeEnv := append(profileEnv(p), "OPENCODE_DISABLE_AUTOUPDATE=1")
	piEnv := append(profileEnv(p), "PI_CODING_AGENT_DIR="+p+"/pi", "PI_SKIP_VERSION_CHECK=1")
	join := func(parts ...[]string) []string { return slices.Concat(parts...) }
	for _, tt := range []struct {
		agent, op string
		o         LaunchOptions
		want      []string
		slash     string
	}{
		{"claude", OpRun, LaunchOptions{Allow: []string{"CLAUDE_CODE_OAUTH_TOKEN"}}, join([]string{"env"}, claudeUnset, claudeEnv, []string{"claude"}), ""},
		{"claude", OpRun, LaunchOptions{Prompt: "fix\nit", Headless: true}, join([]string{"env"}, claudeUnset[:6], []string{"-u", "CLAUDE_CODE_OAUTH_TOKEN"}, claudeUnset[6:], claudeEnv,
			[]string{"claude", "-p", "--permission-mode", "bypassPermissions", "--", "fix\nit"}), ""},
		{"claude", OpLogin, LaunchOptions{}, join([]string{"env"}, claudeUnset[:6], []string{"-u", "CLAUDE_CODE_OAUTH_TOKEN"}, claudeUnset[6:], claudeEnv, []string{"claude", "setup-token"}), ""},
		{"codex", OpRun, LaunchOptions{Prompt: "go"}, join([]string{"env"}, codexUnset, codexEnv, []string{"codex", "--sandbox", "danger-full-access"}, login, trust, []string{"--", "go"}), ""},
		{"codex", OpRun, LaunchOptions{Prompt: "go", Headless: true, Telemetry: "https://otel.example"}, join([]string{"env"}, codexUnset, codexEnv,
			[]string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox"}, login, trust,
			[]string{"-c", `otel.exporter={otlp-http={endpoint="https://otel.example/v1/logs",protocol="binary"}}`,
				"-c", `otel.trace_exporter={otlp-http={endpoint="https://otel.example/v1/traces",protocol="binary"}}`, "--", "go"}), ""},
		{"codex", OpLogin, LaunchOptions{}, join([]string{"env"}, codexUnset, codexEnv, []string{"codex"}, login, []string{"login", "--device-auth"}), ""},
		{"opencode", OpRun, LaunchOptions{Prompt: "go", Allow: []string{"GEMINI_API_KEY"}}, join([]string{"env", "-u", "ANTHROPIC_API_KEY", "-u", "OPENAI_API_KEY",
			"-u", "GOOGLE_GENERATIVE_AI_API_KEY", "-u", "OPENROUTER_API_KEY", "-u", "XAI_API_KEY", "-u", "GITHUB_TOKEN", "-u", "GITLAB_TOKEN",
			"-u", "OPENCODE_DISABLE_DEFAULT_PLUGINS", "-u", "OPENCODE_CONFIG", "-u", "OPENCODE_CONFIG_CONTENT"}, opencodeEnv, []string{"opencode", "--prompt=go"}), ""},
		{"pi", OpRun, LaunchOptions{Prompt: "go", Headless: true}, nil, ""},
		{"pi", OpLogin, LaunchOptions{}, nil, "/login"},
		{"pi", OpStatus, LaunchOptions{}, nil, ""},
		{"antigravity", OpRun, LaunchOptions{}, nil, ""},
		{"antigravity", OpLogout, LaunchOptions{}, nil, "/logout"},
	} {
		t.Run(tt.agent+" "+tt.op, func(t *testing.T) {
			m, err := r.Lookup(tt.agent)
			if err != nil {
				t.Fatal(err)
			}
			tt.o.Profile, tt.o.Workdir = p, "/work"
			l, err := m.Launch(tt.op, tt.o)
			if err != nil {
				t.Fatal(err)
			}
			if l.Slash != tt.slash {
				t.Errorf("slash = %q, want %q", l.Slash, tt.slash)
			}
			if tt.want != nil && !slices.Equal(l.Argv, tt.want) {
				t.Errorf("argv =\n%q\nwant\n%q", l.Argv, tt.want)
			}
			switch tt.agent + " " + tt.op {
			case "pi run":
				if !slices.Equal(l.Argv[len(l.Argv)-6:], []string{"PI_CODING_AGENT_DIR=" + p + "/pi", "PI_SKIP_VERSION_CHECK=1", "pi", "-p", "--", "go"}) {
					t.Errorf("argv = %q", l.Argv)
				}
			case "pi status":
				if !slices.Equal(l.Argv[len(l.Argv)-len(piEnv)-2:], append(slices.Clone(piEnv), "pi", "--list-models")) {
					t.Errorf("argv = %q", l.Argv)
				}
			case "antigravity run":
				i := slices.Index(l.Argv, "dbus-run-session")
				if i < 0 || !slices.Equal(l.Argv[len(l.Argv)-2:], []string{"keyring", "agy"}) || !slices.Contains(l.Argv[:i], "-u") {
					t.Errorf("argv = %q", l.Argv)
				}
			}
		})
	}
}

func TestLaunchRefusesWhatTheClientCannotDo(t *testing.T) {
	r, _ := Load("")
	agy, _ := r.Lookup("antigravity")
	if _, err := agy.Launch(OpRun, LaunchOptions{Prompt: "go"}); err == nil || !strings.Contains(err.Error(), "does not accept a task") {
		t.Fatalf("task for agy: %v", err)
	}
	codex, _ := r.Lookup("codex")
	if _, err := codex.Launch(OpRun, LaunchOptions{Headless: true}); err == nil {
		t.Fatal("headless run without a task accepted")
	}
	if _, err := codex.Launch(OpLogin, LaunchOptions{Prompt: "go"}); err == nil {
		t.Fatal("login with a task accepted")
	}
	// Without a profile, settings that name it are left out.
	l, err := codex.Launch(OpRun, LaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(l.Argv, func(a string) bool { return strings.HasPrefix(a, "CODEX_HOME=") || strings.HasPrefix(a, "HOME=") }) {
		t.Fatalf("argv = %q", l.Argv)
	}
	if !strings.HasPrefix(l.Marker, "{workdir:") || !slices.ContainsFunc(l.Argv, func(a string) bool { return strings.Contains(a, l.Marker) }) {
		t.Fatalf("marker %q not in %q", l.Marker, l.Argv)
	}
}

func TestLaunchEnvironment(t *testing.T) {
	l := Launch{Argv: []string{"env", "a b", ""}, Slash: "/login", Marker: "{workdir:x}"}
	env := l.Env()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(env[0], "HEWITT_LAUNCH="))
	if err != nil || string(raw) != "env\x00a b\x00\x00" {
		t.Fatalf("launch = %q, %v", raw, err)
	}
	if !slices.Equal(env[1:], []string{"HEWITT_WORKDIR_MARK={workdir:x}", "HEWITT_SLASH=/login"}) {
		t.Fatalf("env = %q", env)
	}
	menu := Pick(map[string]Launch{"my-agent": l}, []string{"my-agent"})
	if menu[0] != "HEWITT_AGENTS=my-agent" || !strings.HasPrefix(menu[1], "HEWITT_LAUNCH_MY_AGENT=") || menu[2] != "HEWITT_LAUNCH_MY_AGENT_MARK={workdir:x}" {
		t.Fatalf("menu = %q", menu)
	}
	r, _ := Load("")
	for name, want := range map[string]string{
		"pi":          "npm\x00pi\x00@earendil-works/pi-coding-agent@latest\x00--ignore-scripts\x00",
		"claude":      "script\x00claude\x00https://claude.ai/install.sh\x00.local/bin/claude\x00stable\x00",
		"antigravity": "script\x00agy\x00https://antigravity.google/cli/install.sh\x00bin/agy\x00--dir\x00{tmp}/bin\x00",
	} {
		m, _ := r.Lookup(name)
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(m.InstallEnv()[0], "HEWITT_INSTALL="))
		if string(raw) != want {
			t.Errorf("%s install = %q, want %q", name, raw, want)
		}
	}
}

const userManifest = `version: 1
name: my-agent
binary: myagent
install: {npm: my-agent-cli, version: 1.2.3}
env: {MY_HOME: "{profile}/my"}
unset_env: [MY_API_KEY]
commands:
  run: [--yes]
  login: [login]
  status: [whoami]
  logout: {slash: /logout}
task: {interactive: ["{prompt}"], headless: [-p, "{prompt}"]}
config_dir: "{profile}/my"
instructions: "{profile}/my/RULES.md"
hosts: [api.example.com, "*.example.net"]
`

func TestUserManifests(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "agents")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("my-agent.yaml", userManifest)
	write("codex.yml", strings.NewReplacer("name: my-agent", "name: codex", "binary: myagent", "binary: codex").Replace(userManifest))
	write("notes.txt", "not a manifest")
	r, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Names(); !slices.Equal(got, []string{"claude", "codex", "opencode", "pi", "antigravity", "my-agent"}) {
		t.Fatalf("names = %v", got)
	}
	codex, _ := r.Lookup("codex")
	if codex.Source != filepath.Join(dir, "codex.yml") || codex.Install.NPM != "my-agent-cli" {
		t.Fatalf("codex = %+v", codex)
	}
	m, _ := r.Lookup("my-agent")
	l, err := m.Launch(OpRun, LaunchOptions{Profile: "/p", Prompt: "go", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := append(append([]string{"env", "-u", "MY_API_KEY"}, ProfileEnv("/p")...), "MY_HOME=/p/my", "myagent", "--yes", "-p", "go"); !slices.Equal(l.Argv, want) {
		t.Fatalf("argv = %q, want %q", l.Argv, want)
	}

	// Each change makes the manifest invalid; none is skipped silently.
	for _, tt := range []struct{ from, to string }{
		{"name: my-agent\n", "name: other\n"},
		{"hosts: [api.example.com, \"*.example.net\"]\n", ""},
		{"version: 1\n", ""},
		{"  run: [--yes]\n", "  run: [\"{prompt}\"]\n"},
		{"install: {npm: my-agent-cli, version: 1.2.3}\n", "install: {script: http://example.com/x.sh, output: x}\n"},
		{"config_dir: \"{profile}/my\"\n", "config_dir: /etc\n"},
		{"env: {MY_HOME: \"{profile}/my\"}\n", "env: {HEWITT_TASK: x}\n"},
		{"binary: myagent\n", "binary: ../bin/sh\n"},
		{"hosts: [api.example.com, \"*.example.net\"]\n", "hosts: [\"Example.com/x\"]\n"},
		{"version: 1\n", "version: 1\nshell: true\n"},
	} {
		write("my-agent.yaml", strings.Replace(userManifest, tt.from, tt.to, 1))
		if _, err := Load(data); !errors.As(err, new(*ManifestError)) {
			t.Errorf("%q -> %q: error = %v", tt.from, tt.to, err)
		}
	}
}
