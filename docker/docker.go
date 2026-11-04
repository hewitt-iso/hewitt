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
	Image      string
	Name       string
	Command    []string
	Entrypoint string
	Env        []string
	// EnvFiles are read by the Docker CLI; explicit Env entries take precedence.
	EnvFiles []string
	Mounts   []BindMount
	Volumes  []VolumeMount
	User     string
	// CapAdd restores single capabilities after --cap-drop ALL; only one-shot
	// helpers use it.
	CapAdd      []string
	Workdir     string
	DisableLogs bool
	DisableTTY  bool
	// ReadOnlyRoot mounts the image filesystem read-only with a private /tmp.
	ReadOnlyRoot bool
	// Network is passed to --network when set, for example none or container:<id>.
	Network string
	// Runtime selects an installed OCI runtime such as runsc.
	Runtime string
	// Resource limits are applied only when set, using Docker's value syntax.
	Memory, CPUs, PIDs string
}

// BindMount grants access to one explicitly selected host directory.
type BindMount struct {
	Source, Target string
	ReadOnly       bool
}

// VolumeMount mounts a named Docker volume.
type VolumeMount struct {
	Name, Target string
}

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
	for _, value := range []struct{ name, value string }{
		{"entrypoint", opts.Entrypoint}, {"user", opts.User}, {"network", opts.Network}, {"runtime", opts.Runtime},
		{"memory limit", opts.Memory}, {"CPU limit", opts.CPUs}, {"PIDs limit", opts.PIDs},
	} {
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
	for _, file := range opts.EnvFiles {
		if !filepath.IsAbs(file) || strings.ContainsAny(file, "\x00\r\n") {
			return fmt.Errorf("container env file must be an absolute path")
		}
	}
	for _, mount := range opts.Mounts {
		if !filepath.IsAbs(mount.Source) || !strings.HasPrefix(mount.Target, "/") || strings.ContainsAny(mount.Source+mount.Target, ",\x00\r\n") {
			return fmt.Errorf("bind mount paths must be absolute and contain no commas or control characters")
		}
	}
	for _, volume := range opts.Volumes {
		if err := validateArgument("volume name", volume.Name); err != nil {
			return err
		}
		if !strings.HasPrefix(volume.Target, "/") || strings.ContainsAny(volume.Name+volume.Target, ",\x00\r\n") {
			return fmt.Errorf("volume targets must be absolute and contain no commas or control characters")
		}
	}
	for _, capability := range opts.CapAdd {
		if err := validateArgument("capability", capability); err != nil {
			return err
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
	args := []string{"container", "create", "--pull", "missing", "--init", "--interactive"}
	if !opts.DisableTTY {
		args = append(args, "--tty")
	}
	args = append(args, containerArgs(opts)...)
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

// Run runs a one-shot helper container to completion and removes it. Its
// standard output is streamed to stdout; stdin and a terminal are not attached.
func (c Client) Run(ctx context.Context, opts CreateOptions, stdout io.Writer) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	args := append([]string{"container", "run", "--rm", "--pull", "missing", "--init"}, containerArgs(opts)...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("run helper container: %w: %s", err, detail)
		}
		return fmt.Errorf("run helper container: %w", err)
	}
	return nil
}

// containerArgs returns the flags shared by create and run, then the image
// and command.
func containerArgs(opts CreateOptions) []string {
	args := []string{"--label", "io.hewitt.managed=true",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges=true"}
	for _, capability := range opts.CapAdd {
		args = append(args, "--cap-add", capability)
	}
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
	if opts.ReadOnlyRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777")
	}
	for _, flag := range []struct{ name, value string }{
		{"--network", opts.Network}, {"--runtime", opts.Runtime},
		{"--memory", opts.Memory}, {"--cpus", opts.CPUs}, {"--pids-limit", opts.PIDs},
	} {
		if flag.value != "" {
			args = append(args, flag.name, flag.value)
		}
	}
	for _, file := range opts.EnvFiles {
		args = append(args, "--env-file", file)
	}
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}
	for _, mount := range opts.Mounts {
		spec := "type=bind,src=" + mount.Source + ",dst=" + mount.Target
		if mount.ReadOnly {
			spec += ",readonly"
		}
		args = append(args, "--mount", spec)
	}
	for _, volume := range opts.Volumes {
		args = append(args, "--mount", "type=volume,src="+volume.Name+",dst="+volume.Target)
	}
	args = append(args, "--", opts.Image)
	return append(args, opts.Command...)
}

// CreateVolume creates a labeled named volume. Creating an existing volume
// succeeds, so callers must choose unique names.
func (c Client) CreateVolume(ctx context.Context, name string) error {
	if err := validateArgument("volume name", name); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "volume", "create", "--label", "io.hewitt.managed=true", "--", name); err != nil {
		return fmt.Errorf("create volume %q: %w", name, err)
	}
	return nil
}

// VolumeExists reports whether Docker knows a volume.
func (c Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	if err := validateArgument("volume name", name); err != nil {
		return false, err
	}
	if _, err := c.execute(ctx, "volume", "inspect", "--", name); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("inspect volume %q: %w", name, err)
	}
	return true, nil
}

// RemoveVolume deletes a volume no container uses. A missing volume is not an error.
func (c Client) RemoveVolume(ctx context.Context, name string) error {
	if err := validateArgument("volume name", name); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "volume", "rm", "--", name); err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("remove volume %q: %w", name, err)
	}
	return nil
}

