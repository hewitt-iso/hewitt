package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hewitt-iso/hewitt/terminal"
)

// AgentRun is a live foreground connection, not an owner of workspace resources.
// The recording CLI holds an exclusive file lock for the connection's lifetime.
type AgentRun struct {
	Version       int               `json:"version"`
	ID            string            `json:"id"`
	WorkspaceID   string            `json:"workspace_id"`
	WorkspaceName string            `json:"workspace_name"`
	Agent         string            `json:"agent"`
	Terminal      terminal.Location `json:"terminal"`
	CreatedAt     time.Time         `json:"created_at"`
}

// RecordRun registers a connection until release is called or the caller dies.
// A kernel lock prevents records from looking live after a crash or reboot.
func (s *Store) RecordRun(m Metadata, agent string) (AgentRun, func(), error) {
	if err := validateID(m.ID); err != nil {
		return AgentRun{}, nil, err
	}
	if agent != "shell" && agent != "workspace" {
		if err := validateAgent(agent); err != nil {
			return AgentRun{}, nil, err
		}
	}
	location, err := terminal.Current()
	if err != nil {
		return AgentRun{}, nil, err
	}
	hash := sha256.Sum256([]byte(rand.Text()))
	run := AgentRun{Version: 1, ID: fmt.Sprintf("%x", hash), WorkspaceID: m.ID, WorkspaceName: m.Name, Agent: agent, Terminal: location, CreatedAt: time.Now().UTC()}
	dir := filepath.Join(s.dataDir, "runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return AgentRun{}, nil, fmt.Errorf("create run directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return AgentRun{}, nil, err
	}
	tmp := ".tmp-" + run.ID
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		return AgentRun{}, nil, err
	}
	fail := func(err error) (AgentRun, func(), error) {
		f.Close()
		root.Remove(tmp)
		root.Close()
		return AgentRun{}, nil, fmt.Errorf("record agent run: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(f).Encode(run); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	name := run.ID + ".json"
	if err := root.Rename(tmp, name); err != nil {
		return fail(err)
	}
	return run, func() { root.Remove(name); f.Close(); root.Close() }, nil
}

// RunningAgents returns only records whose launching CLI still holds its lock.
// Reading never creates directories or changes workspace/session metadata.
func (s *Store) RunningAgents() ([]AgentRun, error) {
	root, err := os.OpenRoot(filepath.Join(s.dataDir, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open agent runs: %w", err)
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	var runs []AgentRun
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !strings.HasSuffix(entry.Name(), ".json") || validateID(id) != nil {
			continue
		}
		f, err := root.Open(entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		locked := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if locked == nil {
			f.Close()
			continue
		}
		if locked != syscall.EWOULDBLOCK && locked != syscall.EAGAIN {
			f.Close()
			return nil, fmt.Errorf("inspect run liveness: %w", locked)
		}
		var run AgentRun
		decoder := json.NewDecoder(f)
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&run)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("read agent run %s: %w", id, err)
		}
		if run.Version != 1 || run.CreatedAt.IsZero() || run.ID != id || validateID(run.WorkspaceID) != nil || run.Terminal.Validate() != nil || (run.Agent != "shell" && validateAgent(run.Agent) != nil) {
			return nil, fmt.Errorf("invalid agent run %s", id)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) resolveRun(ref string) (AgentRun, error) {
	runs, err := s.RunningAgents()
	if err != nil {
		return AgentRun{}, err
	}
	var found []AgentRun
	for _, run := range runs {
		if ref != "" && strings.HasPrefix(run.ID, ref) {
			found = append(found, run)
		}
	}
	if len(found) > 1 {
		return AgentRun{}, fmt.Errorf("agent run %q is ambiguous; use a longer ID", ref)
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return AgentRun{}, fmt.Errorf("agent run %q: %w", ref, os.ErrNotExist)
}

func attachRun(ctx context.Context, run AgentRun, in io.Reader, out, stderr io.Writer) error {
	return terminal.Focus(ctx, run.Terminal, in, out, stderr)
}
