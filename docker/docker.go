// Package docker manages containers through the Docker CLI.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"
)

// Client uses the Docker executable on PATH and the user's Docker context.
// Its zero value is ready to use.
type Client struct{}

// CreateOptions describes a container. Host access requires explicit mounts.
type CreateOptions struct {
	Image       string
	Name        string
	Command     []string
	Entrypoint  string
	Env         []string
	Mounts      []BindMount
	User        string
	Workdir     string
	DisableLogs bool
	DisableTTY  bool
}

// BindMount grants access to one explicitly selected host directory.
type BindMount struct{ Source, Target string }

// Validate checks options before any filesystem or Docker side effects.
func (opts CreateOptions) Validate() error {
	if err := validateArgument("image", opts.Image); err != nil {
		return err
	}
	if opts.Name != "" {
		if err := validateArgument("container name", opts.Name); err != nil {
			return err
		}
	}
	for _, value := range []struct{ name, value string }{{"entrypoint", opts.Entrypoint}, {"user", opts.User}} {
		if value.value != "" {
			if err := validateArgument(value.name, value.value); err != nil {
				return err
			}
		}
	}
	if opts.Workdir != "" && (!strings.HasPrefix(opts.Workdir, "/") || strings.ContainsAny(opts.Workdir, "\x00\r\n")) {
		return fmt.Errorf("container workdir must be an absolute path")
	}
	for _, env := range opts.Env {
		key, _, ok := strings.Cut(env, "=")
		if !ok || key == "" || strings.ContainsAny(env, "\x00\r\n") {
			return fmt.Errorf("container environment must contain explicit KEY=value entries")
		}
		for i, c := range key {
			if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
				return fmt.Errorf("invalid container environment key")
			}
		}
	}
	for _, mount := range opts.Mounts {
		if !filepath.IsAbs(mount.Source) || !strings.HasPrefix(mount.Target, "/") || strings.ContainsAny(mount.Source+mount.Target, ",\x00\r\n") {
			return fmt.Errorf("bind mount paths must be absolute and contain no commas or control characters")
		}
	}
	for _, arg := range opts.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("container command contains NUL")
		}
	}
	return nil
}

// Create creates a stopped container and returns its ID. Missing images are
// pulled by Docker. With no Command, the image's default command is used.
// The caller owns the container after creation, including cleanup on failure.
func (c Client) Create(ctx context.Context, opts CreateOptions) (string, error) {
	if err := opts.Validate(); err != nil {
		return "", err
	}
	args := []string{
		"container", "create",
		"--pull", "missing",
		"--init", "--interactive",
	}
	if !opts.DisableTTY {
		args = append(args, "--tty")
	}
	args = append(args, "--label", "io.hewitt.managed=true",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges=true")
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if opts.DisableLogs {
		args = append(args, "--log-driver", "none")
	}
	if opts.Entrypoint != "" {
		args = append(args, "--entrypoint", opts.Entrypoint)
	}
	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	if opts.Workdir != "" {
		args = append(args, "--workdir", opts.Workdir)
	}
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}
	for _, mount := range opts.Mounts {
		args = append(args, "--mount", "type=bind,src="+mount.Source+",dst="+mount.Target)
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

// StartAttached streams directly to the terminal without recording login output.
// It checks the container's exit status, not merely Docker's attach success.
// Containers are preserved on success and failure for explicit caller cleanup.
func (c Client) StartAttached(ctx context.Context, id string, in io.Reader, out, stderr io.Writer) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "docker", "container", "start", "--attach", "--interactive", "--", id)
	command.Stdin, command.Stdout, command.Stderr = in, out, stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("attached container %q (container preserved): %w", id, err)
	}
	// Inspect instead of waiting: detach must return while the container is alive.
	result, err := c.execute(ctx, "container", "inspect", "--format", "{{json .State}}", "--", id)
	if err != nil {
		return fmt.Errorf("inspect container %q (container preserved): %w", id, err)
	}
	var state struct {
		Status   string
		ExitCode int
	}
	if err := json.Unmarshal([]byte(result), &state); err != nil || state.Status == "" {
		return fmt.Errorf("container %q returned invalid state (container preserved)", id)
	}
	if state.Status != "exited" {
		return fmt.Errorf("detached from container %q (state %s); authentication completion not confirmed; reconnect with docker attach %s", id, state.Status, id)
	}
	if state.ExitCode != 0 {
		return fmt.Errorf("container %q exited with status %d (container preserved)", id, state.ExitCode)
	}
	return nil
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
func (Client) Attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer) error {
	return attach(ctx, id, stdin, stdout, stderr, []string{"container", "attach", "--detach-keys=ctrl-p,ctrl-q", "--sig-proxy=false", "--", id})
}

func attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer, args []string) (result error) {
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
	cmd := exec.CommandContext(ctx, "docker", args...)
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
