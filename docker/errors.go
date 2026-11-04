package docker

import (
	"errors"
	"strings"
)

// Kind classifies a Docker failure. Match it with errors.Is; Hint says what the
// user can do about it.
type Kind struct{ msg, hint string }

func (k *Kind) Error() string { return k.msg }
func (k *Kind) Hint() string  { return k.hint }

var (
	ErrCLIMissing         = &Kind{"Docker CLI not found on PATH", "install Docker and make sure docker runs in this shell"}
	ErrDaemonUnavailable  = &Kind{"Docker daemon is not reachable", "start Docker (or Docker Desktop) and retry"}
	ErrSocketPermission   = &Kind{"no permission to use the Docker socket", "add your user to the docker group (then log in again) or use rootless Docker"}
	ErrNotFound           = &Kind{"Docker object not found", ""}
	ErrImagePullForbidden = &Kind{"image could not be pulled", "check the image name, run docker login for private registries, or build it locally"}
)

// CommandError keeps Docker's own diagnostic instead of only its exit status.
type CommandError struct {
	Command string
	Detail  string
	Kind    *Kind
	Err     error
}

func newCommandError(args []string, detail string, err error) *CommandError {
	command := args[0]
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		command += " " + args[1]
	}
	detail = strings.TrimPrefix(detail, "Error response from daemon: ")
	detail = strings.TrimPrefix(detail, "Error: ")
	return &CommandError{Command: command, Detail: detail, Kind: classify(detail), Err: err}
}

func classify(detail string) *Kind {
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "cannot connect to the docker daemon"), strings.Contains(lower, "is the docker daemon running"),
		strings.Contains(lower, "failed to connect to the docker api"):
		return ErrDaemonUnavailable
	case strings.Contains(lower, "permission denied") && strings.Contains(lower, "docker"):
		return ErrSocketPermission
	case strings.Contains(lower, "pull access denied"), strings.Contains(lower, "manifest unknown"):
		return ErrImagePullForbidden
	case strings.Contains(lower, "no such container"), strings.Contains(lower, "no such network"),
		strings.Contains(lower, "no such volume"), strings.Contains(lower, "no such image"),
		strings.Contains(lower, "not found"):
		return ErrNotFound
	}
	return nil
}

func (e *CommandError) Error() string {
	detail := e.Detail
	if detail == "" {
		detail = e.Err.Error()
	}
	return "docker " + e.Command + ": " + detail
}

func (e *CommandError) Unwrap() []error {
	if e.Kind == nil {
		return []error{e.Err}
	}
	return []error{e.Kind, e.Err}
}

// IsNotFound reports whether Docker said a container, network, image, or volume does not exist.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
