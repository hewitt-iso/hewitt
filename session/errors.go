package session

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// NotFoundError means no saved session matches a name or ID. It matches
// os.ErrNotExist.
type NotFoundError struct{ Ref string }

func (e *NotFoundError) Error() string      { return fmt.Sprintf("no session named or with ID %q", e.Ref) }
func (*NotFoundError) Hint() string         { return "hewitt ps -a lists saved sessions" }
func (*NotFoundError) Is(target error) bool { return target == os.ErrNotExist }

// errInterrupted marks an agent the user left with Ctrl-C; that is a normal exit.
var errInterrupted = errors.New("agent interrupted")

// AgentExitError is a foreground agent process that ended with a nonzero
// status. The workspace container keeps running.
type AgentExitError struct {
	Agent, Workspace string
	Code             int
	Err              error
}

func (e *AgentExitError) Error() string {
	switch e.Code {
	case 126, 127:
		return fmt.Sprintf("could not start %s in workspace %s (exit status %d)", e.Agent, e.Workspace, e.Code)
	case 137:
		return fmt.Sprintf("%s in workspace %s was killed (exit status 137)", e.Agent, e.Workspace)
	}
	return fmt.Sprintf("%s exited with status %d in workspace %s", e.Agent, e.Code, e.Workspace)
}

func (e *AgentExitError) Hint() string {
	switch e.Code {
	case 126, 127:
		return "the workspace image may be outdated or broken; hewitt rm " + e.Workspace + " and run again to recreate its container"
	case 137:
		return "the container ran out of memory or was stopped; check hewitt ps and docker ps -a"
	}
	return "the workspace keeps running; hewitt run opens the agent again"
}

func (e *AgentExitError) Unwrap() []error {
	if e.Code == 130 {
		return []error{errInterrupted, e.Err}
	}
	return []error{e.Err}
}

// DirtyVolumeError refuses to discard uncommitted work in a session volume.
type DirtyVolumeError struct {
	Session, Volume string
	Paths           []string
}

func (e *DirtyVolumeError) Error() string {
	return fmt.Sprintf("volume %q has uncommitted or untracked files in %s", e.Volume, strings.Join(e.Paths, ", "))
}

func (e *DirtyVolumeError) Hint() string {
	return "commit them inside the session and rerun, or hewitt rm --force " + e.Session + " to discard them"
}

// RunningError refuses to remove a session whose container still runs.
type RunningError struct{ Session, Container string }

func (e *RunningError) Error() string {
	return fmt.Sprintf("session %s is still running in container %s", e.Session, e.Container)
}

func (e *RunningError) Hint() string { return "stop it first with hewitt stop " + e.Session }

// DangerError is a setting the chosen isolation level forbids unless allowed.
type DangerError struct{ Isolation, What, Item string }

func (e *DangerError) Error() string {
	return fmt.Sprintf("%s isolation forbids %s", e.Isolation, e.What)
}

func (e *DangerError) Hint() string {
	return fmt.Sprintf("allow it explicitly with danger: [%s] in hewitt.yml or --danger %s, or choose a lower --isolation", e.Item, e.Item)
}

// SSHAgentError means the host ssh-agent cannot be forwarded.
type SSHAgentError struct {
	Reason string
	Err    error
}

func (e *SSHAgentError) Error() string {
	if e.Err != nil {
		return "ssh: " + e.Reason + ": " + e.Err.Error()
	}
	return "ssh: " + e.Reason
}

func (*SSHAgentError) Hint() string {
	return "start ssh-agent (eval \"$(ssh-agent)\"), add a key with ssh-add, and run hewitt from that shell"
}

func (e *SSHAgentError) Unwrap() error { return e.Err }
