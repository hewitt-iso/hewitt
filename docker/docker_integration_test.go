//go:build integration

package docker

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestContainerLifecycle is opt-in because it creates a real Docker container.
// The selected Linux image must supply the sleep executable and no entrypoint.
func TestContainerLifecycle(t *testing.T) {
	image := os.Getenv("HEWITT_TEST_IMAGE")
	if image == "" {
		t.Skip("set HEWITT_TEST_IMAGE to a Linux image with sleep to run Docker integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := Client{}
	id, err := client.Create(ctx, CreateOptions{Image: image, Command: []string{"sleep", "300"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "container", "rm", "--force", "--volumes", "--", id).CombinedOutput(); err != nil {
			t.Errorf("clean up test container %s: %v: %s", id, err, out)
		}
	})
	inspect := func(format, want string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "container", "inspect", "--format", format, "--", id).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("inspect %s: output = %q, error = %v, want %q", format, out, err, want)
		}
	}
	inspect("{{.State.Status}}", "created")
	inspect(`{{index .Config.Labels "io.hewitt.managed"}}`, "true")
	inspect("{{.HostConfig.Privileged}}", "false")
	inspect("{{len .HostConfig.Binds}}", "0")
	if err := client.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	inspect("{{.State.Status}}", "running")
}

// TestPersistentTerminal requires tmux and env in HEWITT_TEST_IMAGE.
func TestPersistentTerminal(t *testing.T) {
	image := os.Getenv("HEWITT_TEST_IMAGE")
	if image == "" {
		t.Skip("set HEWITT_TEST_IMAGE to an image with tmux, env, and sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := Client{}
	id, err := client.Create(ctx, CreateOptions{Image: image, Entrypoint: "tmux", Command: TerminalServerArgs()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(cleanup, "docker", "container", "rm", "--force", "--volumes", "--", id).CombinedOutput(); err != nil {
			t.Errorf("cleanup %s: %v: %s", id, err, out)
		}
	})
	if err := client.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := client.StartTerminal(ctx, id, []string{"env", "sh", "-c", "printf 'persistent-screen\\n'; sleep 3"}); err != nil {
		t.Fatal(err)
	}
	// Control clients need no host terminal. Each attach/detach must leave the
	// original pane running, and must not create another session or launch an agent.
	for range 2 {
		args := []string{"container", "exec", "--interactive", "--", id, "tmux", "-C", "-N", "-S", terminalSocket, "attach-session", "-t", "=hewitt"}
		command := exec.CommandContext(ctx, "docker", args...)
		command.Stdin = strings.NewReader("detach-client\n")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("reconnect: %v: %s", err, out)
		}
	}
	// An exiting agent ends the session, so the foreground server exits and the
	// container stops instead of keeping a dead pane.
	for {
		state, err := client.execute(ctx, "container", "inspect", "--format", "{{.State.Running}}", "--", id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(state) == "false" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
