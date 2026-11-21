package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A suspicious request needs approval (SRNA): the gateway records a request
// to an unlisted host in the state directory and waits; the user allows or
// denies it from the host. Only the gateway and the host CLI can write here.

// Request is one recorded SRNA. Run and Agent name the agent connection that
// made it when the request carried them; abort stops that connection.
type Request struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Target  string    `json:"target"`
	Run     string    `json:"run,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Created time.Time `json:"created"`
	// Decision is "allow", "deny", or empty while pending.
	Decision string `json:"-"`
}

var requestID = regexp.MustCompile(`^[0-9a-f]{12}$`)

func requestsDir(state string) string  { return filepath.Join(state, "requests") }
func decisionsDir(state string) string { return filepath.Join(state, "decisions") }

// Requests lists recorded requests in creation order with their decisions.
func Requests(state string) ([]Request, error) {
	entries, err := os.ReadDir(requestsDir(state))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list gateway requests: %w", err)
	}
	var list []Request
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !requestID.MatchString(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(requestsDir(state), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read gateway request: %w", err)
		}
		var r Request
		if err := json.Unmarshal(data, &r); err != nil || r.ID != id {
			continue
		}
		decision, err := os.ReadFile(filepath.Join(decisionsDir(state), id))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read gateway decision: %w", err)
		}
		if d := strings.TrimSpace(string(decision)); d == "allow" || d == "deny" {
			r.Decision = d
		}
		list = append(list, r)
	}
	slices.SortFunc(list, func(a, b Request) int { return a.Created.Compare(b.Created) })
	return list, nil
}

// Decide records the user's decision for a pending request.
func Decide(state, id string, allow bool) error {
	if !requestID.MatchString(id) {
		return fmt.Errorf("invalid request ID %q", id)
	}
	if _, err := os.Stat(filepath.Join(requestsDir(state), id+".json")); err != nil {
		return fmt.Errorf("request %s: %w", id, err)
	}
	decision := "deny"
	if allow {
		decision = "allow"
	}
	return writeAtomic(filepath.Join(decisionsDir(state), id), []byte(decision+"\n"))
}

// Record adds r as a pending request with a new ID and returns it.
func Record(state string, r Request) (Request, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return Request{}, err
	}
	r.ID, r.Created, r.Decision = hex.EncodeToString(random), time.Now().UTC(), ""
	data, err := json.Marshal(r)
	if err != nil {
		return Request{}, err
	}
	return r, writeAtomic(filepath.Join(requestsDir(state), r.ID+".json"), data)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create gateway state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("write gateway state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write gateway state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write gateway state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write gateway state: %w", err)
	}
	return nil
}
