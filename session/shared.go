package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hewitt-iso/hewitt/docker"
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

func reuseWorkspace(ctx context.Context, dataDir, key string, stderr io.Writer) (string, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return "", err
	}
	records, err := store.List()
	if err != nil {
		return "", err
	}
	for _, m := range records {
		if m.Terminal != "host" || m.WorkspaceKey != key {
			continue
		}
		client := docker.Client{}
		running, err := client.Running(ctx, m.ID)
		if err != nil {
			return "", err
		}
		if !running {
			if m.Egress != nil {
				if err := client.Start(ctx, m.Egress.Proxy); err != nil {
					return "", err
				}
			}
			if err := client.Start(ctx, m.ID); err != nil {
				return "", err
			}
		}
		if _, err := fmt.Fprintf(stderr, "Reusing workspace %s (%s)\n", m.Name, m.ID); err != nil {
			return "", err
		}
		return m.ID, nil
	}
	return "", nil
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
	command := []string{"hewitt-agent", "run"}
	if choice != "workspace" {
		command = append(command, choice)
	}
	return (docker.Client{}).ExecInteractive(ctx, m.ID, command, in, out, stderr)
}
