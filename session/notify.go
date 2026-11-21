package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hewitt-iso/hewitt/terminal"
)

// watchRequests shows this launch's suspicious requests where its agent
// waits: a popup over the agent's host tmux pane that asks for a decision, or
// a desktop notification when no tmux client shows the pane. Requests that
// name no launch are shown by the one launch of the workspace that holds its
// unattributed lock. It returns when ctx ends; there is no background daemon.
func watchRequests(ctx context.Context, store *Store, m Metadata, run AgentRun, stderr io.Writer) {
	if m.Egress == nil {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		return
	}
	unattributed := holdLock(ctx, store.dataDir, "srna-unattributed-"+m.ID)
	offered := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		pending := pendingFor(store, m, run.ID, unattributed())
		var fresh []SessionRequest
		for _, r := range pending {
			if !offered[r.ID] {
				fresh = append(fresh, r)
			}
		}
		if len(fresh) == 0 || popupOpen(store.dataDir, run.ID) {
			continue
		}
		for _, r := range pending {
			offered[r.ID] = true
		}
		first := fresh[0]
		who := first.Agent
		if who == "" {
			who = "an unattributed process"
		}
		title := "Hewitt: suspicious request needs approval"
		command := []string{executable, "srna-prompt", "--data-dir", store.dataDir, "--session", m.ID, "--run", run.ID}
		if unattributed() {
			command = append(command, "--unattributed")
		}
		message := fmt.Sprintf("Hewitt SRNA #%d: %s wants %s %s; hewitt approve %d / deny %d / abort %d",
			first.Number, who, first.Kind, first.Target, first.Number, first.Number, first.Number)
		go func() {
			err := terminal.Popup(ctx, run.Terminal, " "+title+" ", command, message)
			if err == nil || ctx.Err() != nil {
				return
			}
			if !terminal.Notify(ctx, "Hewitt: suspicious request", message) {
				// Without tmux or notifications, stderr is the agent's screen;
				// one line there is the last resort.
				fmt.Fprintf(stderr, "\r\n[hewitt] %s\r\n", message)
			}
		}()
	}
}

// pendingFor lists undecided requests of the workspace for one launch.
func pendingFor(store *Store, m Metadata, run string, unattributed bool) []SessionRequest {
	list, err := store.Requests()
	if err != nil {
		return nil
	}
	var pending []SessionRequest
	for _, r := range list {
		if r.SessionID == m.ID && r.Decision == "" && (r.Run == run || unattributed && r.Run == "") {
			pending = append(pending, r)
		}
	}
	return pending
}

// PendingFor is pendingFor for the prompt shown in a popup.
func (s *Store) PendingFor(sessionID, run string, unattributed bool) ([]SessionRequest, error) {
	m, err := s.Get(sessionID)
	if err != nil {
		return nil, err
	}
	return pendingFor(s, m, run, unattributed), nil
}

// holdLock takes a named lock for as long as ctx lasts, retrying while
// another holder has it, and reports whether this caller holds it now.
func holdLock(ctx context.Context, dataDir, name string) func() bool {
	path := filepath.Join(dataDir, "locks", name)
	held := false
	var f *os.File
	return func() bool {
		if held {
			return true
		}
		if f == nil {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return false
			}
			var err error
			if f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err != nil {
				return false
			}
			go func() { <-ctx.Done(); f.Close() }()
		}
		held = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
		return held
	}
}

// PopupLock is held by the prompt of one launch while its popup is open, so
// the launch does not open a second popup over it.
func PopupLock(dataDir, run string) (func(), error) {
	path := filepath.Join(dataDir, "locks", "srna-popup-"+run)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another prompt for this launch is open")
	}
	return func() { f.Close() }, nil
}

func popupOpen(dataDir, run string) bool {
	release, err := PopupLock(dataDir, run)
	if err != nil {
		return true
	}
	release()
	return false
}
