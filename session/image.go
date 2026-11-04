package session

import (
	"context"
	"fmt"
	"io"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
)

// refreshImage keeps the default registry image current; Docker's create
// --pull missing never replaces a stale local tag. Locally built images (no
// registry digest) and custom images are left alone, and a failed check falls
// back to the local copy so offline runs still work.
func refreshImage(ctx context.Context, image string, stderr io.Writer) error {
	if image != agent.DefaultImage {
		return nil
	}
	client := docker.Client{}
	// Without a usable local copy, container creation pulls and reports errors.
	id, pulled, err := client.ImageID(ctx, image)
	if err != nil || id == "" || !pulled {
		return nil
	}
	if err := reportStep(stderr, "Checking for image updates ("+image+")"); err != nil {
		return err
	}
	if err := client.Pull(ctx, image); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return warn(stderr, "could not check for image updates, using the local copy: %v", err)
	}
	return nil
}

// warn reports a problem that does not stop the current operation.
func warn(w io.Writer, format string, args ...any) error {
	if _, err := fmt.Fprintf(w, "[hewitt] Warning: "+format+"\n", args...); err != nil {
		return fmt.Errorf("report warning: %w", err)
	}
	return nil
}
