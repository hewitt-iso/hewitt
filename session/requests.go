package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/gateway"
)

// SessionRequest is a gateway or observer request with the session it came
// from. Number is assigned the first time Hewitt lists the request and never
// changes or repeats, so a number the user read names the same request even
// when new requests arrive meanwhile.
type SessionRequest struct {
	gateway.Request
	Number             int
	SessionID, Session string
}

// requestIndex maps request IDs to their numbers.
type requestIndex struct {
	Next    int            `json:"next"`
	Numbers map[string]int `json:"numbers"`
}

// Requests lists the requests of every saved session in creation order,
// numbering new ones.
func (s *Store) Requests() ([]SessionRequest, error) {
	records, err := s.List()
	if err != nil {
		return nil, err
	}
	var list []SessionRequest
	for _, m := range records {
		if m.Egress == nil {
			continue
		}
		requests, err := gateway.Requests(m.Egress.StateDir())
		if err != nil {
			return nil, fmt.Errorf("session %s: %w", m.ID[:12], err)
		}
		name := m.Name
		if name == "" {
			name = m.ID[:12]
		}
		for _, r := range requests {
			list = append(list, SessionRequest{Request: r, SessionID: m.ID, Session: name})
		}
	}
	slices.SortStableFunc(list, func(a, b SessionRequest) int { return a.Created.Compare(b.Created) })
	if len(list) == 0 {
		return nil, nil
	}
	return list, s.number(list)
}

// number assigns numbers under a lock, so concurrent CLIs agree.
func (s *Store) number(list []SessionRequest) error {
	if err := os.MkdirAll(filepath.Join(s.dataDir, "locks"), 0o700); err != nil {
		return fmt.Errorf("create request lock: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.dataDir, "locks", "requests"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open request lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock requests: %w", err)
	}
	path := filepath.Join(s.dataDir, "requests.json")
	index := requestIndex{Next: 1, Numbers: map[string]int{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &index); err != nil || index.Numbers == nil || index.Next < 1 {
			return fmt.Errorf("request numbers in %s are malformed", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read request numbers: %w", err)
	}
	changed := false
	for i := range list {
		n, ok := index.Numbers[list[i].ID]
		if !ok {
			n, index.Next, changed = index.Next, index.Next+1, true
			index.Numbers[list[i].ID] = n
		}
		list[i].Number = n
	}
	if !changed {
		return nil
	}
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("save request numbers: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("save request numbers: %w", err)
	}
	return nil
}

var (
	numberRange = regexp.MustCompile(`^(\d+)(?:-(\d+))?$`)
	requestRef  = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// SelectRequests resolves numbers, ranges such as 3-7, and request IDs. Every
// reference must name a listed request, so a typo decides nothing.
func (s *Store) SelectRequests(refs []string) ([]SessionRequest, error) {
	list, err := s.Requests()
	if err != nil {
		return nil, err
	}
	byNumber := map[int]SessionRequest{}
	byID := map[string]SessionRequest{}
	for _, r := range list {
		byNumber[r.Number], byID[r.ID] = r, r
	}
	var selected []SessionRequest
	seen := map[string]bool{}
	add := func(r SessionRequest) {
		if !seen[r.ID] {
			seen[r.ID] = true
			selected = append(selected, r)
		}
	}
	for _, ref := range refs {
		for _, part := range strings.FieldsFunc(ref, func(c rune) bool { return c == ',' }) {
			if r, ok := byID[part]; ok && requestRef.MatchString(part) {
				add(r)
				continue
			}
			m := numberRange.FindStringSubmatch(part)
			if m == nil {
				return nil, &RequestNotFoundError{ID: part}
			}
			first, _ := strconv.Atoi(m[1])
			last := first
			if m[2] != "" {
				last, _ = strconv.Atoi(m[2])
			}
			if last < first || last-first > 10000 {
				return nil, fmt.Errorf("request range %q must go from a lower to a higher number", part)
			}
			for n := first; n <= last; n++ {
				r, ok := byNumber[n]
				if !ok {
					return nil, &RequestNotFoundError{ID: "#" + strconv.Itoa(n)}
				}
				add(r)
			}
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select requests by number, range (3-7), or ID")
	}
	return selected, nil
}

// Decide allows or denies a request. A decided request can be decided again;
// later connections and file opens follow the latest decision.
func (s *Store) Decide(r SessionRequest, allow bool) error {
	m, err := s.Get(r.SessionID)
	if err != nil {
		return err
	}
	return gateway.Decide(m.Egress.StateDir(), r.ID, allow)
}

// abortScript kills every process of one agent connection in the container:
// those started with its HEWITT_RUN, and their descendants, even if they
// cleared their environment.
const abortScript = `set -u
run=$1 found=
for e in /proc/[0-9]*/environ; do
	if tr '\0' '\n' <"$e" 2>/dev/null | grep -qx "HEWITT_RUN=$run"; then
		p=${e#/proc/}; found="$found ${p%/environ}"
	fi
done
[ -n "$found" ] || exit 3
all=" $found " changed=1
while [ "$changed" = 1 ]; do
	changed=0
	for s in /proc/[0-9]*/stat; do
		p=${s#/proc/}; p=${p%/stat}
		case "$all" in *" $p "*) continue ;; esac
		ppid=$(sed 's/.*) . \([0-9]*\) .*/\1/' "$s" 2>/dev/null) || continue
		case "$all" in *" $ppid "*) all="$all$p "; changed=1 ;; esac
	done
done
kill -KILL $all 2>/dev/null
echo $all`

// Abort denies a request and kills the agent connection that made it.
func (s *Store) Abort(ctx context.Context, r SessionRequest) error {
	if err := s.Decide(r, false); err != nil {
		return err
	}
	if r.Run == "" {
		return &UnattributedRequestError{Number: r.Number}
	}
	if err := stopRun(ctx, r.SessionID, r.Run); err != nil {
		return fmt.Errorf("request #%d denied; stop %s: %w", r.Number, r.Agent, err)
	}
	return nil
}

// stopRun kills the processes of one agent connection in its workspace
// container. A connection that already ended is not an error.
func stopRun(ctx context.Context, workspaceID, run string) error {
	_, err := (docker.Client{}).Exec(ctx, workspaceID, []string{"sh", "-c", abortScript, "sh", run})
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return nil
	}
	return err
}
