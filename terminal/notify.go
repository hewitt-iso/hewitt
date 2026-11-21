package terminal

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ErrNoClient means no tmux client shows the pane's session, so a popup
// would not be seen.
var ErrNoClient = errors.New("no tmux client shows this pane")

// Popup runs command in a popup over pane l, on a client that shows the
// pane's session, so the user answers where the agent waits. tmux runs the
// command through sh, so each argument is quoted. A tmux without popups
// (before 3.2) shows message in the pane's status line instead.
func Popup(ctx context.Context, l Location, title string, command []string, message string) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if l.Socket == "" {
		return ErrNoClient
	}
	session, err := query(ctx, l, "display-message", "-p", "-t", l.Pane, "#{session_id}")
	if err != nil {
		return fmt.Errorf("find agent pane %s: %w", l.Pane, err)
	}
	clients, err := query(ctx, l, "list-clients", "-t", session, "-F", "#{client_name}\t#{client_width}\t#{client_height}")
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(clients, "\n")
	fields := strings.Split(first, "\t")
	if len(fields) != 3 || fields[0] == "" {
		return ErrNoClient
	}
	client := fields[0]
	// tmux refuses a popup larger than the client.
	size := func(value string, want int) string {
		n, err := strconv.Atoi(value)
		if err != nil || n-2 >= want {
			return strconv.Itoa(want)
		}
		return strconv.Itoa(max(n-2, 10))
	}
	width, height := size(fields[1], 88), size(fields[2], 14)
	quoted := make([]string, len(command))
	for i, arg := range command {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	_, err = query(ctx, l, "display-popup", "-c", client, "-t", l.Pane, "-E", "-w", width, "-h", height, "-T", title, strings.Join(quoted, " "))
	if err == nil {
		return nil
	}
	if _, merr := query(ctx, l, "display-message", "-c", client, "-d", "15000", message); merr != nil {
		return errors.Join(err, merr)
	}
	return nil
}

// Notify shows a desktop notification when the system offers a way to; it
// reports whether one was shown. Arguments are passed directly.
func Notify(ctx context.Context, title, body string) bool {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		if _, err := exec.LookPath("osascript"); err != nil {
			return false
		}
		// AppleScript receives the text as arguments, never as source.
		cmd = exec.CommandContext(ctx, "osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, body)
	default:
		if _, err := exec.LookPath("notify-send"); err != nil {
			return false
		}
		cmd = exec.CommandContext(ctx, "notify-send", "--app-name=Hewitt", "--urgency=critical", "--", title, body)
	}
	return cmd.Run() == nil
}
