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
