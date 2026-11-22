package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// TaskRecord is one task of a batch: the agent that runs it, its branch, and
// how it ended. Records are private files under <data_dir>/tasks/<batch>.
type TaskRecord struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	Agent       string    `json:"agent"`
	WorkspaceID string    `json:"workspace_id"`
	Workspace   string    `json:"workspace"`
	Run         string    `json:"run,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	State       string    `json:"state"`
	Exit        int       `json:"exit,omitempty"`
	Started     time.Time `json:"started,omitzero"`
	Finished    time.Time `json:"finished,omitzero"`
	Log         string    `json:"log"`
}

// Task states. A batch that ended without finishing a task leaves it lost.
const (
	TaskQueued   = "queued"
	TaskRunning  = "running"
	TaskDone     = "done"
	TaskFailed   = "failed"
	TaskCanceled = "canceled"
	TaskLost     = "lost"
)

var (
	taskName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	batchID  = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9]{1,3})?$`)
)

// ValidTaskName reports whether name can name a task and its branch.
func ValidTaskName(name string) error {
	if !taskName.MatchString(name) {
		return fmt.Errorf("task name %q must be 1-40 lowercase letters, digits, or hyphens", name)
	}
	return nil
}

// Batch is a running batch. Its process holds the batch lock until Close, so
// records of a batch that crashed read as lost rather than running forever.
type Batch struct {
	ID   string
	dir  string
	lock *os.File
}

// NewBatch creates the directory of a new batch and takes its lock.
func (s *Store) NewBatch() (*Batch, error) {
	parent := filepath.Join(s.dataDir, "tasks")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("create task directory: %w", err)
	}
	stamp := time.Now().Format("20060102-150405")
	for i := 1; i < 1000; i++ {
		id := stamp
		if i > 1 {
			id = fmt.Sprintf("%s-%d", stamp, i)
		}
		dir := filepath.Join(parent, id)
		if err := os.Mkdir(dir, 0o700); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("create batch directory: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("lock batch: %w", err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			return nil, fmt.Errorf("lock batch: %w", err)
		}
		return &Batch{ID: id, dir: dir, lock: f}, nil
	}
	return nil, fmt.Errorf("create batch directory: too many batches started at %s", stamp)
}

// LogPath is where a task's agent output goes.
func (b *Batch) LogPath(task string) string { return filepath.Join(b.dir, task+".log") }

// Save replaces a task's record atomically.
func (b *Batch) Save(r TaskRecord) error {
	if err := ValidTaskName(r.Name); err != nil {
		return err
	}
	r.Version = 1
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(b.dir, "."+r.Name+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("save task %s: %w", r.Name, err)
	}
	if err := os.Rename(tmp, filepath.Join(b.dir, r.Name+".json")); err != nil {
		return fmt.Errorf("save task %s: %w", r.Name, err)
	}
	return nil
}

// Close releases the batch lock.
func (b *Batch) Close() { b.lock.Close() }

// Batches lists batch IDs, oldest first.
func (s *Store) Batches() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "tasks"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list batches: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && batchID.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// BatchTasks reads the tasks of one batch in name order and reports whether
// the batch is still running. Unfinished tasks of an ended batch are lost.
func (s *Store) BatchTasks(id string) ([]TaskRecord, bool, error) {
	if !batchID.MatchString(id) {
		return nil, false, &BatchNotFoundError{ID: id}
	}
	dir := filepath.Join(s.dataDir, "tasks", id)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, &BatchNotFoundError{ID: id}
	}
	if err != nil {
		return nil, false, fmt.Errorf("read batch %s: %w", id, err)
	}
	live := false
	if f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR, 0); err == nil {
		live = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
		f.Close()
	}
	var tasks []TaskRecord
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || ValidTaskName(name) != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, false, fmt.Errorf("read task %s: %w", name, err)
		}
		var r TaskRecord
		if err := json.Unmarshal(data, &r); err != nil || r.Version != 1 || r.Name != name {
			return nil, false, fmt.Errorf("read task %s: invalid record", name)
		}
		if !live && (r.State == TaskQueued || r.State == TaskRunning) {
			r.State = TaskLost
		}
		tasks = append(tasks, r)
	}
	return tasks, live, nil
}
