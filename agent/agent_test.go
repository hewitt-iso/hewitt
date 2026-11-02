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
