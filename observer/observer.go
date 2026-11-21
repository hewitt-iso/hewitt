// Package observer holds agents' access to files they should not need, such
// as another agent's login or a credential file in the workspace, until the
// user decides. It runs as hewitt observe in its own container, attaches
// fanotify permission marks to one workspace container's mounts, drops to
// the user's UID, and records each held access as a suspicious request that
// needs approval (SRNA) in the session's state directory, which the gateway
// shares. Agents run as one UID in one container, so mounts cannot separate
// them; the observer tells them apart by the HEWITT_RUN and HEWITT_AGENT that
// Hewitt gives the first process of each launch.
package observer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hewitt-iso/hewitt/gateway"
	"github.com/hewitt-iso/hewitt/profile"
)

// Config is written by Hewitt on the host and read by the observer.
type Config struct {
	// Token is the HEWITT_OBSERVE value of the workspace container to watch.
	Token string `json:"token"`
	// UID and GID are the user the observer runs as after setting its marks.
	UID int `json:"uid"`
	GID int `json:"gid"`
	// Watches are container paths of mounts to observe.
	Watches []Watch `json:"watches"`
	// StateDir is the session state directory shared with the gateway.
	StateDir string `json:"state_dir"`
	// Wait is how long a held access waits for a decision, in seconds; an
	// unanswered access is refused.
	Wait int `json:"wait"`
}

// Watch is one observed mount. Opening a file under Path is held unless the
// opener is Owner, the agent whose login lives there; a watch without an owner
// holds only credential files, such as .env or keys, in a workspace.
type Watch struct {
	Path  string `json:"path"`
	Owner string `json:"owner,omitempty"`
}

// LoadConfig reads and validates a configuration file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read observer configuration: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse observer configuration: %w", err)
	}
	if c.Token == "" || c.StateDir == "" || len(c.Watches) == 0 || c.UID <= 0 || c.GID < 0 {
		return Config{}, fmt.Errorf("observer configuration needs token, state_dir, watches, and a non-root uid")
	}
	for _, w := range c.Watches {
		if !filepath.IsAbs(w.Path) {
			return Config{}, fmt.Errorf("observer watch %q must be an absolute container path", w.Path)
		}
	}
	if c.Wait <= 0 {
		c.Wait = 60
	}
	return c, nil
}

// Opener identifies the agent connection behind a file access. Run is empty
// when the process descends from no Hewitt launch.
type Opener struct {
	Run, Agent string
}

// policy decides file accesses and remembers the user's decisions.
type policy struct {
	cfg  Config
	poll time.Duration
	// mu serializes request recording, so concurrent opens of one file by
	// one agent share one pending request.
	mu sync.Mutex
}

// check reports whether an open of path needs the user's approval, and by
// whom. opener is called only for opens that can be held, since most opens
// in a workspace are of ordinary files. The shell is the user, who may read
// anything.
func (p *policy) check(path string, opener func() Opener) (bool, Opener) {
	for _, w := range p.cfg.Watches {
		if path != w.Path && !strings.HasPrefix(path, w.Path+"/") {
			continue
		}
		if w.Owner == "" && !profile.SensitiveFile(filepath.Base(path)) {
			return false, Opener{}
		}
		o := opener()
		if o.Agent == "shell" && o.Run != "" {
			return false, o
		}
		return w.Owner == "" || o.Run == "" || o.Agent != w.Owner, o
	}
	return false, Opener{}
}

// decide waits for the user's decision on a held access: the latest decision
// for the same file and agent applies, otherwise a new pending request waits
// until the deadline. It reports whether to allow the open.
func (p *policy) decide(path string, o Opener) bool {
	id, decision, err := p.request(path, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "record request for %s: %v\n", path, err)
		return false
	}
	deadline := time.Now().Add(time.Duration(p.cfg.Wait) * time.Second)
	for decision == "" && time.Now().Before(deadline) {
		time.Sleep(p.poll)
		decision = p.decision(id)
	}
	who := o.Agent
	if who == "" {
		who = "unattributed process"
	}
	switch decision {
	case "allow":
		fmt.Fprintf(os.Stderr, "approved %s: %s opens %s\n", id, who, path)
		return true
	case "deny":
		fmt.Fprintf(os.Stderr, "denied %s: %s opens %s\n", id, who, path)
	default:
		fmt.Fprintf(os.Stderr, "pending approval %s: %s opens %s (refused for now)\n", id, who, path)
	}
	return false
}

func (p *policy) request(path string, o Opener) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list, err := gateway.Requests(p.cfg.StateDir)
	if err != nil {
		return "", "", err
	}
	// A decision covers the agent's later launches; a pending request is
	// shared only within its launch, so each launch's pane can ask.
	for i := len(list) - 1; i >= 0; i-- {
		r := list[i]
		if r.Kind == "file" && r.Target == path && r.Agent == o.Agent && (r.Decision != "" || r.Run == o.Run) {
			return r.ID, r.Decision, nil
		}
	}
	r, err := gateway.Record(p.cfg.StateDir, gateway.Request{Kind: "file", Target: path, Run: o.Run, Agent: o.Agent})
	return r.ID, "", err
}

func (p *policy) decision(id string) string {
	data, err := os.ReadFile(filepath.Join(p.cfg.StateDir, "decisions", id))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
