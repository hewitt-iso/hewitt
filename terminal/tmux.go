// Package terminal navigates existing host terminals without owning agent processes.
package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Location identifies a pane in a host tmux server or a Herdr session. Empty
// means a plain terminal.
type Location struct {
	Socket string `json:"socket,omitempty"`
	Pane   string `json:"pane,omitempty"`
	// HerdrSocket and HerdrPane locate a pane of a Herdr session.
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPane   string `json:"herdr_pane,omitempty"`
}

// Kept reports whether a terminal manager holds the pane, so it outlives the
// terminal window and can be returned to.
func (l Location) Kept() bool { return l.Socket != "" || l.HerdrPane != "" }

// Manager names what holds the pane, for listings.
func (l Location) Manager() string {
	switch {
	case l.HerdrPane != "":
		return "herdr"
	case l.Socket != "":
		return "host tmux"
	}
	return "plain terminal"
}

func (l Location) Validate() error {
	if l.HerdrSocket != "" || l.HerdrPane != "" {
		if l.Socket != "" || l.Pane != "" {
			return fmt.Errorf("invalid terminal location: both tmux and Herdr")
		}
		return validHerdr(l.HerdrSocket, l.HerdrPane)
	}
	if l.Socket == "" && l.Pane == "" {
		return nil
	}
	if !strings.HasPrefix(l.Socket, "/") || strings.ContainsAny(l.Socket, "\x00\r\n") || !strings.HasPrefix(l.Pane, "%") {
		return fmt.Errorf("invalid host tmux location")
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(l.Pane, "%"), 10, 64); err != nil {
		return fmt.Errorf("invalid host tmux pane: %w", err)
	}
	return nil
}

// Current locates the pane this process runs in. Herdr removes tmux markers
// from its panes, so TMUX set inside a Herdr pane means a tmux started there,
// which is the closer pane.
func Current() (Location, error) {
	value := os.Getenv("TMUX")
	if value == "" {
		l, _ := currentHerdr()
		return l, l.Validate()
	}
	// TMUX ends with ,server-pid,session-index; socket paths can contain commas.
	for range 2 {
		i := strings.LastIndex(value, ",")
		if i < 0 {
			return Location{}, fmt.Errorf("invalid host TMUX environment")
		}
		value = value[:i]
	}
	l := Location{Socket: value, Pane: os.Getenv("TMUX_PANE")}
	return l, l.Validate()
}

func query(ctx context.Context, l Location, args ...string) (string, error) {
	args = append([]string{"-S", l.Socket}, args...)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var out, diagnostic bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("host tmux: %w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Focus returns to an existing pane. It never creates a pane or starts an agent.
func Focus(ctx context.Context, l Location, in io.Reader, out, stderr io.Writer) (result error) {
	if err := l.Validate(); err != nil {
		return err
	}
	if l.HerdrPane != "" {
		return focusHerdr(ctx, l)
	}
	if l.Socket == "" {
		return &NotReattachableError{}
	}
	current, err := Current()
	if err != nil {
		return err
	}
	if current.Socket != "" && current.Socket != l.Socket {
		return &OtherServerError{Socket: l.Socket}
	}
	target, err := query(ctx, l, "display-message", "-p", "-t", l.Pane, "#{session_id}\t#{window_id}")
	if err != nil {
		return fmt.Errorf("find agent pane %s: %w", l.Pane, err)
	}
	ids := strings.Split(target, "\t")
	if len(ids) != 2 || !strings.HasPrefix(ids[0], "$") || !strings.HasPrefix(ids[1], "@") {
		return fmt.Errorf("host tmux returned invalid pane target")
	}
	for _, args := range [][]string{{"select-pane", "-t", l.Pane}, {"select-window", "-t", ids[1]}} {
		if _, err := query(ctx, l, args...); err != nil {
			return err
		}
	}
	if current.Socket != "" {
		_, err := query(ctx, l, "switch-client", "-t", ids[0])
		return err
	}
	// Outside tmux, connect to the existing host server using the caller's terminal.
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		state, err := term.GetState(int(f.Fd()))
		if err != nil {
			return fmt.Errorf("read host terminal state: %w", err)
		}
		defer func() { result = errors.Join(result, term.Restore(int(f.Fd()), state)) }()
	}
	cmd := exec.CommandContext(ctx, "tmux", "-S", l.Socket, "attach-session", "-t", ids[0])
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach host tmux: %w", err)
	}
	return nil
}
