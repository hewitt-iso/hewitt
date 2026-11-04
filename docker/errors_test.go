package docker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommandErrorKinds(t *testing.T) {
	for _, tt := range []struct {
		diagnostic string
		kind       *Kind
	}{
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", ErrDaemonUnavailable},
		{"failed to connect to the docker API at unix:///x; check if the path is correct and if the daemon is running", ErrDaemonUnavailable},
		{"permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock", ErrSocketPermission},
		{"Error response from daemon: No such container: abc", ErrNotFound},
		{"Error response from daemon: pull access denied for nope, repository does not exist", ErrImagePullForbidden},
	} {
		fakeDocker(t, "printf '%s\\n' '"+tt.diagnostic+"' >&2; exit 1")
		_, err := (Client{}).Running(context.Background(), "abc")
		if tt.kind == ErrNotFound {
			_, err = (Client{}).ContainerImage(context.Background(), "abc")
		}
		if !errors.Is(err, tt.kind) || tt.kind.Hint() == "" && tt.kind != ErrNotFound {
			t.Errorf("%q: error = %v, want kind %q", tt.diagnostic, err, tt.kind)
		}
		if err != nil && (strings.Contains(err.Error(), "exit status") || strings.Contains(err.Error(), "Error response from daemon")) {
			t.Errorf("error repeats Docker noise: %v", err)
		}
	}
}

func TestImageID(t *testing.T) {
	fakeDocker(t, `printf 'sha256:abc 0\n'`)
	id, pulled, err := (Client{}).ImageID(context.Background(), "hewitt:local")
	if err != nil || id != "sha256:abc" || pulled {
		t.Fatalf("local image = %q, %v, %v", id, pulled, err)
	}
	fakeDocker(t, `printf 'Error: No such image: x\n' >&2; exit 1`)
	if id, _, err := (Client{}).ImageID(context.Background(), "x"); err != nil || id != "" {
		t.Fatalf("missing image = %q, %v", id, err)
	}
}
