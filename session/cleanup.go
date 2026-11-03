package session

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/workspace"
)

// Stop stops a session's agent and preserves everything else. For exec
// sessions only Hewitt's terminal ends; the external container keeps running.
// It returns the resolved full session ID.
func Stop(ctx context.Context, store *Store, id string) (string, error) {
	m, err := store.Resolve(id)
	if err != nil {
		return "", err
	}
	client := docker.Client{}
	if m.Mode == "exec" {
		if err := client.StopExternalTerminal(ctx, m.Container, m.Socket); err != nil {
			return "", err
		}
		return m.ID, nil
	}
	if err := client.Stop(ctx, m.ID); err != nil {
		return "", err
	}
	if m.Egress != nil {
		if err := client.Stop(ctx, m.Egress.Proxy); err != nil {
			return "", err
		}
	}
	return m.ID, nil
}

// Remove deletes a stopped session's Hewitt-owned resources (including all
// worktrees, copies, and volumes) and then its metadata. Running sessions are
// refused. A volume's branches are synced first; uncommitted or untracked work
// in it refuses removal unless force discards it. Branches, direct source
// paths, and external containers are kept.
// A partial failure leaves the metadata, so rerunning finishes the cleanup.
// It returns the resolved full session ID.
func Remove(ctx context.Context, store *Store, id string, force bool) (string, error) {
	m, err := store.Resolve(id)
	if err != nil {
		return "", err
	}
	client := docker.Client{}
	if m.Mode == "exec" {
		running, err := client.ExternalTerminalRunning(ctx, m.Container, m.Socket)
		if err != nil {
			return "", err
		}
		if running {
			return "", fmt.Errorf("session %s is running in container %q; stop it first with hewitt stop", m.ID, m.Container)
		}
		if err := store.Delete(m.ID); err != nil {
			return "", err
		}
		return m.ID, nil
	}
	containers := []string{m.ID}
	if m.Egress != nil {
		containers = append(containers, m.Egress.Proxy)
	}
	for _, container := range containers {
		running, err := client.Running(ctx, container)
		if err != nil {
			return "", err
		}
		if running {
			return "", fmt.Errorf("container %s is running; stop the session first with hewitt stop %s", container, m.ID)
		}
	}
	if m.Volume != nil && !force {
		if err := checkVolume(ctx, m); err != nil {
			return "", err
		}
	}
	for _, container := range containers {
		if err := client.Remove(ctx, container); err != nil {
			return "", err
		}
	}
	if m.Egress != nil {
		if err := client.RemoveNetwork(ctx, m.Egress.Network); err != nil {
			return "", err
		}
		if err := os.RemoveAll(m.Egress.ConfigDir); err != nil {
			return "", fmt.Errorf("remove egress configuration: %w", err)
		}
	}
	if m.Workspace != nil {
		if err := workspace.RemoveAll(ctx, *m.Workspace); err != nil {
			return "", err
		}
	}
	if m.Volume != nil {
		if err := dropVolume(ctx, *m.Volume); err != nil {
			return "", err
		}
	}
	if m.Copy != nil {
		if err := workspace.RemoveCopy(ctx, *m.Copy); err != nil {
			return "", err
		}
	}
	if err := store.Delete(m.ID); err != nil {
		return "", err
	}
	return m.ID, nil
}

// Status reports whether a session is "running", "stopped", or "unknown" when
// Docker cannot be reached or inspected.
func Status(ctx context.Context, m Metadata) string {
	client := docker.Client{}
	if m.Mode == "exec" {
		running, err := client.ExternalTerminalRunning(ctx, m.Container, m.Socket)
		if err != nil {
			return "unknown"
		}
		if running {
			return "running"
		}
		return "stopped"
	}
	running, err := client.Running(ctx, m.ID)
	if err != nil {
		return "unknown"
	}
	if running {
		return "running"
	}
	return "stopped"
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}
