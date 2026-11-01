// Package docker manages containers through the Docker CLI.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"
)

// Client uses the Docker executable on PATH and the user's Docker context.
// Its zero value is ready to use.
type Client struct{}

// CreateOptions describes a container without granting access to host files.
type CreateOptions struct {
	Image   string
	Name    string
	Command []string
}

// Create creates a stopped container and returns its ID. Missing images are
// pulled by Docker. With no Command, the image's default command is used.
// The caller owns the container after creation, including cleanup on failure.
func (c Client) Create(ctx context.Context, opts CreateOptions) (string, error) {
	if err := validateArgument("image", opts.Image); err != nil {
		return "", err
	}
	if opts.Name != "" {
		if err := validateArgument("container name", opts.Name); err != nil {
			return "", err
		}
	}
	args := []string{
		"container", "create",
		"--pull", "missing",
		"--init", "--interactive", "--tty",
		"--label", "io.hewitt.managed=true",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true",
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	args = append(args, "--", opts.Image)
	args = append(args, opts.Command...)
	out, err := c.execute(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	id := strings.TrimSpace(out)
	if len(id) != 64 || strings.ContainsAny(id, "\r\n") || strings.Trim(id, "0123456789abcdef") != "" {
		return "", fmt.Errorf("create container: Docker returned an invalid container ID; inspect containers with label io.hewitt.managed=true")
	}
	return id, nil
}

// Start starts a container by ID or name without attaching to it. Success means
// Docker accepted startup, not that the process stays alive or is ready.
// On failure the container is preserved for inspection or retry.
func (c Client) Start(ctx context.Context, id string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "container", "start", "--", id); err != nil {
		return fmt.Errorf("start container %q (container preserved): %w", id, err)
	}
	return nil
}

// Attach connects streams to an existing container process. Docker owns terminal
// setup and the Ctrl-P, Ctrl-Q detach sequence. Attachment never starts a container.
func (Client) Attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer) (result error) {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("attach container %q: %w", id, err)
	}
	// Docker normally restores terminal settings itself. Keep a snapshot because
	// CommandContext may kill it before its cleanup runs during cancellation.
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fd := int(f.Fd())
		state, err := term.GetState(fd)
		if err != nil {
			return fmt.Errorf("read terminal state: %w", err)
		}
		defer func() {
			if err := term.Restore(fd, state); err != nil {
				result = errors.Join(result, fmt.Errorf("restore terminal state: %w", err))
			}
		}()
	}
	cmd := exec.CommandContext(ctx, "docker", "container", "attach", "--detach-keys=ctrl-p,ctrl-q", "--sig-proxy=false", "--", id)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if errors.Is(err, exec.ErrNotFound) {
			err = fmt.Errorf("ensure the Docker CLI is installed: %w", err)
		}
		return fmt.Errorf("attach container %q: %w", id, err)
	}
	return nil
}

func (Client) execute(ctx context.Context, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("docker: %w: %s", err, detail)
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("docker (ensure the Docker CLI is installed): %w", err)
		}
		return "", fmt.Errorf("docker: %w", err)
	}
	return stdout.String(), nil
}

func validateArgument(name, value string) error {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return fmt.Errorf("%s must be nonempty, contain no whitespace or control characters, and not start with '-'", name)
	}
	return nil
}
