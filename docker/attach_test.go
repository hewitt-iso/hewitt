package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAttachStreams(t *testing.T) {
	log := fakeDocker(t, `IFS= read -r line
printf 'received: %s\n' "$line"
printf 'diagnostic\n' >&2`)
	var out, diagnostics bytes.Buffer
	err := (Client{}).Attach(context.Background(), testID, strings.NewReader("hello world\n"), &out, &diagnostics)
	if err != nil || out.String() != "received: hello world\n" || diagnostics.String() != "diagnostic\n" {
		t.Fatalf("attach = %q, %q, %v", out.String(), diagnostics.String(), err)
	}
	want := []string{"container", "attach", "--detach-keys=ctrl-p,ctrl-q", "--sig-proxy=false", "--", testID}
	if got := readArgs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}

func TestAttachErrors(t *testing.T) {
	fakeDocker(t, "printf 'container is stopped\n' >&2\nexit 17")
	var diagnostics bytes.Buffer
	err := (Client{}).Attach(context.Background(), testID, nil, io.Discard, &diagnostics)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 17 || !strings.Contains(err.Error(), testID) || diagnostics.String() != "container is stopped\n" {
		t.Fatalf("error = %v, diagnostics = %q", err, diagnostics.String())
	}
	t.Setenv("PATH", t.TempDir())
	err = (Client{}).Attach(context.Background(), testID, nil, io.Discard, io.Discard)
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing Docker: %v", err)
	}
}

func TestAttachValidation(t *testing.T) {
	log := fakeDocker(t, "exit 99")
	for _, id := range []string{"", "--privileged", "bad name", "bad\nname"} {
		if err := (Client{}).Attach(context.Background(), id, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Client{}).Attach(ctx, testID, nil, io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Docker invoked: %v", err)
	}
}

func TestAttachCancellation(t *testing.T) {
	log := fakeDocker(t, "exec /bin/sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (Client{}).Attach(ctx, testID, nil, io.Discard, io.Discard) }()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("Docker did not start")
		case <-ticker.C:
			if _, err := os.Stat(log); err != nil {
				continue
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("attach did not cancel")
			}
			return
		}
	}
}
