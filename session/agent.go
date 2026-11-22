package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/workspace"
	"golang.org/x/term"
)

// AgentOptions describes one native client invocation. Run images must contain
// Git, bash, sleep, and base64 for volume runs; Hewitt mounts its own
// hewitt-agent. Legacy modes also require tmux. Images must support execution as the invoking host UID/GID.
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
	// Separate creates another workspace container instead of reusing one.
	Separate bool
	Shell    bool
	// Prompt is a task passed to the agent as its initial prompt; Headless
	// runs it in the agent's non-interactive mode and prints its output.
	Prompt   string
	Headless bool
	// Task names a batch task: its agent works on a branch named after it.
	// Started, when set, receives the launch's run ID once it is recorded.
	Task    string
	Started func(run string)
}

// RunAgent prepares a shared workspace container for volume runs, or starts a
// legacy persistent terminal for other modes. Effective
// settings are reported to stderr. External containers are never managed.
func RunAgent(ctx context.Context, opts AgentOptions, stderr io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := reportStep(stderr, "Reading project settings"); err != nil {
		return "", err
	}
	s, err := mergeSettings(opts)
	if err != nil {
		return "", err
	}
	if s.Agent != "workspace" {
		if _, err := s.agents.Lookup(s.Agent); err != nil {
			return "", err
		}
	}
	if s.Mode != "exec" {
		if err := (docker.CreateOptions{Image: s.Image, Name: s.Name}).Validate(); err != nil {
			return "", err
		}
		if err := profile.ValidateID(s.Profile); err != nil {
			return "", err
		}
		if err := refreshImage(ctx, s.Image, stderr); err != nil {
			return "", err
		}
	}
	if (opts.Shell || opts.Separate || opts.Prompt != "") && (s.Mode != "new" || s.Workspace != "volume") {
		return "", fmt.Errorf("shell, --separate, and tasks (-p) require the managed volume workspace in new mode")
	}
	key := ""
	if s.Mode == "new" && s.Workspace == "volume" {
		if err := reportStep(stderr, "Resolving repository and checking workspace availability"); err != nil {
			return "", err
		}
		var unlock func()
		key, unlock, err = lockWorkspace(ctx, opts.DataDir, s)
		if err != nil {
			return "", err
		}
		defer unlock()
		// All agent profiles and egress hosts belong to the workspace.
		s.Agent = "workspace"
	}
	if s.Mode == "exec" {
		return runExternal(ctx, opts.DataDir, s, stderr)
	}
	var chosen *agent.Manifest
	if s.Agent != "workspace" {
		m, err := s.agents.Lookup(s.Agent)
		if err != nil {
			return "", err
		}
		chosen = &m
	}
	// Resolve every host path before creating a workspace or container.
	if err := reportStep(stderr, "Checking mounts and credential configuration"); err != nil {
		return "", err
	}
	extra, err := s.hostMounts(chosen, agent.ProfilePath)
	if err != nil {
		return "", err
	}
	if key != "" {
		for _, a := range s.agents.All() {
			mounts, err := s.hostMounts(&a, agent.ProfilesPath+"/"+a.Name)
			if err != nil {
				return "", err
			}
			extra = append(extra, mounts[len(s.mounts):]...)
		}
	}
	envFile, envKeys, err := s.envFile()
	// stale is a saved workspace whose container is replaced; its volume is kept.
	var stale *Metadata
	if err != nil {
		return "", err
	}
	if key != "" {
		// Reuse only after validating host mounts and the credential env file.
		if !opts.Separate {
			id, old, err := reuseWorkspace(ctx, opts.DataDir, key, s.agents.Names(), stderr)
			if id != "" || err != nil {
				return id, err
			}
			stale = old
		} else {
			key = fmt.Sprintf("%x", sha256.Sum256([]byte(key+rand.Text())))
		}
	}
	var direct []docker.BindMount
	var root string
	if s.Workspace == "direct" {
		if direct, root, err = s.directMounts(); err != nil {
			return "", err
		}
	}
	opts.Agent, opts.Image, opts.Name, opts.Profile = s.Agent, s.Image, s.Name, s.Profile
	if err := reportStep(stderr, "Preparing agent profiles and tool cache"); err != nil {
		return "", err
	}
	create, _, err := prepare(ctx, opts, "run")
	if err != nil {
		return "", err
	}
	if chosen != nil {
		// Legacy terminals start the agent at once; install it before creating anything.
		if err := ensureTool(ctx, opts.DataDir, create.Image, *chosen, stderr); err != nil {
			return "", err
		}
	}
	s.apply(&create)
	if envFile != "" {
		create.EnvFiles = append(create.EnvFiles, envFile)
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return "", err
	}
	m := Metadata{Agent: s.Agent, Image: s.Image, Name: s.Name, Profile: s.Profile, Operation: "run", Terminal: "tmux", Mode: s.Mode, Isolation: s.Isolation, WorkspaceKey: key}
	if key != "" {
		m.Terminal, m.Agents, m.MountsAgent = "host", s.agents.Names(), true
	}
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
		var v workspace.Volume
		if stale != nil {
			v, m.Name = *stale.Volume, stale.Name
			preserved = volumePreserved(v)
			if err := replaceContainers(ctx, *stale, stderr); err != nil {
				return "", fmt.Errorf("%s; old workspace record kept: %w", preserved, err)
			}
		} else {
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
			// Check Docker before anchoring the base in the host repository. A
			// failed daemon connection must not reserve a name with no volume.
			if exists, err := (docker.Client{}).VolumeExists(ctx, "hewitt-"+name); err != nil {
				return "", err
			} else if exists {
				return "", fmt.Errorf("volume %q already exists; remove it or choose another --name", "hewitt-"+name)
			}
			if err := reportStep(stderr, "Preparing repository snapshot"); err != nil {
				return "", err
			}
			v, err = workspace.PrepareVolume(ctx, top, base, name)
			if err != nil {
				return "", err
			}
			v.Path = volumeRepo(filepath.Base(v.Repository))
			preserved = volumePreserved(v)
			if err := createVolume(ctx, s.Image, v, stderr); err != nil {
				return "", fmt.Errorf("%s: %w", preserved, err)
			}
		}
		projectName = filepath.Base(v.Repository)
		m.Volume = &v
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
		)
		if key == "" {
			create.Env = append(create.Env, "HEWITT_INITIAL_WORKTREE_READY=1")
		}
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
	if s.Telemetry != "" {
		create.Env = append(create.Env, telemetryEnv(s.Telemetry)...)
	}
	if s.gateway() {
		image := s.EgressImage
		if image == "" {
			image = DefaultEgressImage
		}
		if err := refreshImage(ctx, image, stderr); err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		var hosts, allow []string
		if s.egress() {
			if chosen != nil {
				hosts = append(hosts, chosen.Hosts...)
			} else {
				for _, a := range s.agents.All() {
					hosts = append(hosts, a.Hosts...)
				}
			}
			allow = s.Allow
			if s.Telemetry != "" {
				allow = append(slices.Clone(allow), telemetryAllow(s.Telemetry))
			}
		}
		if err := reportStep(stderr, "Preparing network and gateway"); err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		e, err := createGateway(ctx, opts.DataDir, image, s.Profile, !s.egress(), append(hosts, allow...), s.agents.All())
		if err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		m.Egress = &e
		preserved += "; " + e.preserved()
		if open := openPorts(allow); len(open) > 0 {
			if err := warn(stderr, "egress.allow opens ports other than 443, where traffic need not be HTTPS: %s", strings.Join(open, ", ")); err != nil {
				return "", fmt.Errorf("%s: %w", preserved, err)
			}
		}
		if s.egress() && strings.HasPrefix(s.Telemetry, "http://") {
			// The gateway refuses plain HTTP; only Claude Code tunnels an http:// endpoint.
			if err := warn(stderr, "telemetry.endpoint uses http://; through the gateway only Claude Code delivers it, so use https:// for Codex and OpenCode"); err != nil {
				return "", fmt.Errorf("%s: %w", preserved, err)
			}
		}
		create.Network = e.Network
		create.Env = append(create.Env, proxyEnv(e.Open)...)
		create.Mounts = append(create.Mounts, gatewayMount(opts.DataDir))
		if key != "" && s.Isolation != "shared" {
			// Agents share the container and its UID, so the observer tells
			// them apart and holds access to each other's logins.
			token := observerToken()
			if err := createObserver(ctx, image, &e, token, s.agents.Names()); err != nil {
				return "", fmt.Errorf("%s: %w", preserved, err)
			}
			preserved += fmt.Sprintf("; observer %q preserved", e.Observer)
			create.Env = append(create.Env, "HEWITT_OBSERVE="+token)
		}
	}
	if chosen != nil {
		// A legacy terminal starts the agent with the container, so its
		// command line and credential are chosen now: a placeholder behind a
		// gateway, the token itself on a network the gateway does not serve.
		allow := envKeys
		if chosen.Token != nil {
			env, files, err := agentToken(opts.DataDir, s.Profile, *chosen, m.Egress != nil)
			if err != nil {
				return "", fmt.Errorf("%s: %w", preserved, err)
			}
			create.Env = append(create.Env, env...)
			create.EnvFiles = append(create.EnvFiles, files...)
			allow = append(slices.Clone(allow), chosen.Token.Variable)
		}
		launch, err := chosen.Launch(agent.OpRun, agent.LaunchOptions{Profile: agent.ProfilePath, Telemetry: s.Telemetry, Allow: allow})
		if err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		create.Env = append(create.Env, launch.Env()...)
		// New tmux windows open hewitt-agent's menu of every agent.
		menu, err := menuEnv(s.agents, s.agents.Names(), s.Telemetry, envKeys)
		if err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
		create.Env = append(create.Env, menu...)
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
	if key != "" {
		create.Entrypoint = "sleep"
		create.Command = []string{"infinity"}
		create.DisableTTY = true
		create.Env = append(create.Env, "HEWITT_HOST_TERMINAL=1")
	} else {
		create.Entrypoint = "tmux"
		create.Command = docker.TerminalServerArgs()
	}
	if _, err := io.WriteString(stderr, s.describe(create)); err != nil {
		return "", fmt.Errorf("report settings (%s): %w", preserved, err)
	}
	if err := reportStep(stderr, "Creating workspace container (pulling the image if needed)"); err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	metadata, err := createAgentContainer(ctx, store, create, m)
	if err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	if stale != nil && stale.ID != metadata.ID {
		// The new record owns the volume now; the old one names removed containers.
		if err := store.Delete(stale.ID); err != nil {
			return "", fmt.Errorf("container %s preserved: %w", metadata.ID[:12], err)
		}
	}
	if m.Egress != nil {
		if err := (docker.Client{}).Start(ctx, m.Egress.Proxy); err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
	}
	if err := reportStep(stderr, "Starting workspace container"); err != nil {
		return "", fmt.Errorf("container %s preserved: %w", metadata.ID, err)
	}
	if err := (docker.Client{}).Start(ctx, metadata.ID); err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	if err := startObserver(ctx, m.Egress, stderr); err != nil {
		return "", fmt.Errorf("%s: %w", preserved, err)
	}
	if key == "" {
		if err := reportStep(stderr, "Starting agent terminal"); err != nil {
			return "", fmt.Errorf("container %s preserved: %w", metadata.ID, err)
		}
		if err := (docker.Client{}).StartTerminal(ctx, metadata.ID, command); err != nil {
			return "", fmt.Errorf("%s: %w", preserved, err)
		}
	}
	return metadata.ID, nil
}

