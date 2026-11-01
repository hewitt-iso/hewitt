package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const terminalSocket = "/tmp/hewitt-tmux.sock"

// TerminalServerArgs runs the server in the foreground as the container process.
// No user configuration is loaded, and no client owns the server's lifetime.
func TerminalServerArgs() []string {
	return []string{"-D", "-f", "/dev/null", "-S", terminalSocket}
}

func terminalExec(id string, command ...string) []string {
	args := []string{"container", "exec", "--", id, "tmux", "-N", "-S", terminalSocket}
	return append(args, command...)
}

// StartTerminal launches the agent once, after the foreground server is ready.
// The final screen remains available when the agent exits. Failure preserves the
// container; reconnecting never launches a second agent.
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
	if _, err := c.execute(ctx, terminalExec(id, "set-option", "-g", "remain-on-exit", "on")...); err != nil {
		return fmt.Errorf("configure terminal in container %q (container preserved): %w", id, err)
	}
	args := terminalExec(id, "new-session", "-d", "-s", "hewitt", "--")
	args = append(args, command...)
	if _, err := c.execute(ctx, args...); err != nil {
		return fmt.Errorf("launch agent in container %q (container preserved): %w", id, err)
	}
	return nil
}

// AttachTerminal reconnects a client to the existing tmux session. Ctrl-B, D
// detaches only this client, leaving both server and agent running.
func (Client) AttachTerminal(ctx context.Context, id string, in io.Reader, out, stderr io.Writer) error {
	args := []string{"container", "exec", "--interactive", "--tty", "--env", "TERM=xterm-256color", "--", id,
		"tmux", "-N", "-S", terminalSocket, "attach-session", "-t", "=hewitt"}
	return attach(ctx, id, in, out, stderr, args)
}
