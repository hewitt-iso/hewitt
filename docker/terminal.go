package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const terminalSocket = "/tmp/hewitt-tmux.sock"

// terminalKeys follows docker run -it: Ctrl-C stops the agent (tmux sends it
// SIGHUP and ends the session, so attached clients return to the host shell),
// and Ctrl-D detaches this client while the agent keeps running. Without
// remain-on-exit, an exiting agent also ends the session instead of leaving a
// dead pane.
var terminalKeys = []string{"bind-key", "-n", "C-c", "kill-session", ";", "bind-key", "-n", "C-d", "detach-client"}

// TerminalServerArgs runs the server in the foreground as the container process.
// No user configuration is loaded, and no client owns the server's lifetime.
func TerminalServerArgs() []string {
	return []string{"-D", "-f", "/dev/null", "-S", terminalSocket}
}

func terminalExec(id string, command ...string) []string {
	args := []string{"container", "exec", "--", id, "tmux", "-N", "-S", terminalSocket}
	return append(args, command...)
}

// StartExternalTerminal launches the agent once in a container Hewitt does not
// own. The tmux server uses a session-unique socket and runs beside the
// container's own processes; the container is never started, stopped, or removed.
func (c Client) StartExternalTerminal(ctx context.Context, id, socket, workdir string, command []string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if err := validateSocket(socket); err != nil {
		return err
	}
	if len(command) < 2 {
		return fmt.Errorf("terminal command requires an executable and arguments")
	}
	for _, arg := range command {
		if arg == ";" || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("terminal command contains a tmux separator or NUL")
		}
	}
	args := []string{"container", "exec"}
	if workdir != "" {
		if !strings.HasPrefix(workdir, "/") || strings.ContainsAny(workdir, "\x00\r\n") {
			return fmt.Errorf("container workdir must be an absolute path")
		}
		args = append(args, "--workdir", workdir)
	}
	// One tmux command list binds the session keys; the server daemonizes
	// inside the external container and exits with the agent.
	args = append(args, "--", id, "tmux", "-f", "/dev/null", "-S", socket, "new-session", "-d", "-s", "hewitt", "--")
	args = append(args, command...)
	args = append(append(args, ";"), terminalKeys...)
	if _, err := c.execute(ctx, args...); err != nil {
		return fmt.Errorf("launch agent in external container %q (container untouched; it must contain tmux): %w", id, err)
	}
	return nil
}

// StopExternalTerminal ends the tmux server, and with it the agent, that
// Hewitt started in an external container. The container itself is untouched.
func (c Client) StopExternalTerminal(ctx context.Context, id, socket string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if err := validateSocket(socket); err != nil {
		return err
	}
	if _, err := c.execute(ctx, "container", "exec", "--", id, "tmux", "-N", "-S", socket, "kill-server"); err != nil {
		return fmt.Errorf("stop agent terminal in external container %q: %w", id, err)
	}
	return nil
}

// ExternalTerminalRunning reports whether that tmux server still answers.
// A stopped or missing container counts as not running.
func (c Client) ExternalTerminalRunning(ctx context.Context, id, socket string) (bool, error) {
	running, err := c.Running(ctx, id)
	if err != nil || !running {
		return false, err
	}
	if err := validateSocket(socket); err != nil {
		return false, err
	}
	_, err = c.execute(ctx, "container", "exec", "--", id, "tmux", "-N", "-S", socket, "has-session", "-t", "=hewitt")
	return err == nil, nil
}

func validateSocket(socket string) error {
	if !strings.HasPrefix(socket, "/tmp/hewitt-") || strings.ContainsFunc(socket[len("/tmp/hewitt-"):], func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.')
	}) {
		return fmt.Errorf("invalid terminal socket %q", socket)
	}
	return nil
}

// StartTerminal launches the agent once, after the foreground server is ready.
// When the agent exits, the session and the foreground server end, stopping the
// container. Failure preserves the container; reconnecting never launches a
// second agent.
func (c Client) StartTerminal(ctx context.Context, id string, command []string) error {
	if err := validateArgument("container ID or name", id); err != nil {
		return err
	}
	if len(command) < 2 {
		return fmt.Errorf("terminal command requires an executable and arguments")
	}
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		_, err := c.execute(ready, terminalExec(id, "show-options", "-s")...)
		if err == nil {
			break
		}
		select {
		case <-ready.Done():
			return fmt.Errorf("wait for terminal in container %q (container preserved; image must contain tmux): %w", id, errors.Join(ready.Err(), err))
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := c.execute(ctx, terminalExec(id, terminalKeys...)...); err != nil {
		return fmt.Errorf("configure terminal in container %q (container preserved): %w", id, err)
	}
	args := terminalExec(id, "new-session", "-d", "-s", "hewitt", "--")
	args = append(args, command...)
	if _, err := c.execute(ctx, args...); err != nil {
		return fmt.Errorf("launch agent in container %q (container preserved): %w", id, err)
	}
	return nil
}

// AttachTerminal reconnects a client to the existing tmux session. Ctrl-D
// detaches only this client, leaving both server and agent running.
func (c Client) AttachTerminal(ctx context.Context, id string, in io.Reader, out, stderr io.Writer) error {
	return c.AttachTerminalSocket(ctx, id, terminalSocket, in, out, stderr)
}

// AttachTerminalSocket reconnects to a terminal started with a specific socket,
// such as one in an external container.
func (Client) AttachTerminalSocket(ctx context.Context, id, socket string, in io.Reader, out, stderr io.Writer) error {
	if err := validateSocket(socket); err != nil {
		return err
	}
	args := []string{"container", "exec", "--interactive", "--tty", "--env", "TERM=xterm-256color", "--", id,
		"tmux", "-N", "-S", socket, "attach-session", "-t", "=hewitt"}
	return attach(ctx, id, in, out, stderr, args)
}
