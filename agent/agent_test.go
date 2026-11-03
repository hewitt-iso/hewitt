package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSubscriptionCommands(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			adapter, err := Lookup(name)
			if err != nil {
				t.Fatal(err)
			}
			inv := adapter.Login()
			dir := t.TempDir()
			script := `#!/bin/sh
if [ -n "${ANTHROPIC_API_KEY}${CLAUDE_CODE_OAUTH_TOKEN}${CODEX_API_KEY}${OPENAI_API_KEY}" ]; then exit 91; fi
printf '%s\n' "$@"
`
			if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			launcher := `#!/bin/sh
exec "$@"
`
			if err := os.WriteFile(filepath.Join(dir, "hewitt-agent"), []byte(launcher), 0o700); err != nil {
				t.Fatal(err)
			}
			env, err := exec.LookPath("env")
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(env, inv.Args...)
			command.Env = append(inv.Env, "PATH="+dir, "ANTHROPIC_API_KEY=synthetic", "CLAUDE_CODE_OAUTH_TOKEN=synthetic", "CODEX_API_KEY=synthetic", "OPENAI_API_KEY=synthetic")
			// Each adapter strips only its own provider's conflicting credentials.
			if name == "claude" {
				command.Env = append(inv.Env, "PATH="+dir, "ANTHROPIC_API_KEY=synthetic", "CLAUDE_CODE_OAUTH_TOKEN=synthetic")
			} else {
				command.Env = append(inv.Env, "PATH="+dir, "CODEX_API_KEY=synthetic", "OPENAI_API_KEY=synthetic")
			}
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("native command: %v, %s", err, out)
			}
			want := "setup-token\n"
			if name == "codex" {
				want = "-c\ncli_auth_credentials_store=\"file\"\n-c\nforced_login_method=\"chatgpt\"\nlogin\n--device-auth\n"
			}
			if string(out) != want {
				t.Fatalf("args = %q, want %q", out, want)
			}
			if strings.Contains(strings.Join(adapter.Run().Args, " "), "login") && name == "claude" {
				t.Fatal("run invokes login")
			}
		})
	}
}

func TestUnsupportedAdapter(t *testing.T) {
	for _, name := range []string{"gemini", "copilot", "../claude", ""} {
		if _, err := Lookup(name); err == nil {
			t.Fatalf("accepted unsupported adapter %q", name)
		}
	}
}

func TestNamesResolve(t *testing.T) {
	for _, name := range Names {
		adapter, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if adapter.Name() != name {
			t.Fatalf("Lookup(%q).Name() = %q", name, adapter.Name())
		}
		if adapter.DefaultImage() == "" || len(adapter.Hosts()) == 0 {
			t.Fatalf("%s: missing default image or egress hosts", name)
		}
		for _, host := range adapter.Hosts() {
			if host != strings.ToLower(host) || strings.ContainsAny(host, "/: ") {
				t.Fatalf("%s: invalid egress host %q", name, host)
			}
		}
		if !strings.HasPrefix(adapter.ConfigDir(), ProfilePath+"/") || !strings.HasPrefix(adapter.InstructionFile(), adapter.ConfigDir()+"/") {
			t.Fatalf("%s: rules paths %q, %q must live in the profile", name, adapter.ConfigDir(), adapter.InstructionFile())
		}
	}
}

// TestInterfaceLoginAdapters runs each invocation through a fake hewitt-agent
// that prints its arguments and the environment it received; both adapters
// remove a Gemini key baked into the image.
func TestInterfaceLoginAdapters(t *testing.T) {
	dir := t.TempDir()
	launcher := `#!/bin/sh
printf '%s\n' "$@"
printf 'dir=%s skip=%s key=%s\n' "$PI_CODING_AGENT_DIR" "$PI_SKIP_VERSION_CHECK" "$GEMINI_API_KEY"
`
	if err := os.WriteFile(filepath.Join(dir, "hewitt-agent"), []byte(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	env, err := exec.LookPath("env")
	if err != nil {
		t.Fatal(err)
	}
	piEnv := "dir=" + ProfilePath + "/pi skip=1 key=\n"
	for _, tt := range []struct {
		name string
		inv  Invocation
		want string
	}{
		{"pi run", Pi{}.Run(), "pi\n" + piEnv},
		{"pi login types /login", Pi{}.Login(), "slash\npi\n/login\n" + piEnv},
		{"pi status", Pi{}.Status(), "pi\n--list-models\n" + piEnv},
		{"pi logout types /logout", Pi{}.Logout(), "slash\npi\n/logout\n" + piEnv},
		{"antigravity run", Antigravity{}.Run(), "agy\ndir= skip= key=\n"},
		{"antigravity login opens sign-in", Antigravity{}.Login(), "agy\ndir= skip= key=\n"},
		{"antigravity status", Antigravity{}.Status(), "agy\nmodels\ndir= skip= key=\n"},
		{"antigravity logout", Antigravity{}.Logout(), "slash\nagy\n/logout\ndir= skip= key=\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.Command(env, tt.inv.Args...)
			command.Env = append(tt.inv.Env, "PATH="+dir, "GEMINI_API_KEY=synthetic")
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("invocation: %v, %s", err, out)
			}
			if string(out) != tt.want {
				t.Fatalf("output = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestAllowKeepsEnvFileKeys(t *testing.T) {
	inv := Pi{}.Run().Allow("ANTHROPIC_API_KEY")
	args := strings.Join(inv.Args, " ")
	if strings.Contains(args, "-u ANTHROPIC_API_KEY") || !strings.Contains(args, "-u OPENAI_API_KEY") {
		t.Fatalf("args = %q", args)
	}
}
