package session

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/workspace"
)

// AgentOptions describes one native client invocation. Run images must contain
// tmux, env, and the selected CLI on PATH and support execution as the invoking host user's UID/GID.
type AgentOptions struct {
	Agent, Image, Name, Profile, DataDir string
	Repository, Base                     string
}

// RunAgent creates and starts a detached native agent with its persistent profile.
func RunAgent(ctx context.Context, opts AgentOptions) (string, error) {
	create, err := prepare(ctx, opts, "run")
	if err != nil {
		return "", err
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return "", err
	}
	worktree, err := workspace.Create(ctx, opts.DataDir, opts.Repository, opts.Base)
	if err != nil {
		return "", err
	}
	create.Workdir = worktree.Path
	create.Mounts = append(create.Mounts, docker.BindMount{Source: worktree.Path, Target: worktree.Path}, docker.BindMount{Source: worktree.GitDir, Target: worktree.GitDir})
	command := append([]string{create.Entrypoint}, create.Command...)
	create.Entrypoint = "tmux"
	create.Command = docker.TerminalServerArgs()
	metadata, err := createAgentContainer(ctx, store, opts.Agent, create, opts.Profile, "run", "tmux", &worktree)
	if err != nil {
		return "", fmt.Errorf("worktree %q (branch %s) preserved: %w", worktree.Path, worktree.Branch, err)
	}
	if err := (docker.Client{}).Start(ctx, metadata.ID); err != nil {
		return "", fmt.Errorf("worktree %q preserved: %w", worktree.Path, err)
	}
	if err := (docker.Client{}).StartTerminal(ctx, metadata.ID, command); err != nil {
		return "", fmt.Errorf("worktree %q preserved: %w", worktree.Path, err)
	}
	return metadata.ID, nil
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
	create, err := prepare(ctx, opts, operation)
	if err != nil {
		return err
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return err
	}
	metadata, err := createAgentContainer(ctx, store, opts.Agent, create, opts.Profile, operation, "")
	if err != nil {
		return err
	}
	id := metadata.ID
	if _, err := fmt.Fprintf(stderr, "Authentication container: %s (preserved; remove with docker rm after exit)\n", id); err != nil {
		return fmt.Errorf("report login container %q: %w", id, err)
	}
	return (docker.Client{}).StartAttached(ctx, id, in, out, stderr)
}

func prepare(ctx context.Context, opts AgentOptions, operation string) (docker.CreateOptions, error) {
	if err := ctx.Err(); err != nil {
		return docker.CreateOptions{}, err
	}
	adapter, err := agent.Lookup(opts.Agent)
	if err != nil {
		return docker.CreateOptions{}, err
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
		return docker.CreateOptions{}, err
	}
	path, err := profile.Prepare(opts.DataDir, adapter.Name(), opts.Profile)
	if err != nil {
		return docker.CreateOptions{}, err
	}
	create.Mounts = []docker.BindMount{{Source: path, Target: agent.ProfilePath}}
	if err := create.Validate(); err != nil {
		return docker.CreateOptions{}, err
	}
	return create, nil
}
