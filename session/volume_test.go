package session

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/workspace"
)

// script runs a helper script on the host, as the helper container would.
func script(t *testing.T, body string, args ...string) string {
	t.Helper()
	out, err := exec.Command("sh", append([]string{"-c", body, "sh"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v: %s", err, out)
	}
	return string(out)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestVolumeScriptsRoundTrip initializes a borrowing repository with the
// helper scripts, commits in it, and imports its branches through a bundle.
func TestVolumeScriptsRoundTrip(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	host := t.TempDir()
	gitIn(t, host, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(host, "README"), []byte("host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, host, "add", "README")
	gitIn(t, host, "-c", "user.name=Host", "-c", "user.email=host@example.com", "commit", "-q", "-m", "initial")
	gitIn(t, host, "config", "user.name", "Host")
	gitIn(t, host, "config", "user.email", "host@example.com")
	hooks := filepath.Join(host, ".git", "hooks")
	before, _ := os.ReadDir(hooks)

	v, err := workspace.PrepareVolume(ctx, host, "", "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	v.Path = filepath.Join(t.TempDir(), "repo")
	script(t, initScript, v.Path, v.Branch, v.Base, v.Objects, v.Origin, v.UserName, v.UserEmail)
	// Rerunning is a no-op on an initialized volume.
	script(t, initScript, v.Path, v.Branch, v.Base, v.Objects, v.Origin, v.UserName, v.UserEmail)
	if got, err := os.ReadFile(filepath.Join(v.Path, "README")); err != nil || string(got) != "host\n" {
		t.Fatalf("checkout = %q, %v", got, err)
	}
	if out := script(t, bundleScript, v.Path, v.Base); out != "" {
		t.Fatalf("bundle without new commits = %d bytes", len(out))
	}

	// The agent commits, adds a tab branch, and plants a hook in its own repository.
	if err := os.WriteFile(filepath.Join(v.Path, "change"), []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, v.Path, "add", "change")
	gitIn(t, v.Path, "commit", "-q", "-m", "agent change")
	gitIn(t, v.Path, "branch", "main-2")
	if err := os.WriteFile(filepath.Join(v.Path, ".git", "hooks", "post-checkout"), []byte("#!/bin/sh\ntouch "+filepath.Join(host, "pwned")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if author := gitIn(t, v.Path, "log", "-1", "--format=%an <%ae>"); author != "Host <host@example.com>" {
		t.Fatalf("author = %q", author)
	}
	if dirty := script(t, dirtyScript, v.Path); dirty != "" {
		t.Fatalf("clean volume reported dirty: %q", dirty)
	}
	if err := os.WriteFile(filepath.Join(v.Path, "untracked"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Git reports resolved paths (macOS /var is a symlink to /private/var).
	real, _ := filepath.EvalSymlinks(v.Path)
	if dirty := script(t, dirtyScript, v.Path); strings.TrimSpace(dirty) != real {
		t.Fatalf("dirty = %q", dirty)
	}

	bundle := filepath.Join(t.TempDir(), "b.bundle")
	data := script(t, bundleScript, v.Path, v.Base)
	if err := os.WriteFile(bundle, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := v.Import(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(changed)
	if want := []string{"hewitt/claude-1/main", "hewitt/claude-1/main-2"}; !slices.Equal(changed, want) {
		t.Fatalf("imported %v, want %v", changed, want)
	}
	if got, want := gitIn(t, host, "rev-parse", "hewitt/claude-1/main"), gitIn(t, v.Path, "rev-parse", "main"); got != want {
		t.Fatalf("host branch %s, volume %s", got, want)
	}
	if again, err := v.Import(ctx, bundle); err != nil || len(again) != 0 {
		t.Fatalf("reimport changed %v, %v", again, err)
	}
	gitIn(t, host, "checkout", "-q", "hewitt/claude-1/main")
	gitIn(t, host, "status")
	if _, err := os.Stat(filepath.Join(host, "pwned")); !os.IsNotExist(err) {
		t.Fatal("a hook from the session repository ran on the host")
	}
	after, _ := os.ReadDir(hooks)
	if len(after) != len(before) {
		t.Fatal("host hooks changed")
	}
	if gitIn(t, host, "rev-parse", "main") != v.Base {
		t.Fatal("host main moved")
	}
}

func TestVolumeRepoPath(t *testing.T) {
	for project, want := range map[string]string{"hewitt": "/hewitt/work/hewitt", "my.app": "/hewitt/work/my.app", "a b": "/hewitt/work/repo", "-x": "/hewitt/work/repo"} {
		if got := volumeRepo(project); got != want {
			t.Errorf("volumeRepo(%q) = %q, want %q", project, got, want)
		}
	}
}

func TestRunAgentVolumeWorkspace(t *testing.T) {
	runRepository(t)
	bin, data := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("HEWITT_CALLS", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HEWITT_CALLS"
case "$1 $2" in
"volume inspect") printf 'Error: No such volume: %s\n' "$4" >&2; exit 1 ;;
"container create") printf '%s\n' '` + adapterTestID + `' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := RunAgent(ctx, AgentOptions{Agent: "codex", Image: "agent:test", DataDir: data}, io.Discard)
	if err != nil || id != adapterTestID {
		t.Fatalf("run %q %v", id, err)
	}
	store, err := NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Get(id)
	if err != nil || m.Name != "workspace-1" || m.Volume == nil || m.Workspace != nil || m.Terminal != "host" || m.Agent != "workspace" {
		t.Fatalf("metadata = %+v, %v", m, err)
	}
	v := *m.Volume
	if v.Volume != "hewitt-workspace-1" || v.Branch == "" || !strings.HasPrefix(v.Path, volumeRoot+"/") {
		t.Fatalf("volume = %+v", v)
	}
	repo, _ := os.Getwd()
	if anchored := gitIn(t, repo, "rev-parse", "refs/hewitt/workspace-1/base"); anchored != v.Base {
		t.Fatalf("anchor = %q, base %q", anchored, v.Base)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var create string
	var helpers []string
	for _, call := range calls {
		switch {
		case strings.HasPrefix(call, "container create"):
			create = call
		case strings.HasPrefix(call, "container run"):
			helpers = append(helpers, call)
		}
	}
	if !slices.Contains(calls, "volume create --label io.hewitt.managed=true -- hewitt-workspace-1") || len(helpers) != 2 {
		t.Fatalf("calls = %q", calls)
	}
	if !strings.Contains(helpers[0], "--cap-drop ALL") || !strings.Contains(helpers[0], "--cap-add CHOWN") || !strings.Contains(helpers[0], "--user 0:0") {
		t.Fatalf("ownership helper = %q", helpers[0])
	}
	for _, helper := range helpers {
		for _, want := range []string{"--rm", "--network none", "--read-only", "type=volume,src=hewitt-workspace-1,dst=/hewitt/work"} {
			if !strings.Contains(helper, want) {
				t.Fatalf("helper %q lacks %q", helper, want)
			}
		}
	}
	if strings.Contains(helpers[1], "--cap-add") || strings.Contains(helpers[1], "--user 0:0") {
		t.Fatalf("init helper is privileged: %q", helpers[1])
	}
	objects := "type=bind,src=" + v.Objects + ",dst=" + sourceObjects + ",readonly"
	for _, want := range []string{"type=volume,src=hewitt-workspace-1,dst=/hewitt/work", objects, "HEWITT_REPO=" + v.Path, "HEWITT_BRANCH=" + v.Branch, "--workdir " + v.Path} {
		if !strings.Contains(create, want) {
			t.Fatalf("create %q lacks %q", create, want)
		}
	}
	gitDir := filepath.Dir(v.Objects)
	if strings.Contains(create, "src="+gitDir+",") {
		t.Fatalf("host .git mounted: %q", create)
	}
}

func TestVolumeSettingsChecks(t *testing.T) {
	runRepository(t)
	for _, tt := range []struct {
		opts AgentOptions
		want string
	}{
		{AgentOptions{Agent: "codex", Image: "x", Workspace: "worktree"}, "danger: [host-paths]"},
		{AgentOptions{Agent: "codex", Image: "x", SSH: true, Isolation: "restricted"}, "ssh needs direct network access"},
	} {
		if _, err := mergeSettings(tt.opts); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%+v: %v, want %q", tt.opts, err, tt.want)
		}
	}
	for _, opts := range []AgentOptions{
		{Agent: "codex", Image: "x", Workspace: "worktree", Danger: []string{"host-paths"}},
		{Agent: "codex", Image: "x", SSH: true},
		{Agent: "codex", Image: "x", Base: "HEAD"},
	} {
		s, err := mergeSettings(opts)
		if err != nil {
			t.Errorf("%+v: %v", opts, err)
		}
		if opts.Base != "" && s.Workspace != "volume" {
			t.Errorf("--base workspace = %q", s.Workspace)
		}
	}
}

func TestSSHAgentMount(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Docker Desktop path")
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	if _, err := sshAgentMount(); err == nil {
		t.Fatal("accepted missing SSH_AUTH_SOCK")
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("SSH_AUTH_SOCK", file)
	if _, err := sshAgentMount(); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("regular file: %v", err)
	}
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("SSH_AUTH_SOCK", socket)
	mount, err := sshAgentMount()
	if err != nil || mount.Source != socket || mount.Target != sshAgentSocket || mount.ReadOnly {
		t.Fatalf("mount = %+v, %v", mount, err)
	}
}
