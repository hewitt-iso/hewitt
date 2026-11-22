package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/profile"
)

// toolsPath is where the shared tool cache appears in agent containers. Agent
// containers mount it read-only, so a session cannot replace executables that
// later sessions of any project run; only installer containers write to it.
const toolsPath = "/hewitt/tools"

// ensureTool installs agent into the shared tool cache unless its executable
// is already there. Installed agents need no lock, so any number of runs can
// start in parallel; installations are serialized and checked again under the
// lock, so concurrent runs never write the same files twice. The installer has
// network access but no project, profile, or credentials, and it is removed
// after it exits.
func ensureTool(ctx context.Context, dataDir, image string, a agent.Manifest, stderr io.Writer) error {
	tools, err := profile.PrepareTools(dataDir)
	if err != nil {
		return err
	}
	if toolInstalled(tools, a.Binary) {
		return nil
	}
	unlock, err := lockTools(ctx, dataDir, stderr)
	if err != nil {
		return err
	}
	defer unlock()
	if toolInstalled(tools, a.Binary) {
		return nil
	}
	if err := reportStep(stderr, "Installing "+a.Name+" into the tool cache"); err != nil {
		return err
	}
	script, path, err := agentScript(dataDir)
	if err != nil {
		return err
	}
	opts := docker.CreateOptions{
		Image: image, Entrypoint: path, Command: []string{"install"}, Env: a.InstallEnv(),
		User:   fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		Mounts: []docker.BindMount{{Source: tools, Target: toolsPath}, script},
		// Installer output is progress for the user, not command output.
		DisableLogs: true, DisableTTY: true,
	}
	if err := (docker.Client{}).Run(ctx, opts, stderr); err != nil {
		return fmt.Errorf("install %s: %w", a.Name, err)
	}
	return nil
}

// toolInstalled reports whether the cache has the agent's executable. The
// installer moves it into place last, so its presence means a complete
// installation. Links are not followed: they name container paths.
func toolInstalled(tools, binary string) bool {
	_, err := os.Lstat(filepath.Join(tools, "bin", binary))
	return err == nil
}

// lockTools serializes installations into the shared tool cache. The kernel
// releases the lock even when a CLI is killed during an installation.
func lockTools(ctx context.Context, dataDir string, stderr io.Writer) (func(), error) {
	dir := filepath.Join(dataDir, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create tool cache lock directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "tools"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open tool cache lock: %w", err)
	}
	waiting := false
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, fmt.Errorf("lock tool cache: %w", err)
		}
		if !waiting {
			waiting = true
			if err := reportStep(stderr, "Waiting for another agent installation to finish"); err != nil {
				f.Close()
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