// CreateNetwork creates a labeled network and returns its ID. An internal
// network has no route outside Docker. The caller owns and removes it.
func (c Client) CreateNetwork(ctx context.Context, name string, internal bool) (string, error) {
	if err := validateArgument("network name", name); err != nil {
		return "", err
	}
	args := []string{"network", "create", "--label", "io.hewitt.managed=true"}
	if internal {
		args = append(args, "--internal")
	}
	out, err := c.execute(ctx, append(args, "--", name)...)
	if err != nil {
		return "", fmt.Errorf("create network %q: %w", name, err)
	}
	return strings.TrimSpace(out), nil
}

// ConnectNetwork attaches a container to a network under alias.
func (c Client) ConnectNetwork(ctx context.Context, network, id, alias string) error {
	for _, value := range []struct{ name, value string }{{"network", network}, {"container ID or name", id}, {"network alias", alias}} {
		if err := validateArgument(value.name, value.value); err != nil {
			return err
		}
	}
	if _, err := c.execute(ctx, "network", "connect", "--alias", alias, "--", network, id); err != nil {
		return fmt.Errorf("connect container %q to network %q (both preserved): %w", id, network, err)
	}
	return nil
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
	if err := runTerminal(ctx, in, out, stderr, "container", "start", "--attach", "--interactive", "--detach-keys=ctrl-p,ctrl-q", "--", id); err != nil {
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
		return fmt.Errorf("detached from container %q (state %s); authentication completion not confirmed; reconnect with hewitt attach %s", id, state.Status, id)
	}
	if state.ExitCode != 0 {
		return fmt.Errorf("container %q exited with status %d (container preserved)", id, state.ExitCode)
	}
	return nil
}

// Running reports whether a container is running. A container Docker no
// longer knows is reported as not running.
func (c Client) Running(ctx context.Context, id string) (bool, error) {
	if err := validateArgument("container ID or name", id); err != nil {
		return false, err
	}
	out, err := c.execute(ctx, "container", "inspect", "--format", "{{.State.Running}}", "--", id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("inspect container %q: %w", id, err)
	}
	return strings.TrimSpace(out) == "true", nil
}

// Stop stops a running container, preserving it. Stopping a stopped container
// succeeds; a missing container is reported.
func (c Client) Stop(ctx context.Context, id string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "container", "stop", "--", id); err != nil {
		return fmt.Errorf("stop container %q: %w", id, err)
	}
	return nil
}

// Remove deletes a stopped container. Docker refuses running containers; the
// removal is never forced. A container that no longer exists is not an error.
func (c Client) Remove(ctx context.Context, id string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "container", "rm", "--", id); err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("remove container %q: %w", id, err)
	}
	return nil
}

// RemoveNetwork deletes a network with no attached containers. A missing
// network is not an error.
func (c Client) RemoveNetwork(ctx context.Context, name string) error {
	if err := validateArgument("network name", name); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "network", "rm", "--", name); err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("remove network %q: %w", name, err)
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

// runTerminal shares cancellation cleanup between login and session attachment.
func runTerminal(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) (result error) {
	if err := ctx.Err(); err != nil {
		return err
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
	var diagnostic bytes.Buffer
	cmd.Stdin, cmd.Stdout = stdin, stdout
	cmd.Stderr = io.MultiWriter(stderr, &diagnostic)
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if errors.Is(err, exec.ErrNotFound) {
			err = fmt.Errorf("%w: %w", ErrCLIMissing, err)
		} else {
			// Some Docker CLI versions report their detach escape as exit 1.
			// Match only that diagnostic; other failures must remain errors.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(diagnostic.String()) == "read escape sequence" {
				return nil
			}
		}
		return err
	}
	return nil
}

func attach(ctx context.Context, id string, in io.Reader, out, stderr io.Writer, args []string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if err := runTerminal(ctx, in, out, stderr, args...); err != nil {
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
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%w: %w", ErrCLIMissing, err)
		}
		return "", newCommandError(args, strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}

// Pull fetches the current version of an image tag. Progress is not shown.
func (c Client) Pull(ctx context.Context, image string) error {
	if err := validateArgument("image", image); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "image", "pull", "--quiet", "--", image); err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}

// ImageID returns the local image ID for a reference, or "" when the image is
// not present locally. Pulled reports whether the local copy came from a
// registry; images built locally have no repository digests.
func (c Client) ImageID(ctx context.Context, image string) (id string, pulled bool, err error) {
	if err := validateArgument("image", image); err != nil {
		return "", false, err
	}
	out, err := c.execute(ctx, "image", "inspect", "--format", "{{.Id}} {{len .RepoDigests}}", "--", image)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect image %s: %w", image, err)
	}
	id, digests, _ := strings.Cut(strings.TrimSpace(out), " ")
	return id, digests != "0", nil
}

// ContainerImage returns the image ID a container was created from.
func (c Client) ContainerImage(ctx context.Context, id string) (string, error) {
	if err := validateArgument("container ID or name", id); err != nil {
		return "", err
	}
	out, err := c.execute(ctx, "container", "inspect", "--format", "{{.Image}}", "--", id)
	if err != nil {
		return "", fmt.Errorf("inspect container %q: %w", id, err)
	}
	return strings.TrimSpace(out), nil
}

func validateArgument(name, value string) error {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return fmt.Errorf("%s must be nonempty, contain no whitespace or control characters, and not start with '-'", name)
	}
	return nil
}
