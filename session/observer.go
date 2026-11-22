package session

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/observer"
)

// observerToken marks the workspace container for its observer.
func observerToken() string { return strings.ToLower(rand.Text()) }

// createObserver creates a stopped observer for the workspace container whose
// environment carries token. It watches every agent profile, held unless the
// owning agent opens it, and the workspace volume, where credential files are
// held. The observer starts as root with CAP_SYS_ADMIN, CAP_SYS_PTRACE, and
// CAP_DAC_READ_SEARCH in the host PID namespace only to find the container
// and set its marks, then runs as the user; it has no network.
func createObserver(ctx context.Context, image string, e *Egress, token string, agents []string) error {
	cfg := observer.Config{Token: token, UID: os.Getuid(), GID: os.Getgid(), StateDir: "/hewitt/state",
		Watches: []observer.Watch{{Path: volumeRoot}}}
	for _, name := range agents {
		cfg.Watches = append(cfg.Watches, observer.Watch{Path: agent.ProfilesPath + "/" + name, Owner: name})
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode observer configuration: %w", err)
	}
	path := filepath.Join(e.ConfigDir, "observer.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write observer configuration: %w", err)
	}
	id, err := (docker.Client{}).Create(ctx, docker.CreateOptions{
		Image: image, Entrypoint: "hewitt", Command: []string{"observe", "/hewitt/observer/config.json"},
		Mounts: []docker.BindMount{
			{Source: path, Target: "/hewitt/observer/config.json", ReadOnly: true},
			{Source: e.StateDir(), Target: "/hewitt/state"},
		},
		User: "0:0", CapAdd: []string{"SYS_ADMIN", "SYS_PTRACE", "DAC_READ_SEARCH", "SETUID", "SETGID"},
		PIDHost: true, Network: "none", Workdir: "/tmp", ReadOnlyRoot: true, DisableTTY: true,
	})
	if err != nil {
		return fmt.Errorf("create observer: %w", err)
	}
	e.Observer = id
	return nil
}

// startObserver starts the observer after its workspace container. An
// observer that cannot attach, such as on a kernel without fanotify
// permission events, is reported; the session runs without held file access.
func startObserver(ctx context.Context, e *Egress, stderr io.Writer) error {
	if e == nil || e.Observer == "" {
		return nil
	}
	client := docker.Client{}
	if running, err := client.Running(ctx, e.Observer); err != nil || running {
		return err
	}
	if err := client.Start(ctx, e.Observer); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	if running, err := client.Running(ctx, e.Observer); err != nil || running {
		return err
	}
	return warn(stderr, "the file observer stopped, so agents' access to other agents' logins and credential files is not held; docker logs %s shows why", e.Observer[:12])
}