// runExternal launches the agent inside a container Hewitt does not own. The
// container's own user, filesystem, network, and client state apply; no profile
// is mounted. The session ID is random because one container may host several.
func runExternal(ctx context.Context, dataDir string, s settings, stderr io.Writer) (string, error) {
	a, err := s.agents.Lookup(s.Agent)
	if err != nil {
		return "", err
	}
	// Profile environment is omitted: the external container has no profile
	// mount, and no hewitt-agent runs the client there.
	launch, err := a.Launch(agent.OpRun, agent.LaunchOptions{Workdir: s.Workdir})
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
	command := launch.Argv
	if err := reportStep(stderr, "Starting agent in target container"); err != nil {
		return "", err
	}
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

// Authenticate validates the agent, profile, and image once, then runs the
// credential operation. Relogin runs logout and then login, each in its own
// container, and stops if logout fails. Containers and profiles are preserved.
func Authenticate(ctx context.Context, opts AgentOptions, operation string, in io.Reader, out, stderr io.Writer) error {
	agents, err := agent.Load(opts.DataDir)
	if err != nil {
		return err
	}
	a, err := agents.Lookup(opts.Agent)
	if err != nil {
		return err
	}
	if opts.Image == "" {
		opts.Image = a.ImageFor()
	}
	if err := (docker.CreateOptions{Image: opts.Image, Name: opts.Name}).Validate(); err != nil {
		return err
	}
	if err := profile.ValidateID(opts.Profile); err != nil {
		return err
	}
	if err := refreshImage(ctx, opts.Image, stderr); err != nil {
		return err
	}
	switch operation {
	case "login", "status", "logout":
		return runAuthentication(ctx, opts, a, operation, in, out, stderr)
	case "relogin":
		if err := reportStep(stderr, fmt.Sprintf("Relogin 1/2: logging out of %s (profile %s)", opts.Agent, opts.Profile)); err != nil {
			return err
		}
		if err := runAuthentication(ctx, opts, a, "logout", in, out, stderr); err != nil {
			return fmt.Errorf("relogin: logout: %w", err)
		}
		if err := reportStep(stderr, fmt.Sprintf("Relogin 2/2: logging in to %s (profile %s)", opts.Agent, opts.Profile)); err != nil {
			return err
		}
		if err := runAuthentication(ctx, opts, a, "login", in, out, stderr); err != nil {
			return fmt.Errorf("relogin: login: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported authentication operation %q", operation)
	}
}

// runAuthentication runs one native login, status, or logout in a new
// container and keeps a long-lived token file in step with it.
func runAuthentication(ctx context.Context, opts AgentOptions, a agent.Manifest, operation string, in io.Reader, out, stderr io.Writer) error {
	create, dir, err := prepare(ctx, opts, operation)
	if err != nil {
		return err
	}
	if err := ensureTool(ctx, opts.DataDir, create.Image, a, stderr); err != nil {
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
	// Clients without a token keep their login in their own files.
	if a.Token == nil {
		return nil
	}
	switch operation {
	case "login":
		token, err := readToken(in, stderr)
		if err != nil {
			return err
		}
		return profile.SaveToken(dir, a.Token.Variable, token)
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

// prepare returns create options and the client's secrets directory. A saved
// long-lived token is passed through a Docker env file, never as an argument.
func prepare(ctx context.Context, opts AgentOptions, operation string) (docker.CreateOptions, string, error) {
	if err := ctx.Err(); err != nil {
		return docker.CreateOptions{}, "", err
	}
	agents, err := agent.Load(opts.DataDir)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	script, launcher, err := agentScript(opts.DataDir)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	if opts.Agent == "workspace" {
		if opts.Image == "" {
			opts.Image = agent.DefaultImage
		}
		create := docker.CreateOptions{
			Image: opts.Image, Name: opts.Name, Entrypoint: launcher,
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
		create.Mounts = []docker.BindMount{{Source: tools, Target: toolsPath, ReadOnly: true}, script}
		profiles, err := profileMounts(opts.DataDir, opts.Profile, agents.Names())
		if err != nil {
			return docker.CreateOptions{}, "", err
		}
		create.Mounts = append(create.Mounts, profiles...)
		if err := create.Validate(); err != nil {
			return docker.CreateOptions{}, "", err
		}
		return create, "", nil
	}
	a, err := agents.Lookup(opts.Agent)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	if opts.Image == "" {
		opts.Image = a.ImageFor()
	}
	// RunAgent adds the command line of a run once it knows the run's
	// credentials; hewitt-agent launch then starts it.
	create := docker.CreateOptions{
		Image: opts.Image, Name: opts.Name, Entrypoint: launcher,
		Command: []string{"launch"}, Env: agent.ProfileEnv(agent.ProfilePath), Workdir: "/tmp",
		DisableLogs: operation != "run",
		DisableTTY:  operation == "status" || operation == "logout",
		User:        fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
	}
	if err := create.Validate(); err != nil {
		return docker.CreateOptions{}, "", err
	}
	path, err := profile.Prepare(opts.DataDir, a.Name, opts.Profile)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	secrets, err := profile.PrepareSecrets(opts.DataDir, a.Name, opts.Profile)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	tools, err := profile.PrepareTools(opts.DataDir)
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	create.Mounts = []docker.BindMount{
		{Source: path, Target: agent.ProfilePath},
		{Source: tools, Target: toolsPath, ReadOnly: true},
		script,
	}
	if operation == "run" {
		profiles, err := profileMounts(opts.DataDir, opts.Profile, agents.Names())
		if err != nil {
			return docker.CreateOptions{}, "", err
		}
		create.Mounts = append(create.Mounts, profiles...)
		if err := create.Validate(); err != nil {
			return docker.CreateOptions{}, "", err
		}
		return create, secrets, nil
	}
	// Status and logout run the native client with the saved token; runs
	// receive it from RunAgent, as a placeholder behind a gateway.
	var allow []string
	if a.Token != nil && (operation == "status" || operation == "logout") {
		token, err := profile.TokenFile(secrets)
		if err != nil {
			return docker.CreateOptions{}, "", err
		}
		if token != "" {
			create.EnvFiles = append(create.EnvFiles, token)
			allow = []string{a.Token.Variable}
		}
	}
	launch, err := a.Launch(operation, agent.LaunchOptions{Profile: agent.ProfilePath, Allow: allow})
	if err != nil {
		return docker.CreateOptions{}, "", err
	}
	if launch.Slash != "" {
		create.Command = []string{"slash"}
	}
	create.Env = append(create.Env, launch.Env()...)
	if err := create.Validate(); err != nil {
		return docker.CreateOptions{}, "", err
	}
	return create, secrets, nil
}

// agentToken returns how a run receives the client's saved long-lived token:
// behind a gateway, a placeholder the gateway replaces; otherwise the token's
// env file. Without a saved token the client uses its own login.
func agentToken(dataDir, profileID string, a agent.Manifest, gateway bool) (env, files []string, err error) {
	secrets, err := profile.PrepareSecrets(dataDir, a.Name, profileID)
	if err != nil {
		return nil, nil, err
	}
	token, err := profile.TokenFile(secrets)
	if err != nil || token == "" {
		return nil, nil, err
	}
	if gateway && len(a.Token.Hosts) > 0 {
		return []string{a.Token.Variable + "=" + a.Token.Placeholder}, nil, nil
	}
	return nil, []string{token}, nil
}

// profileMounts mounts each agent's profile at ProfilesPath/<agent>.
func profileMounts(dataDir, profileID string, names []string) ([]docker.BindMount, error) {
	var mounts []docker.BindMount
	for _, name := range names {
		p, err := profile.Prepare(dataDir, name, profileID)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, docker.BindMount{Source: p, Target: agent.ProfilesPath + "/" + name})
	}
	return mounts, nil
}

// menuEnv is the environment of hewitt-agent's agent menu for names: each
// agent's interactive run in its profile under ProfilesPath.
func menuEnv(agents *agent.Registry, names []string, telemetry string, allow []string) ([]string, error) {
	launches := map[string]agent.Launch{}
	for _, name := range names {
		a, err := agents.Lookup(name)
		if err != nil {
			// A workspace may name an agent whose manifest was removed since.
			continue
		}
		var keep []string
		keep = append(keep, allow...)
		if a.Token != nil {
			keep = append(keep, a.Token.Variable)
		}
		l, err := a.Launch(agent.OpRun, agent.LaunchOptions{Profile: agent.ProfilesPath + "/" + name, Telemetry: telemetry, Allow: keep})
		if err != nil {
			return nil, err
		}
		launches[name] = l
	}
	return agent.Pick(launches, names), nil
}
