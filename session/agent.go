package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/workspace"
	"golang.org/x/term"
)

// AgentOptions describes one native client invocation. Run images must contain
// tmux, env, and the selected CLI on PATH and support execution as the invoking host user's UID/GID.
// Empty run settings fall back to hewitt.yml, then to defaults.
type AgentOptions struct {
	Agent, Image, Name, Profile, DataDir string
	Repository, Base                     string
	// File selects a hewitt.yml; empty looks for one in Repository.
	File                 string
	Mode, Target         string
	Workspace, Isolation string
	Danger               []string
	// SSH forwards the host ssh-agent; hewitt.yml ssh: true does the same.
	SSH bool
}

// RunAgent starts a detached native agent in a persistent terminal. Effective
// settings are reported to stderr. External containers are never managed.
func RunAgent(ctx context.Context, opts AgentOptions, stderr io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s, err := mergeSettings(opts)
	if err != nil {
		return "", err
	}
	if s.Mode == "exec" {
		return runExternal(ctx, opts.DataDir, s, stderr)
	}
	var adapter agent.Adapter
	if s.Agent != "workspace" {
		adapter, err = agent.Lookup(s.Agent)
		if err != nil {
			return "", err
		}
	}
	// Resolve every host path before creating a workspace or container.
	extra, err := s.hostMounts(adapter)
	if err != nil {
		return "", err
	}
	envFile, envKeys, err := s.envFile()
	if err != nil {
		return "", err
	}
	var direct []docker.BindMount
	var root string
	if s.Workspace == "direct" {
		if direct, root, err = s.directMounts(); err != nil {
			return "", err
		}
	}
	opts.Agent, opts.Image, opts.Name, opts.Profile = s.Agent, s.Image, s.Name, s.Profile
	create, _, err := prepare(ctx, opts, "run")
	if err != nil {
		return "", err
	}
	s.apply(&create)
	if envFile != "" {
		create.EnvFiles = append(create.EnvFiles, envFile)
	}
	create.Command = agent.Invocation{Args: create.Command}.Allow(envKeys...).Args
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return "", err
	}
	m := Metadata{Agent: s.Agent, Image: s.Image, Name: s.Name, Profile: s.Profile, Operation: "run", Terminal: "tmux", Mode: s.Mode, Isolation: s.Isolation}
	if s.Mode == "sidecar" {
		m.Container = s.Target
	}
	preserved := "no workspace was created"
	projectName := filepath.Base(s.repo)
	switch s.Workspace {
	case "volume":
		top, err := workspace.Toplevel(ctx, s.repo)
		if err != nil {
			return "", err
		}
		name := s.Name
		if name == "" {
			if name, err = nextName(ctx, store, top, s.Agent); err != nil {
				return "", err
			}
			// A generated name labels the session, not the container.
			m.Name = name
		}
		base := ""
		if len(s.WorkspaceArgs) > 0 {
			base = s.WorkspaceArgs[0]
		}
		v, err := workspace.PrepareVolume(ctx, top, base, name)
		if err != nil {
			return "", err
		}
		projectName = filepath.Base(v.Repository)
		v.Path = volumeRepo(projectName)
		m.Volume = &v
		preserved = volumePreserved(v)
		if err := createVolume(ctx, s.Image, v); err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		root = v.Path
		volumes, binds := volumeMounts(v)
		create.Volumes = append(create.Volumes, volumes...)
		create.Mounts = append(create.Mounts, binds...)
		create.Env = append(create.Env,
			"HEWITT_WORKSPACE_MODE=worktree",
			"HEWITT_REPO="+v.Path,
			"HEWITT_WORKTREES_DIR="+volumeRoot,
			"HEWITT_BRANCH="+v.Branch,
			"HEWITT_BASE="+v.Branch,
			"HEWITT_INITIAL_WORKTREE_READY=1",
		)
		if s.Workdir != "" {
			create.Env = append(create.Env, "HEWITT_SUBDIR="+s.Workdir)
		}
	case "worktree":
		base := ""
		if len(s.WorkspaceArgs) > 0 {
			base = s.WorkspaceArgs[0]
		}
		worktree, err := workspace.Create(ctx, opts.DataDir, s.repo, base)
		if err != nil {
			return "", err
		}
		m.Workspace = &worktree
		root = worktree.Path
		projectName = filepath.Base(worktree.Repository)
		preserved = fmt.Sprintf("worktree %q (branch %s) preserved", worktree.Path, worktree.Branch)
		worktreesDir := filepath.Dir(worktree.Path)
		create.Mounts = append(create.Mounts,
			docker.BindMount{Source: worktree.Path, Target: worktree.Path},
			docker.BindMount{Source: worktreesDir, Target: worktreesDir},
		)
		create.Env = append(create.Env,
			"HEWITT_WORKSPACE_MODE=worktree",
			"HEWITT_REPO="+worktree.Path,
			"HEWITT_WORKTREES_DIR="+worktreesDir,
			"HEWITT_BASE="+worktree.Base,
			"HEWITT_INITIAL_WORKTREE_READY=1",
		)
		if s.Workdir != "" {
			create.Env = append(create.Env, "HEWITT_SUBDIR="+s.Workdir)
		}
		if s.Git != "none" {
			create.Mounts = append(create.Mounts, docker.BindMount{Source: worktree.GitDir, Target: worktree.GitDir, ReadOnly: s.Git == "ro"})
		}
	case "direct":
		create.Mounts = append(create.Mounts, direct...)
		create.Env = append(create.Env, "HEWITT_WORKSPACE_MODE=direct")
		preserved = "source paths were used directly"
	case "copy":
		source, err := filepath.EvalSymlinks(s.repo)
		if err != nil {
			return "", fmt.Errorf("resolve workspace: %w", err)
		}
		c, err := workspace.CreateCopy(opts.DataDir, source, s.WorkspaceArgs)
		if err != nil {
			return "", err
		}
		m.Copy = &c
		root = c.Path
		projectName = filepath.Base(c.Source)
		preserved = fmt.Sprintf("copy %q preserved", c.Path)
		create.Mounts = append(create.Mounts, docker.BindMount{Source: c.Path, Target: c.Path})
		create.Env = append(create.Env, "HEWITT_WORKSPACE_MODE=copy")
	}
	create.Env = append(create.Env, "HEWITT_PROJECT_NAME="+projectName)
	if s.egress() {
		image := s.EgressImage
		if image == "" {
			image = DefaultEgressImage
		}
		var hosts []string
		if adapter != nil {
			hosts = append(hosts, adapter.Hosts()...)
		} else {
			for _, name := range agent.Names {
				if a, err := agent.Lookup(name); err == nil {
					hosts = append(hosts, a.Hosts()...)
				}
			}
		}
		e, err := createEgress(ctx, opts.DataDir, image, append(hosts, s.Allow...))
		if err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		m.Egress = &e
		preserved += "; " + e.preserved()
		create.Network = e.Network
		create.Env = append(create.Env, proxyEnv()...)
	}
	if s.SSH {
		mount, err := sshAgentMount()
		if err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		create.Mounts = append(create.Mounts, mount)
		create.Env = append(create.Env, "SSH_AUTH_SOCK="+sshAgentSocket)
	}
	create.Workdir = filepath.Join(root, s.Workdir)
	create.Mounts = append(create.Mounts, extra...)
	command := append([]string{create.Entrypoint}, create.Command...)
	create.Entrypoint = "tmux"
	create.Command = docker.TerminalServerArgs()
	if _, err := io.WriteString(stderr, s.describe(create)); err != nil {
		return "", fmt.Errorf("report settings (%s): %w", preserved, err)
	}
	metadata, err := createAgentContainer(ctx, store, create, m)
	if err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	if m.Egress != nil {
		if err := (docker.Client{}).Start(ctx, m.Egress.Proxy); err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
	}
	if err := (docker.Client{}).Start(ctx, metadata.ID); err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	if err := (docker.Client{}).StartTerminal(ctx, metadata.ID, command); err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	return metadata.ID, nil
}

