package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCreateAndStart(t *testing.T) {
	log := fakeDocker(t, `printf '%s\n' '`+testID+`'`)
	client := Client{}
	command := []string{"echo", "hello world", "$(touch should-not-exist)", "--flag"}
	id, err := client.Create(context.Background(), CreateOptions{
		Image: "example/agent:test", Name: "hewitt-test", Command: command,
	})
	if err != nil || id != testID {
		t.Fatalf("Create = %q, %v", id, err)
	}
	want := []string{
		"container", "create", "--pull", "missing", "--init", "--interactive", "--tty",
		"--label", "io.hewitt.managed=true", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true", "--name", "hewitt-test",
		"--", "example/agent:test",
	}
	want = append(want, command...)
	if got := readArgs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("create args = %q, want %q", got, want)
	}
	if err := client.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got, want := readArgs(t, log), []string{"container", "start", "--", testID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("start args = %q, want %q", got, want)
	}
}

func TestCreateImageDefaults(t *testing.T) {
	log := fakeDocker(t, `printf '%s\n' '`+testID+`'`)
	if _, err := (Client{}).Create(context.Background(), CreateOptions{Image: "example/agent:test"}); err != nil {
		t.Fatal(err)
	}
	args := readArgs(t, log)
	for _, forbidden := range []string{"--name", "--entrypoint", "--privileged", "--mount", "--volume", "--publish", "--env", "--network", "--pid"} {
		for _, arg := range args {
			if arg == forbidden {
				t.Errorf("unexpected option %q", forbidden)
			}
		}
	}
	if args[len(args)-1] != "example/agent:test" {
		t.Fatalf("default command was overridden: %q", args)
	}
}

func TestValidationDoesNotInvokeDocker(t *testing.T) {
	log := fakeDocker(t, "exit 99")
	client := Client{}
	for _, value := range []string{"", " ", "--privileged", "bad name", "bad\nname", "bad\x00name"} {
		if _, err := client.Create(context.Background(), CreateOptions{Image: value}); err == nil {
			t.Errorf("Create accepted image %q", value)
		}
		if err := client.Start(context.Background(), value); err == nil {
			t.Errorf("Start accepted identifier %q", value)
		}
		if value != "" {
			if _, err := client.Create(context.Background(), CreateOptions{Image: "example/agent:test", Name: value}); err == nil {
				t.Errorf("Create accepted name %q", value)
			}
		}
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Docker was invoked for invalid input: %v", err)
	}
}

func TestDockerErrors(t *testing.T) {
	fakeDocker(t, "printf 'daemon unavailable\n' >&2\nexit 17")
	_, err := (Client{}).Create(context.Background(), CreateOptions{Image: "example/agent:test"})
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 17 || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("expected wrapped Docker error and diagnostic, got %v", err)
	}
	err = (Client{}).Start(context.Background(), testID)
	if !errors.As(err, &exitErr) || !strings.Contains(err.Error(), testID) || !strings.Contains(err.Error(), "preserved") {
		t.Fatalf("expected retained container ID in start error, got %v", err)
	}
}

func TestMissingDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (Client{}).Create(context.Background(), CreateOptions{Image: "example/agent:test"})
	if !errors.Is(err, exec.ErrNotFound) || !strings.Contains(err.Error(), "Docker CLI is installed") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestCreateInvalidOutput(t *testing.T) {
	for _, output := range []string{"", "not-an-id", testID + "\nextra", strings.Repeat("z", 64)} {
		t.Run(output, func(t *testing.T) {
			fakeDocker(t, `printf '%s' "$HEWITT_TEST_OUTPUT"`)
			t.Setenv("HEWITT_TEST_OUTPUT", output)
			if _, err := (Client{}).Create(context.Background(), CreateOptions{Image: "example/agent:test"}); err == nil || !strings.Contains(err.Error(), "invalid container ID") {
				t.Fatalf("expected invalid output error, got %v", err)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	log := fakeDocker(t, "exec /bin/sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Client{}).Start(ctx, testID) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("Docker process did not start")
		case <-ticker.C:
			if _, err := os.Stat(log); err == nil {
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected wrapped cancellation, got %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Docker process did not stop after cancellation")
				}
				return
			}
		}
	}
}

func TestAlreadyCanceledDoesNotInvokeDocker(t *testing.T) {
	log := fakeDocker(t, "exit 99")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Client{}).Create(ctx, CreateOptions{Image: "example/agent:test"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Docker was invoked with canceled context: %v", err)
	}
}

func fakeDocker(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$HEWITT_TEST_ARGS\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HEWITT_TEST_ARGS", log)
	return log
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}
