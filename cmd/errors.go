package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// hinter is implemented by errors that know what the user can do next. Packages
// return facts as typed errors; the hint is printed once, here, at the CLI boundary.
type hinter interface{ Hint() string }

// PrintError writes err and, when any error in its chain offers one, a hint.
func PrintError(w io.Writer, err error) {
	fmt.Fprintln(w, "Error:", err)
	var h hinter
	if errors.As(err, &h) && h.Hint() != "" {
		fmt.Fprintln(w, "Hint:", h.Hint())
	}
}

// looksLikeID reports whether an argument could be a session or agent run ID.
func looksLikeID(arg string) bool {
	return len(arg) >= 4 && strings.Trim(arg, "0123456789abcdef") == ""
}

// unknownCommandError is a mistyped top-level command.
type unknownCommandError struct {
	Name, Path  string
	Suggestions []string
}

func (e *unknownCommandError) Error() string {
	return fmt.Sprintf("unknown command %q for %q", e.Name, e.Path)
}

func (e *unknownCommandError) Hint() string {
	switch {
	case len(e.Suggestions) > 0:
		return "did you mean " + strings.Join(e.Suggestions, " or ") + "?"
	case looksLikeID(e.Name):
		return "to return to a session, use hewitt attach " + e.Name
	}
	return "hewitt help lists commands"
}

// unknownAgentError names an agent Hewitt has no adapter for.
type unknownAgentError struct {
	Name      string
	Supported []string
}

func (e *unknownAgentError) Error() string {
	return fmt.Sprintf("unknown agent %q (supported: %s)", e.Name, strings.Join(e.Supported, ", "))
}

func (e *unknownAgentError) Hint() string {
	if looksLikeID(e.Name) {
		return "to return to a running session, use hewitt attach " + e.Name
	}
	return "hewitt run without an agent shows a picker"
}

// missingSessionError is a session command run without a terminal for the picker.
type missingSessionError struct{}

func (*missingSessionError) Error() string { return "no session specified" }
func (*missingSessionError) Hint() string {
	return "pass a session name or ID (hewitt ps lists them); the picker needs an interactive terminal"
}

// noAgentsError means nothing can be reattached. Idle lists running shared
// workspaces without a connected agent; Workspace is set when one was named.
type noAgentsError struct {
	Workspace string
	Idle      []string
}

func (e *noAgentsError) Error() string {
	switch {
	case e.Workspace != "":
		return fmt.Sprintf("workspace %s has no connected agents", e.Workspace)
	case len(e.Idle) > 0:
		return fmt.Sprintf("no connected agents; workspace %s is running without one", strings.Join(e.Idle, ", "))
	}
	return "no running agents or legacy sessions"
}

func (e *noAgentsError) Hint() string {
	if e.Workspace == "" && len(e.Idle) == 0 {
		return "start one with hewitt run"
	}
	return "an agent runs in the terminal that started it, so one started outside host tmux ended with that terminal; " +
		"open a new one with hewitt run in the project directory, inside tmux to keep it reattachable"
}

// ambiguousAgentError lists the agents to choose from when no picker can be shown.
type ambiguousAgentError struct{ Choices []string }

func (e *ambiguousAgentError) Error() string {
	return "several agents are running:\n  " + strings.Join(e.Choices, "\n  ")
}

func (*ambiguousAgentError) Hint() string { return "pass one of these IDs to hewitt attach" }
