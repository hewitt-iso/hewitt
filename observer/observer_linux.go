//go:build linux

package observer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Run finds the workspace container, marks its watched mounts, drops to the
// configured user, and answers file opens until the container is gone.
// Setting the marks needs CAP_SYS_ADMIN and the host PID namespace; the
// fanotify descriptor keeps working after privileges are dropped.
func Run(cfg Config) error {
	pid, err := findContainer(cfg.Token, 30*time.Second)
	if err != nil {
		return err
	}
	ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", pid))
	if err != nil {
		return fmt.Errorf("inspect workspace container: %w", err)
	}
	fd, err := unix.FanotifyInit(unix.FAN_CLASS_CONTENT|unix.FAN_CLOEXEC|unix.FAN_UNLIMITED_QUEUE, unix.O_RDONLY|unix.O_LARGEFILE|unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("fanotify is unavailable (it needs a Linux kernel with fanotify permission events and CAP_SYS_ADMIN): %w", err)
	}
	root := fmt.Sprintf("/proc/%d/root", pid)
	for _, w := range cfg.Watches {
		if err := unix.FanotifyMark(fd, unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT, unix.FAN_OPEN_PERM, unix.AT_FDCWD, root+w.Path); err != nil {
			return fmt.Errorf("observe %s: %w", w.Path, err)
		}
	}
	if err := dropPrivileges(cfg.UID, cfg.GID); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "observing workspace process %d: %d mounts\n", pid, len(cfg.Watches))
	o := &linuxObserver{policy: policy{cfg: cfg, poll: 250 * time.Millisecond}, fd: fd, ns: ns, root: root, cache: map[int]cached{}}
	go o.watchContainer(pid)
	return o.serve()
}

type cached struct {
	start  string
	opener Opener
}

type linuxObserver struct {
	policy
	fd       int
	ns, root string
	mu       sync.Mutex
	cache    map[int]cached
}

const metadataSize = 24

func (o *linuxObserver) serve() error {
	buf := make([]byte, 64*1024)
	for {
		n, err := unix.Read(o.fd, buf)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read file events: %w", err)
		}
		for off := 0; off+metadataSize <= n; {
			length := int(binary.NativeEndian.Uint32(buf[off:]))
			if buf[off+4] != unix.FANOTIFY_METADATA_VERSION || length < metadataSize {
				return fmt.Errorf("unsupported fanotify event version %d", buf[off+4])
			}
			mask := binary.NativeEndian.Uint64(buf[off+8:])
			fd := int32(binary.NativeEndian.Uint32(buf[off+16:]))
			pid := int(int32(binary.NativeEndian.Uint32(buf[off+20:])))
			off += length
			if fd < 0 {
				continue
			}
			if mask&unix.FAN_OPEN_PERM == 0 {
				unix.Close(int(fd))
				continue
			}
			o.handle(fd, pid)
		}
	}
}

// handle answers ordinary opens at once and waits for the user's decision on
// held ones in the background, so other opens proceed meanwhile.
func (o *linuxObserver) handle(fd int32, pid int) {
	path, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(fd)))
	if err != nil {
		o.respond(fd, true)
		return
	}
	path = strings.TrimPrefix(path, o.root)
	hold, opener := o.check(path, func() Opener { return o.opener(pid) })
	if !hold {
		o.respond(fd, true)
		return
	}
	go func() { o.respond(fd, o.decide(path, opener)) }()
}

func (o *linuxObserver) respond(fd int32, allow bool) {
	response := uint32(unix.FAN_DENY)
	if allow {
		response = unix.FAN_ALLOW
	}
	var b [8]byte
	binary.NativeEndian.PutUint32(b[0:], uint32(fd))
	binary.NativeEndian.PutUint32(b[4:], response)
	if _, err := unix.Write(o.fd, b[:]); err != nil {
		fmt.Fprintf(os.Stderr, "answer file event: %v\n", err)
	}
	unix.Close(int(fd))
}

// opener attributes a process to the topmost ancestor in the workspace
// container that carries HEWITT_RUN: the launch Hewitt started. Descendants
// cannot claim another identity, and a process that left the launch's tree
// is attributed to none.
func (o *linuxObserver) opener(pid int) Opener {
	start := startTime(pid)
	o.mu.Lock()
	if c, ok := o.cache[pid]; ok && c.start == start {
		o.mu.Unlock()
		return c.opener
	}
	o.mu.Unlock()
	var found Opener
	for p, depth := pid, 0; p > 1 && depth < 256; depth++ {
		if ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", p)); err != nil || ns != o.ns {
			break
		}
		if run, agent := launch(p); run != "" {
			found = Opener{Run: run, Agent: agent}
		}
		p = parent(p)
	}
	o.mu.Lock()
	if len(o.cache) > 4096 {
		o.cache = map[int]cached{}
	}
	o.cache[pid] = cached{start: start, opener: found}
	o.mu.Unlock()
	return found
}

// watchContainer exits the observer once the workspace container is gone.
func (o *linuxObserver) watchContainer(pid int) {
	for range time.Tick(2 * time.Second) {
		if ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", pid)); err != nil || ns != o.ns {
			fmt.Fprintln(os.Stderr, "workspace container stopped")
			os.Exit(0)
		}
	}
}

// findContainer returns the host PID of the init process of the container
// whose environment carries the token.
func findContainer(token string, timeout time.Duration) (int, error) {
	want := []byte("HEWITT_OBSERVE=" + token)
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
			if err != nil || !bytes.Contains(env, want) {
				continue
			}
			for _, entry := range bytes.Split(env, []byte{0}) {
				if bytes.Equal(entry, want) && namespaceInit(pid) {
					return pid, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("no workspace container with this observer token is running")
}

// namespaceInit reports whether pid is PID 1 of its PID namespace.
func namespaceInit(pid int) bool {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(status), "\n") {
		if ids, ok := strings.CutPrefix(line, "NSpid:"); ok {
			fields := strings.Fields(ids)
			return len(fields) > 1 && fields[len(fields)-1] == "1"
		}
	}
	return false
}

func launch(pid int) (run, agent string) {
	env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return "", ""
	}
	for _, entry := range strings.Split(string(env), "\x00") {
		if v, ok := strings.CutPrefix(entry, "HEWITT_RUN="); ok {
			run = v
		} else if v, ok := strings.CutPrefix(entry, "HEWITT_AGENT="); ok {
			agent = v
		}
	}
	if run == "" || agent == "" {
		return "", ""
	}
	return run, agent
}

// stat returns the fields after the command name in /proc/<pid>/stat.
func stat(pid int) []string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil
	}
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return nil
	}
	return strings.Fields(string(data[i+1:]))
}

func parent(pid int) int {
	if f := stat(pid); len(f) > 1 {
		if p, err := strconv.Atoi(f[1]); err == nil {
			return p
		}
	}
	return 0
}

func startTime(pid int) string {
	if f := stat(pid); len(f) > 19 {
		return f[19]
	}
	return ""
}

// dropPrivileges switches every thread to uid and gid with no capabilities.
func dropPrivileges(uid, gid int) error {
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("drop groups: %w", err)
	}
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("drop group: %w", err)
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop user: %w", err)
	}
	return nil
}
