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
	m, err := createAgentContainer(ctx, store, opts, Metadata{Agent: agent})
	if err != nil {
		return m, err
	}
	if err := (docker.Client{}).Start(ctx, m.ID); err != nil {
		return m, err
	}
	return m, nil
}

// createAgentContainer creates a container and saves m, completed with its ID,
// image, name, and creation time, before any startup.
func createAgentContainer(ctx context.Context, store *Store, opts docker.CreateOptions, m Metadata) (Metadata, error) {
	if err := validateAgent(m.Agent); err != nil {
		return Metadata{}, err
	}
	id, err := (docker.Client{}).Create(ctx, opts)
	if err != nil {
		return Metadata{}, err
	}
	m.ID, m.Image, m.Name, m.CreatedAt = id, opts.Image, opts.Name, time.Now().UTC()
	if err := store.Save(m); err != nil {
		return m, fmt.Errorf("record container %q (container preserved, startup not attempted): %w", id, err)
	}
	return m, nil
}
