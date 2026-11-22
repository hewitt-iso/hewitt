package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Herdr (herdr.dev) is a terminal workspace manager for coding agents. It
// recognizes an agent by its foreground process, which for Hewitt is the
// Docker CLI, so Hewitt names the agent with the HERDR_AGENT hint that Herdr
// documents for sandbox wrappers. Herdr then reads the agent's state from the
// screen as usual.

var herdrPane = regexp.MustCompile(`^w[0-9]+:p[0-9]+$`)

func validHerdr(socket, pane string) error {
	if !strings.HasPrefix(socket, "/") || strings.ContainsAny(socket, "\x00\r\n") || !herdrPane.MatchString(pane) {
		return fmt.Errorf("invalid Herdr pane location")
	}
	return nil
}

// currentHerdr locates the Herdr pane this process runs in, from the
// variables Herdr sets in every pane.
func currentHerdr() (Location, bool) {
	if os.Getenv("HERDR_ENV") != "1" {
		return Location{}, false
	}
	l := Location{HerdrSocket: os.Getenv("HERDR_SOCKET_PATH"), HerdrPane: os.Getenv("HERDR_PANE_ID")}
	if validHerdr(l.HerdrSocket, l.HerdrPane) != nil {
		return Location{}, false
	}
	return l, true
}

// AgentHint is the environment that lets the terminal manager of pane l
// recognize an agent of Herdr's kind behind the Docker CLI; it is empty
// outside Herdr and for agents Herdr does not know.
func AgentHint(l Location, kind string) []string {
	if l.HerdrPane == "" || kind == "" {
		return nil
	}
	return []string{"HERDR_AGENT=" + kind}
}

// herdr runs the Herdr CLI against the session of l and returns its output.
func herdr(ctx context.Context, l Location, args ...string) ([]byte, error) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if !strings.HasPrefix(bin, "/") {
		bin = "herdr"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "HERDR_SOCKET_PATH="+l.HerdrSocket)
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("herdr %s: %w: %s", args[0], err, strings.TrimSpace(diagnostic.String()))
	}
	return out.Bytes(), nil
}

// herdrPrompt runs command in a pane split below the agent's Herdr pane and
// focused, so the user answers next to the agent; the pane closes when the
// command exits. A Herdr notification with its request sound draws attention
// to it from other workspaces.
func herdrPrompt(ctx context.Context, l Location, title string, command []string, message string) error {
	raw, err := herdr(ctx, l, "pane", "split", l.HerdrPane, "--direction", "down", "--ratio", "0.6", "--focus")
	if err != nil {
		return err
	}
	var split struct {
		Result struct {
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &split); err != nil || !herdrPane.MatchString(split.Result.Pane.ID) {
		return fmt.Errorf("herdr pane split returned no pane")
	}
	if _, err := herdr(ctx, l, "pane", "run", split.Result.Pane.ID, "exec "+shellQuote(command)); err != nil {
		_, _ = herdr(context.WithoutCancel(ctx), l, "pane", "close", split.Result.Pane.ID)
		return err
	}
	_, _ = herdr(ctx, l, "notification", "show", strings.TrimSpace(title), "--body", message, "--sound", "request")
	return nil
}

// focusHerdr returns to an agent's Herdr pane in the user's Herdr client.
func focusHerdr(ctx context.Context, l Location) error {
	current, _ := currentHerdr()
	if current.HerdrSocket != l.HerdrSocket {
		return &OtherHerdrError{Socket: l.HerdrSocket}
	}
	_, err := herdr(ctx, l, "agent", "focus", l.HerdrPane)
	return err
}

// shellQuote joins arguments for sh, quoting each one.
func shellQuote(command []string) string {
	quoted := make([]string, len(command))
	for i, arg := range command {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