// runExternal launches the agent inside a container Hewitt does not own. The
// container's own user, filesystem, network, and client state apply; no profile
// is mounted. The session ID is random because one container may host several.
func runExternal(ctx context.Context, dataDir string, s settings, stderr io.Writer) (string, error) {
	adapter, err := agent.Lookup(s.Agent)
	if err != nil {
		return "", err
	}
	store, err := NewStore(dataDir)
	if err != nil {
		return "", err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	id := hex.EncodeToString(random)
	m := Metadata{ID: id, Agent: s.Agent, CreatedAt: time.Now().UTC(), Operation: "run", Terminal: "tmux",
		Mode: "exec", Container: s.Target, Socket: "/tmp/hewitt-" + id[:16] + ".sock", Isolation: s.Isolation}
	if _, err := io.WriteString(stderr, s.describe(docker.CreateOptions{})); err != nil {
		return "", fmt.Errorf("report settings: %w", err)
	}
	if err := store.Save(m); err != nil {
		return "", fmt.Errorf("record session for container %q (agent not started): %w", s.Target, err)
	}
	// Profile environment is omitted: the external container has no profile mount.
	inv := adapter.Run()
	command := append([]string{inv.Entrypoint}, inv.Args...)
	if err := (docker.Client{}).StartExternalTerminal(ctx, s.Target, m.Socket, s.Workdir, command); err != nil {
		return "", fmt.Errorf("session %s recorded: %w", id, err)
	}
	return id, nil
}

// Login delegates subscription authentication to the native client in a terminal.
// Returning nil means its login process exited successfully, not a separate
// Hewitt verification of the subscription or entitlement.
func Login(ctx context.Context, opts AgentOptions, in io.Reader, out, stderr io.Writer) error {
	return Authenticate(ctx, opts, "login", in, out, stderr)
}

// Authenticate delegates credential operations to the native client.
// It preserves both the profile and the operation container.
func Authenticate(ctx context.Context, opts AgentOptions, operation string, in io.Reader, out, stderr io.Writer) error {
	if operation != "login" && operation != "status" && operation != "logout" {
		return fmt.Errorf("unsupported authentication operation %q", operation)
	}
	create, dir, err := prepare(ctx, opts, operation)
	if err != nil {
		return err
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return err
	}
	metadata, err := createAgentContainer(ctx, store, create, Metadata{Agent: opts.Agent, Profile: opts.Profile, Operation: operation})
	if err != nil {
		return err
	}
	id := metadata.ID
	if _, err := fmt.Fprintf(stderr, "Authentication container: %s (preserved; remove with docker rm after exit)\n", id); err != nil {
		return fmt.Errorf("report login container %q: %w", id, err)
	}
	if err := (docker.Client{}).StartAttached(ctx, id, in, out, stderr); err != nil {
		return err
	}
	adapter, err := agent.Lookup(opts.Agent)
	if err != nil || adapter.TokenVariable() == "" {
		return err
	}
	switch operation {
	case "login":
		token, err := readToken(in, stderr)
		if err != nil {
			return err
		}
		return profile.SaveToken(dir, adapter.TokenVariable(), token)
	case "logout":
		return profile.RemoveToken(dir)
	}
	return nil
}

// readToken asks for the token the native login just printed. Terminal input
// is not echoed; the value is never written to output or logs.
func readToken(in io.Reader, stderr io.Writer) (string, error) {
	if _, err := io.WriteString(stderr, "Paste the token printed above (input hidden): "); err != nil {
		return "", fmt.Errorf("prompt for token: %w", err)
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		token, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", fmt.Errorf("read token: %w", err)
		}
		return strings.TrimSpace(string(token)), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// prepare returns create options and the profile directory. A saved
// long-lived token is passed through a Docker env file, never as an argument.
func prepare(ctx context.Context, opts AgentOptions, operation string) (docker.CreateOptions, string, error) {
	if err := ctx.Err(); err != nil {
		return docker.CreateOptions{}, "", err
	}
	if opts.Agent == "workspace" {
		if opts.Image == "" {
			opts.Image = agent.DefaultImage
		}
		create := docker.CreateOptions{
			Image: opts.Image, Name: opts.Name, Entrypoint: "hewitt-agent",
			Command: []string{"run"}, Workdir: "/tmp",
			DisableLogs: operation != "run",
			User:        fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		}
		if err := create.Validate(); err != nil {
			return docker.CreateOptions{}, "", err
		}
		tools, err := profile.PrepareTools(opts.DataDir)
		if err != nil {
			return docker.CreateOptions{}, "", err
		}
		create.Mounts = []docker.BindMount{{Source: tools, Target: "/hewitt/tools"}}
		for _, client := range agent.Names {
			p, err := profile.Prepare(opts.DataDir, client, opts.Profile)
			if err != nil {
				return docker.CreateOptions{}, "", err
			}
			create.Mounts = append(create.Mounts, docker.BindMount{Source: p, Target: "/hewitt/profiles/" + client})
		}
		if err := create.Validate(); err != nil {
			return docker.CreateOptions{}, "", err
		}
		return create, "", nil
	}
	adapter, err := agent.Lookup(opts.Agent)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	if opts.Image == "" {
		opts.Image = adapter.DefaultImage()
	}
	inv := adapter.Run()
	switch operation {
	case "login":
		inv = adapter.Login()
	case "status":
		inv = adapter.Status()
	case "logout":
		inv = adapter.Logout()
	}
	create := docker.CreateOptions{
		Image: opts.Image, Name: opts.Name, Entrypoint: inv.Entrypoint,
		Command: inv.Args, Env: inv.Env, Workdir: "/tmp",
		DisableLogs: operation != "run",
		DisableTTY:  operation == "status" || operation == "logout",
		User:        fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
	}
	if err := create.Validate(); err != nil {
		return docker.CreateOptions{}, "", err
	}
	path, err := profile.Prepare(opts.DataDir, adapter.Name(), opts.Profile)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	tools, err := profile.PrepareTools(opts.DataDir)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	create.Mounts = []docker.BindMount{
		{Source: path, Target: agent.ProfilePath},
		{Source: tools, Target: "/hewitt/tools"},
	}
	if operation == "run" {
		for _, client := range agent.Names {
			p, err := profile.Prepare(opts.DataDir, client, opts.Profile)
			if err != nil {
				return docker.CreateOptions{}, "", err
			}
			create.Mounts = append(create.Mounts, docker.BindMount{Source: p, Target: "/hewitt/profiles/" + client})
		}
	}
	if variable := adapter.TokenVariable(); variable != "" && operation != "login" {
		token, err := profile.TokenFile(path)
		if err != nil {
			return docker.CreateOptions{}, "", err
		}
		if token != "" {
			create.EnvFiles = append(create.EnvFiles, token)
			create.Command = agent.Invocation{Args: create.Command}.Allow(variable).Args
		}
	}
	if err := create.Validate(); err != nil {
		return docker.CreateOptions{}, "", err
	}
	return create, path, nil
}
