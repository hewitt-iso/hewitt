package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/profile"
)

const adapterTestID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestLoginAndRunUseSamePersistentProfile(t *testing.T) {
	runRepository(t)
	bin, data := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$HEWITT_CALLS"
case "$2" in
create) printf '%s\n' '` + adapterTestID + `' ;;
start) if [ "$3" = "--attach" ]; then printf 'native login prompt'; fi ;;
inspect) printf '%s\n' '{"Status":"exited","ExitCode":0}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"claude", "codex"} {
		opts := AgentOptions{Agent: client, Image: "agent:test", Profile: "personal", DataDir: data, Workspace: "worktree", Danger: []string{"host-paths"}}
		var out, stderr bytes.Buffer
		// A file is passed to Docker directly, so the token remains to be read
		// after the attached login; a terminal behaves the same way.
		input := filepath.Join(t.TempDir(), "input")
		if err := os.WriteFile(input, []byte("synthetic-token\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		in, err := os.Open(input)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		if err := Login(context.Background(), opts, in, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		store, err := NewStore(data)
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := store.Get(adapterTestID)
		if err != nil || metadata.Profile != "personal" || metadata.Operation != "login" {
			t.Fatalf("login metadata: %+v, %v", metadata, err)
		}
		if out.String() != "native login prompt" || !strings.Contains(stderr.String(), adapterTestID) {
			t.Fatalf("login streams %q %q", out.String(), stderr.String())
		}
		state := filepath.Join(data, "profiles", client, "personal", client, "synthetic-state")
		if err := os.WriteFile(state, []byte("native"), 0o600); err != nil {
			t.Fatal(err)
		}
		if id, err := RunAgent(context.Background(), opts, io.Discard); err != nil || id != adapterTestID {
			t.Fatalf("run %q %v", id, err)
		}
		metadata, err = store.Get(adapterTestID)
		if err != nil || metadata.Profile != "personal" || metadata.Operation != "run" || metadata.Workspace == nil {
			t.Fatalf("run metadata: %+v, %v", metadata, err)
		}
		callsBefore, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		w := metadata.Workspace
		for _, expected := range []string{"src=" + w.Path + ",dst=" + w.Path, "src=" + w.GitDir + ",dst=" + w.GitDir, "--workdir\n" + w.Path} {
			if !strings.Contains(string(callsBefore), expected) {
				t.Fatalf("missing workspace mount or workdir %q: %s", expected, callsBefore)
			}
		}
		if b, err := os.ReadFile(state); err != nil || string(b) != "native" {
			t.Fatalf("lost state %q %v", b, err)
		}
		calls, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(state)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(calls), "src="+canonical+",dst=/hewitt/profile\n") != 2 {
			t.Fatalf("login/run did not share profile: %s", calls)
		}
	}
}

func TestPreparationRejectsInvalidInputBeforeCreatingProfile(t *testing.T) {
	for _, opts := range []AgentOptions{
		{Agent: "gemini", Image: "test", Profile: "default"},
		{Agent: "codex", Image: "--privileged", Profile: "default"},
		{Agent: "claude", Image: "test", Profile: "../escape"},
	} {
		opts.DataDir = filepath.Join(t.TempDir(), "absent")
		if _, err := RunAgent(context.Background(), opts, io.Discard); err == nil {
			t.Fatal("accepted invalid input")
		}
		if _, err := os.Stat(opts.DataDir); !os.IsNotExist(err) {
			t.Fatalf("created profile root: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunAgent(ctx, AgentOptions{}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func runRepository(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prepare repository: %s: %v", out, err)
		}
	}
	t.Chdir(dir)
}

// fakeAuthDocker records Docker calls; the attached logout exits nonzero when failLogout is set.
func fakeAuthDocker(t *testing.T, failLogout bool) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	exit := ""
	if failLogout {
		exit = `if [ "$3" = "--attach" ] && grep -q '^logout$' "$HEWITT_CALLS"; then exit 1; fi`
	}
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$HEWITT_CALLS"
case "$2" in
create) printf '%s\n' '` + adapterTestID + `' ;;
start) ` + exit + ` ;;
inspect) printf '%s\n' '{"Status":"exited","ExitCode":0}' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestReloginLogsOutBeforeLogin(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failLogout=%v", fail), func(t *testing.T) {
			log := fakeAuthDocker(t, fail)
			opts := AgentOptions{Agent: "codex", Image: "agent:test", Profile: "personal", DataDir: t.TempDir()}
			var stderr bytes.Buffer
			err := Authenticate(context.Background(), opts, "relogin", strings.NewReader(""), io.Discard, &stderr)
			calls, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			logout, login := strings.Index(string(calls), "\nlogout\n"), strings.Index(string(calls), "\n--device-auth\n")
			if !strings.Contains(stderr.String(), "Relogin 1/2: logging out of codex (profile personal)") {
				t.Fatalf("relogin progress = %q", stderr.String())
			}
			if fail {
				if err == nil || !strings.Contains(err.Error(), "relogin: logout") || login != -1 || strings.Contains(stderr.String(), "2/2") {
					t.Fatalf("failed logout: err = %v, calls = %s", err, calls)
				}
				return
			}
			if err != nil || logout == -1 || login == -1 || logout > login || !strings.Contains(stderr.String(), "Relogin 2/2") {
				t.Fatalf("relogin: err = %v, calls = %s", err, calls)
			}
		})
	}
}

func TestReloginReplacesClaudeToken(t *testing.T) {
	fakeAuthDocker(t, false)
	data := t.TempDir()
	dir, err := profile.Prepare(data, "claude", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.SaveToken(dir, "CLAUDE_CODE_OAUTH_TOKEN", "old-token"); err != nil {
		t.Fatal(err)
	}
	// A file is passed to Docker directly, so the token remains to be read after login.
	input := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(input, []byte("new-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	opts := AgentOptions{Agent: "claude", Image: "agent:test", Profile: "personal", DataDir: data}
	if err := Authenticate(context.Background(), opts, "relogin", in, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	path, err := profile.TokenFile(dir)
	if err != nil || path == "" {
		t.Fatalf("token file %q, %v", path, err)
	}
	token, err := os.ReadFile(path)
	if err != nil || string(token) != "CLAUDE_CODE_OAUTH_TOKEN=new-token\n" {
		t.Fatalf("token = %q, %v", token, err)
	}
}
