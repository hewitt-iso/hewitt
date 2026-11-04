package terminal

// NotReattachableError is an agent started outside host tmux; nothing holds its
// terminal, so it cannot be returned to.
type NotReattachableError struct{}

func (*NotReattachableError) Error() string {
	return "this agent was started outside host tmux, so its terminal cannot be reattached"
}

func (*NotReattachableError) Hint() string {
	return "return to the terminal window that started it, and start future agents inside tmux"
}

// OtherServerError is an agent pane on a different host tmux server.
type OtherServerError struct{ Socket string }

func (e *OtherServerError) Error() string {
	return "agent belongs to another host tmux server (" + e.Socket + ")"
}

func (*OtherServerError) Hint() string {
	return "run hewitt attach outside tmux, or from a client of that tmux server"
}
