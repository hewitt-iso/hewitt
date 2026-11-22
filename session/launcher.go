package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/images"
)

// agentDir is where containers find the hewitt-agent of the Hewitt that
// starts agents in them.
const agentDir = "/hewitt/agent"

// agentScript writes the hewitt-agent compiled into this binary to
// <dataDir>/agent, named after its content, and returns the read-only mount
// of that directory and the script's path in containers. Every version keeps
// its own file, so a workspace container created by an older Hewitt runs the
// script of the Hewitt that opens an agent in it.
func agentScript(dataDir string) (docker.BindMount, string, error) {
	sum := sha256.Sum256(images.Agent)
	name := "hewitt-agent-" + hex.EncodeToString(sum[:8])
	dir := filepath.Join(dataDir, "agent")
	mount := docker.BindMount{Source: dir, Target: agentDir, ReadOnly: true}
	path := agentDir + "/" + name
	if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil && string(data) == string(images.Agent) {
		return mount, path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return docker.BindMount{}, "", fmt.Errorf("create hewitt-agent directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return docker.BindMount{}, "", fmt.Errorf("write hewitt-agent: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(images.Agent)
	if err == nil {
		err = tmp.Chmod(0o500)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		return docker.BindMount{}, "", fmt.Errorf("write hewitt-agent: %w", err)
	}
	return mount, path, nil
}
