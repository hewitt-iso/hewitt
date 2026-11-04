package docker

import (
	"context"
	"fmt"
	"golang.org/x/term"
	"io"
	"os"
	"strings"
)

// ExecInteractive starts a fresh process in a running workspace. It does not
// start a terminal server or change the container's lifecycle.
func (Client) ExecInteractive(ctx context.Context, id string, command []string, in io.Reader, out, stderr io.Writer) error {
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
	args := []string{"container", "exec", "--interactive", "--env", "TERM=" + terminal}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		args = append(args, "--tty")
	}
	args = append(args, "--", id)
	args = append(args, command...)
	if err := runTerminal(ctx, in, out, stderr, args...); err != nil {
		return fmt.Errorf("run in workspace %q: %w", id, err)
	}
	return nil
}
