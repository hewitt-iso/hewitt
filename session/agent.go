package session

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/profile"
)

// AgentOptions describes one native client invocation. Run images must contain
// tmux, env, and the selected CLI on PATH and support execution as the invoking host user's UID/GID.
type AgentOptions struct {
	Agent, Image, Name, Profile, DataDir string
}

// RunAgent creates and starts a detached native agent with its persistent profile.
func RunAgent(ctx context.Context, opts AgentOptions) (string, error) {
	create, err := prepare(ctx, opts, false)
	if err != nil {
		return "", err
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return "", err
	}
	command := append([]string{create.Entrypoint}, create.Command...)
	create.Entrypoint = "tmux"
	create.Command = docker.TerminalServerArgs()
	metadata, err := createAgentContainer(ctx, store, opts.Agent, create, opts.Profile, "run", "tmux")
	if err != nil {
		return "", err
	}
	if err := (docker.Client{}).Start(ctx, metadata.ID); err != nil {
		return "", err
	}
	if err := (docker.Client{}).StartTerminal(ctx, metadata.ID, command); err != nil {
		return "", err
	}
	return metadata.ID, nil
}

// Login delegates subscription authentication to the native client in a terminal.
// Returning nil means its login process exited successfully, not a separate
// Hewitt verification of the subscription or entitlement.
func Login(ctx context.Context, opts AgentOptions, in io.Reader, out, stderr io.Writer) error {
	create, err := prepare(ctx, opts, true)
	if err != nil {
		return err
	}
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return err
	}
	metadata, err := createAgentContainer(ctx, store, opts.Agent, create, opts.Profile, "login", "")
	if err != nil {
		return err
	}
	id := metadata.ID
	if _, err := fmt.Fprintf(stderr, "Login container: %s (preserved; remove with docker rm after exit)\n", id); err != nil {
		return fmt.Errorf("report login container %q: %w", id, err)
	}
	return (docker.Client{}).StartAttached(ctx, id, in, out, stderr)
}

func prepare(ctx context.Context, opts AgentOptions, login bool) (docker.CreateOptions, error) {
	if err := ctx.Err(); err != nil {
		return docker.CreateOptions{}, err
	}
	adapter, err := agent.Lookup(opts.Agent)
	if err != nil {
		return docker.CreateOptions{}, err
	}
	inv := adapter.Run()
	if login {
		inv = adapter.Login()
	}
	create := docker.CreateOptions{
		Image: opts.Image, Name: opts.Name, Entrypoint: inv.Entrypoint,
		Command: inv.Args, Env: inv.Env, Workdir: "/tmp",
		DisableLogs: login,
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
