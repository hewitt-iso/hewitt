package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/workspace"
)

// Stop stops a session's agent and preserves everything else. For exec
// sessions only Hewitt's terminal ends; the external container keeps running.
func Stop(ctx context.Context, store *Store, id string) error {
	m, err := store.Get(id)
	if err != nil {
		return err
	}
	client := docker.Client{}
	if m.Mode == "exec" {
		return client.StopExternalTerminal(ctx, m.Container, m.Socket)
	}
	if err := client.Stop(ctx, m.ID); err != nil {
		return err
	}
	if m.Egress != nil {
		return client.Stop(ctx, m.Egress.Proxy)
	}
	return nil
}

// Remove deletes a stopped session's Hewitt-owned resources and then its
// metadata. Every check runs before anything is removed: running agents,
// uncommitted or ignored worktree files, and unreturned copy changes refuse
// removal. Branches, direct source paths, and external containers are kept.
// A partial failure leaves the metadata, so rerunning finishes the cleanup.
func Remove(ctx context.Context, store *Store, id string) error {
	m, err := store.Get(id)
	if err != nil {
		return err
	}
	client := docker.Client{}
	if m.Mode == "exec" {
		running, err := client.ExternalTerminalRunning(ctx, m.Container, m.Socket)
		if err != nil {
			return err
		}
		if running {
			return fmt.Errorf("session %s is running in container %q; stop it first with hewitt stop", id, m.Container)
		}
		return store.Delete(id)
	}
	containers := []string{m.ID}
	if m.Egress != nil {
		containers = append(containers, m.Egress.Proxy)
	}
	for _, container := range containers {
		running, err := client.Running(ctx, container)
		if err != nil {
			return err
		}
		if running {
			return fmt.Errorf("container %s is running; stop the session first with hewitt stop %s", container, id)
		}
	}
	worktree := m.Workspace != nil && exists(m.Workspace.Path)
	if worktree {
		if err := workspace.CheckRemovable(ctx, *m.Workspace); err != nil {
			return fmt.Errorf("session %s kept: %w", id, err)
		}
	}
	if m.Copy != nil {
		if err := workspace.CheckCopyRemovable(ctx, *m.Copy); err != nil {
			return fmt.Errorf("session %s kept: %w", id, err)
		}
	}
	for _, container := range containers {
		if err := client.Remove(ctx, container); err != nil {
			return err
		}
	}
	if m.Egress != nil {
		if err := client.RemoveNetwork(ctx, m.Egress.Network); err != nil {
			return err
		}
		if err := os.RemoveAll(m.Egress.ConfigDir); err != nil {
			return fmt.Errorf("remove egress configuration: %w", err)
		}
	}
	if worktree {
		if err := workspace.Remove(ctx, *m.Workspace); err != nil {
			return err
		}
	}
	if m.Copy != nil {
		if err := workspace.RemoveCopy(ctx, *m.Copy); err != nil {
			return err
		}
	}
	return store.Delete(id)
}

// Diff writes a session's changes. Direct and exec sessions edit files in
// place, so their changes belong to the source repository's own history.
func Diff(ctx context.Context, store *Store, id string, out io.Writer) error {
	m, err := store.Get(id)
	if err != nil {
		return err
	}
	switch {
	case m.Workspace != nil:
		return workspace.Diff(ctx, *m.Workspace, out)
	case m.Copy != nil:
		return workspace.DiffCopy(ctx, *m.Copy, out)
	default:
		return fmt.Errorf("session %s has no separate workspace; its changes are in the source paths (use git diff there)", id)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}
