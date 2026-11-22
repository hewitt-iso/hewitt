package docker

import (
	"context"
	"fmt"
	"golang.org/x/term"
	"io"
	"os"
	"strings"
)

// ExecInteractive starts a fresh process in a running workspace with extra
// environment entries and env files. It does not start a terminal server or
// change the container's lifecycle.
func (c Client) ExecInteractive(ctx context.Context, id string, env, envFiles, command []string, in io.Reader, out, stderr io.Writer) error {
	if err := validateArgument("container ID", id); err != nil {
		return err
	}
	if len(command) == 0 {
		return fmt.Errorf("exec command must not be empty")
	}
	for _, arg := range command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("exec command contains NUL")
		}
	}
	terminal := os.Getenv("TERM")
	if terminal == "" || terminal == "dumb" {
		terminal = "xterm-256color"
	}
	if err := validateEnv(env, envFiles); err != nil {
		return err
	}
	args := []string{"container", "exec", "--interactive", "--env", "TERM=" + terminal}
	for _, file := range envFiles {
		args = append(args, "--env-file", file)
	}
	for _, entry := range env {
		args = append(args, "--env", entry)
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		args = append(args, "--tty")
	}
	args = append(args, "--", id)
	args = append(args, command...)
	for _, entry := range c.Env {
		if name, _, ok := strings.Cut(entry, "="); !ok || name == "" || strings.ContainsRune(entry, 0) {
			return fmt.Errorf("invalid Docker CLI environment entry")
		}
	}
	if err := runTerminalEnv(ctx, c.Env, in, out, stderr, args...); err != nil {
		return fmt.Errorf("run in workspace %q: %w", id, err)
	}
	return nil
}

// Exec runs command in a running container as its configured user and returns
// its standard output. Arguments are passed directly, never through a shell.
func (c Client) Exec(ctx context.Context, id string, command []string) (string, error) {
	if err := validateArgument("container ID", id); err != nil {
		return "", err
	}
	if len(command) == 0 {
		return "", fmt.Errorf("exec command must not be empty")
	}
	for _, arg := range command {
		if strings.ContainsRune(arg, 0) {
			return "", fmt.Errorf("exec command contains NUL")
		}
	}
	return c.execute(ctx, append([]string{"container", "exec", "--", id}, command...)...)
}
