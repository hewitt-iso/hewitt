package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/terminal"
	"github.com/hewitt-iso/hewitt/workspace"
)

// lockWorkspace serializes lookup/create across host terminals. The kernel
// releases the lock even when a CLI is killed during container creation.
func lockWorkspace(ctx context.Context, dataDir string, s settings) (string, func(), error) {
	repo, err := workspace.Toplevel(ctx, s.repo)
	if err != nil {
		return "", nil, err
	}
	project := s.Project
	project.Agent = "workspace"
	data, err := json.Marshal(struct {
		Repository string
		Settings   any
		Danger     any
	}{repo, project, s.danger})
	if err != nil {
		return "", nil, fmt.Errorf("encode workspace settings: %w", err)
	}
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
	if !filepath.IsAbs(dataDir) {
		return "", nil, fmt.Errorf("session data directory must be an absolute path")
	}
	dir := filepath.Join(dataDir, "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, fmt.Errorf("create workspace lock directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, key), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("open workspace lock: %w", err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return key, func() { f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return "", nil, fmt.Errorf("lock workspace: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return "", nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// reuseWorkspace returns a saved workspace container for key. A container built
// from an older image or before one of agents was added, or one Docker no
// longer has, is returned as stale when no agent is connected to it, so the
// caller can replace the container and keep its volume.
func reuseWorkspace(ctx context.Context, dataDir, key string, agents []string, stderr io.Writer) (string, *Metadata, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return "", nil, err
	}
	records, err := store.List()
	if err != nil {
		return "", nil, err
	}
	for _, m := range records {
		if m.Terminal != "host" || m.WorkspaceKey != key {
			continue
		}
		client := docker.Client{}
		if err := reportStep(stderr, "Checking saved workspace "+m.Name); err != nil {
			return "", nil, err
		}
		have, err := client.ContainerImage(ctx, m.ID)
		if err != nil && !docker.IsNotFound(err) {
			return "", nil, err
		}
		if err != nil {
			if err := warn(stderr, "container %s of workspace %s no longer exists; creating a new one with the same volume", m.ID[:12], m.Name); err != nil {
				return "", nil, err
			}
			return "", &m, nil
		}
		current, _, err := client.ImageID(ctx, m.Image)
		if err != nil {
			return "", nil, err
		}
		reason := ""
		if current != "" && have != "" && current != have {
			reason = "uses an older " + m.Image
		} else if !m.MountsAgent {
			reason = "predates the hewitt-agent that Hewitt mounts"
		} else if added := slices.DeleteFunc(slices.Clone(agents), func(name string) bool { return slices.Contains(m.WorkspaceAgents(), name) }); len(added) > 0 {
			reason = "has no profile for " + strings.Join(added, ", ")
		}
		if reason != "" {
			live, err := workspaceRuns(store, m.ID)
			if err != nil {
				return "", nil, err
			}
			if len(live) == 0 {
				if _, err := fmt.Fprintf(stderr, "[hewitt] Workspace %s %s; replacing its container (the volume and its commits are kept)\n", m.Name, reason); err != nil {
					return "", nil, err
				}
				return "", &m, nil
			}
			if err := warn(stderr, "workspace %s %s; it is updated once its %d connected agent(s) exit", m.Name, reason, len(live)); err != nil {
				return "", nil, err
			}
		}
		running, err := client.Running(ctx, m.ID)
		if err != nil {
			return "", nil, err
		}
		if !running {
			if err := reportStep(stderr, "Restarting saved workspace"); err != nil {
				return "", nil, err
			}
			if m.Egress != nil {
				if err := client.Start(ctx, m.Egress.Proxy); err != nil {
					return "", nil, err
				}
			}
			if err := client.Start(ctx, m.ID); err != nil {
				return "", nil, err
			}
		}
		if err := startObserver(ctx, m.Egress, stderr); err != nil {
			return "", nil, err
		}
		if _, err := fmt.Fprintf(stderr, "Reusing workspace %s (%s)\n", m.Name, m.ID[:12]); err != nil {
			return "", nil, err
		}
		return m.ID, nil, nil
	}
	return "", nil, nil
}

// workspaceRuns lists the live agent connections of one workspace.
func workspaceRuns(store *Store, id string) ([]AgentRun, error) {
	runs, err := store.RunningAgents()
	if err != nil {
		return nil, err
	}
	var live []AgentRun
	for _, run := range runs {
		if run.WorkspaceID == id {
			live = append(live, run)
		}
	}
	return live, nil
}

// OpenWorkspace starts a new foreground process, never a reconnect to an old
// agent. The host terminal (typically tmux) owns this connection's lifetime.
func OpenWorkspace(ctx context.Context, store *Store, id string, opts AgentOptions, in io.Reader, out, stderr io.Writer) error {
	m, err := store.Resolve(id)
	if err != nil {
		return err
	}
	if m.Terminal != "host" {
		return fmt.Errorf("session %s is not a host-terminal workspace", id)
	}
	s, err := mergeSettings(opts)
	if err != nil {
		return err
	}
	choice := s.Agent
	if opts.Shell {
		choice = "shell"
	}
	if !m.MountsAgent {
		return &OutdatedWorkspaceError{Workspace: m.Name, Reason: "was created by an older Hewitt that did not mount its hewitt-agent"}
	}
	_, launcher, err := agentScript(opts.DataDir)
	if err != nil {
		return err
	}
	command := []string{launcher, "run"}
	if choice != "workspace" {
		command = append(command, choice)
	}
	var chosen agent.Manifest
	if choice != "workspace" && choice != "shell" {
		if chosen, err = s.agents.Lookup(choice); err != nil {
			return err
		}
		if !slices.Contains(m.WorkspaceAgents(), choice) {
			return &OutdatedWorkspaceError{Workspace: m.Name, Reason: "was created before agent " + choice + " was added"}
		}
	}
	if opts.Prompt != "" && chosen.Task == nil {
		return fmt.Errorf("%s does not accept a task (tasks work with %s); start it without -p", choice, strings.Join(s.agents.TaskAgents(), ", "))
	}
	if opts.Task != "" {
		if err := ValidTaskName(opts.Task); err != nil {
			return err
		}
		if opts.Prompt == "" {
			return fmt.Errorf("task %s has no prompt", opts.Task)
		}
	}
	if choice != "workspace" && choice != "shell" {
		// The workspace mounts the tool cache read-only; install before exec.
		if err := ensureTool(ctx, opts.DataDir, m.Image, chosen, stderr); err != nil {
			return err
		}
	}
	run, release, err := store.RecordRun(m, choice)
	if err != nil {
		return fmt.Errorf("track agent connection: %w", err)
	}
	defer release()
	if opts.Started != nil {
		opts.Started(run.ID)
	}
	if _, err := fmt.Fprintf(stderr, "Agent run: %s (%s in %s)\n", run.ID[:12], choice, m.Name); err != nil {
		return fmt.Errorf("report agent run: %w", err)
	}
	if !run.Terminal.Kept() {
		if err := warn(stderr, "not inside tmux: %s ends when this terminal closes and cannot be reattached (start tmux first to keep agents reattachable)", choice); err != nil {
			return err
		}
	}
	if err := reportStep(stderr, "Opening "+choice+" in workspace"); err != nil {
		return err
	}
	env, files := runEnv(m.Egress, run.ID, choice), []string(nil)
	_, allow, err := s.envFile()
	if err != nil {
		return err
	}
	if chosen.Token != nil {
		// Chosen per launch, so a login after the workspace was created applies.
		token, tokenFiles, err := agentToken(opts.DataDir, m.Profile, chosen, m.Egress != nil)
		if err != nil {
			return err
		}
		env, files = append(env, token...), tokenFiles
		allow = append(allow, chosen.Token.Variable)
	}
	switch choice {
	case "shell":
	case "workspace":
		// hewitt-agent asks which agent to open.
		menu, err := menuEnv(s.agents, m.WorkspaceAgents(), s.Telemetry, allow)
		if err != nil {
			return err
		}
		env = append(env, menu...)
	default:
		launch, err := chosen.Launch(agent.OpRun, agent.LaunchOptions{Profile: agent.ProfilesPath + "/" + choice,
			Prompt: opts.Prompt, Headless: opts.Headless, Telemetry: s.Telemetry, Allow: allow})
		if err != nil {
			return err
		}
		env = append(env, launch.Env()...)
	}
	if opts.Task != "" {
		env = append(env, "HEWITT_TASK="+opts.Task)
	}
	if opts.Headless {
		// Without a terminal the agent prints its result and exits.
		in = strings.NewReader("")
	}
	// Requests of this launch appear where its agent waits for them.
	watch, stopWatch := context.WithCancel(ctx)
	go watchRequests(watch, store, m, run, stderr)
	client := docker.Client{}
	if !opts.Headless {
		// Lets a terminal manager such as Herdr recognize the agent.
		client.Env = terminal.AgentHint(run.Terminal, chosen.Herdr)
	}
	err = client.ExecInteractive(ctx, m.ID, env, files, command, in, out, stderr)
	stopWatch()
	if opts.Headless && ctx.Err() != nil {
		// Without a terminal, ending docker exec leaves the agent running.
		stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if serr := stopRun(stop, m.ID, run.ID); serr != nil {
			_ = warn(stderr, "could not stop %s in workspace %s: %v", choice, m.Name, serr)
		}
		cancel()
	}
	var exit *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exit) {
		err = &AgentExitError{Agent: choice, Workspace: m.Name, Code: exit.ExitCode(), Err: err}
	}
	if err == nil && opts.Headless {
		_, werr := fmt.Fprintf(stderr, "[hewitt] %s finished in workspace %s; hewitt sync %s imports its branch\n", choice, m.Name, m.Name)
		return werr
	}
	if err == nil || errors.Is(err, errInterrupted) {
		_, werr := fmt.Fprintf(stderr, "[hewitt] %s exited; workspace %s keeps running (hewitt run opens another agent, hewitt stop %s stops it)\n", choice, m.Name, m.Name)
		return werr
	}
	return err
}
