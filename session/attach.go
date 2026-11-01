package session

import (
	"context"
	"io"

	"github.com/hewitt-iso/hewitt/docker"
)

// Attach resolves saved metadata before connecting to its existing terminal. It does not
// change metadata or start, stop, or remove the container.
func Attach(ctx context.Context, store *Store, id string, stdin io.Reader, stdout, stderr io.Writer) error {
	m, err := store.Get(id)
	if err != nil {
		return err
	}
	if m.Terminal == "tmux" {
		return (docker.Client{}).AttachTerminal(ctx, m.ID, stdin, stdout, stderr)
	}
	return (docker.Client{}).Attach(ctx, m.ID, stdin, stdout, stderr)
}
