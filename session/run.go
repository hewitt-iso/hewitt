package session

import (
	"context"
	"fmt"
	"time"

	"github.com/hewitt-iso/hewitt/docker"
)

// Run creates a container, saves its metadata, and requests startup. Once created,
// the container belongs to the caller even if saving or starting fails. A metadata
// failure prevents startup and reports the preserved container ID for recovery.
func Run(ctx context.Context, store *Store, agent string, opts docker.CreateOptions) (Metadata, error) {
	if err := validateAgent(agent); err != nil {
		return Metadata{}, err
	}
	client := docker.Client{}
	id, err := client.Create(ctx, opts)
	if err != nil {
		return Metadata{}, err
	}
	m := Metadata{ID: id, Agent: agent, Image: opts.Image, Name: opts.Name, CreatedAt: time.Now().UTC()}
	if err := store.Save(m); err != nil {
		return m, fmt.Errorf("record container %q (container preserved, startup not attempted): %w", id, err)
	}
	if err := client.Start(ctx, id); err != nil {
		return m, err
	}
	return m, nil
}
