// Package session owns local session metadata and coordinates container startup.
package session

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Metadata records creation intent, not the container's current runtime state.
// IDs are full Docker container IDs until sessions have independent resources.
// Credentials and agent input must never be stored here.
type Metadata struct {
	ID        string    `json:"id"`
	Agent     string    `json:"agent"`
	Image     string    `json:"image"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type record struct {
	Version int `json:"version"`
	Metadata
}

// Store keeps one versioned JSON file per session beneath the configured data
// directory. Writes are atomic; concurrent saves of the same ID are last-writer
// wins. Deleting metadata never deletes containers or worktrees.
type Store struct {
	dataDir string
}

// NewStore validates the location without creating any files or directories.
func NewStore(dataDir string) (*Store, error) {
	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("session data directory must be an absolute path")
	}
	return &Store{dataDir: dataDir}, nil
}

// Save creates or replaces a record using a private temporary file and an atomic
// rename. Readers never see a partially written record.
func (s *Store) Save(m Metadata) error {
	if err := m.validate(); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	root, err := s.open(true)
	if err != nil {
		return fmt.Errorf("save session %q: %w", m.ID, err)
	}
	defer root.Close()
	data, err := json.MarshalIndent(record{Version: 1, Metadata: m}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session %q: %w", m.ID, err)
	}
	tmp := ".tmp-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary session file: %w", err)
	}
	defer root.Remove(tmp)
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("write session %q: %w", m.ID, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync session %q: %w", m.ID, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close session %q: %w", m.ID, err)
	}
	if err := root.Rename(tmp, m.ID+".json"); err != nil {
		return fmt.Errorf("replace session %q: %w", m.ID, err)
	}
	return nil
}

// Get reads a full session ID. Missing records wrap os.ErrNotExist.
func (s *Store) Get(id string) (Metadata, error) {
	if err := validateID(id); err != nil {
		return Metadata{}, err
	}
	root, err := s.open(false)
	if err != nil {
		return Metadata{}, fmt.Errorf("get session %q: %w", id, err)
	}
	defer root.Close()
	return read(root, id)
}

// List returns records in ID order. A missing store is empty. Invalid records
// fail the whole operation rather than silently hiding sessions.
func (s *Store) List() ([]Metadata, error) {
	root, err := s.open(false)
	if errors.Is(err, os.ErrNotExist) {
		return []Metadata{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	result := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		m, err := read(root, id)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		result = append(result, m)
	}
	return result, nil
}

// Delete removes only the metadata file. Missing records wrap os.ErrNotExist.
func (s *Store) Delete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	root, err := s.open(false)
	if err != nil {
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	defer root.Close()
	if err := checkRecordFile(root, id); err != nil {
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	if err := root.Remove(id + ".json"); err != nil {
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	return nil
}

func (s *Store) open(create bool) (*os.Root, error) {
	if !filepath.IsAbs(s.dataDir) {
		return nil, fmt.Errorf("session data directory must be an absolute path")
	}
	if create {
		if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("create session data directory: %w", err)
		}
	}
	data, err := os.OpenRoot(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("open session data directory: %w", err)
	}
	defer data.Close()
	if create {
		if err := data.Mkdir("sessions", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create sessions directory: %w", err)
		}
	}
	root, err := data.OpenRoot("sessions")
	if err != nil {
		return nil, fmt.Errorf("open sessions directory: %w", err)
	}
	return root, nil
}

func read(root *os.Root, id string) (Metadata, error) {
	if err := validateID(id); err != nil {
		return Metadata{}, err
	}
	if err := checkRecordFile(root, id); err != nil {
		return Metadata{}, fmt.Errorf("read session %q: %w", id, err)
	}
	data, err := root.ReadFile(id + ".json")
	if err != nil {
		return Metadata{}, fmt.Errorf("read session %q: %w", id, err)
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return Metadata{}, fmt.Errorf("decode session %q: %w", id, err)
	}
	if r.Version != 1 {
		return Metadata{}, fmt.Errorf("session %q: unsupported metadata version %d", id, r.Version)
	}
	if r.ID != id {
		return Metadata{}, fmt.Errorf("session %q: metadata ID does not match filename", id)
	}
	if err := r.Metadata.validate(); err != nil {
		return Metadata{}, fmt.Errorf("session %q: %w", id, err)
	}
	return r.Metadata, nil
}

func checkRecordFile(root *os.Root, id string) error {
	info, err := root.Lstat(id + ".json")
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("metadata must be a regular file")
	}
	return nil
}

func validateID(id string) error {
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return fmt.Errorf("session ID must be a full 64-character lowercase hexadecimal container ID")
	}
	return nil
}

func (m Metadata) validate() error {
	if err := validateID(m.ID); err != nil {
		return err
	}
	if err := validateAgent(m.Agent); err != nil {
		return err
	}
	if m.Image == "" {
		return fmt.Errorf("session image must not be empty")
	}
	for _, value := range []string{m.Image, m.Name} {
		if strings.HasPrefix(value, "-") || strings.ContainsFunc(value, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) {
			return fmt.Errorf("session image and name must contain no whitespace or control characters and must not start with '-'")
		}
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("session creation time must not be zero")
	}
	return nil
}

func validateAgent(agent string) error {
	switch agent {
	case "claude", "codex", "opencode":
		return nil
	default:
		return fmt.Errorf("unsupported session agent")
	}
}
