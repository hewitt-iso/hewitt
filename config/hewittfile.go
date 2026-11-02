package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// HewittfileName is looked up in the repository root, like a Dockerfile in its
// build context, when no file is selected explicitly.
const HewittfileName = "Hewittfile"

// Hewittfile holds per-project session settings. It is parsed without side
// effects; paths stay relative to Dir until a session resolves them. Values set
// on the command line take precedence over the file.
type Hewittfile struct {
	// Dir is the absolute directory containing the file; relative paths resolve
	// against it. It is empty when no file was found.
	Dir string

	Image, Agent, Profile, Name string
	// Mode is new, exec, or sidecar; Target names the existing container for
	// exec and sidecar.
	Mode, Target string
	// Workspace is worktree, direct, or copy. WorkspaceArgs is the base ref for
	// worktree or the selected paths for direct and copy.
	Workspace     string
	WorkspaceArgs []string
	Workdir       string
	Git           string
	Mounts        []string
	Isolation     string
	RootFS        string
	Network       string
	Runtime       string
	Memory        string
	CPUs          string
	PIDs          string
	Rules         string
	// EnvFile holds agent credentials such as API keys in KEY=VALUE lines.
	// Only its path is configuration; values are read by Docker, not Hewitt.
	EnvFile string
	// Allow adds hosts to the egress proxy of restricted sessions; EgressImage
	// replaces the proxy image.
	Allow       []string
	EgressImage string
	Danger      []string
}

// Allowed values shared by the file parser and command-line validation.
var (
	Modes       = []string{"new", "exec", "sidecar"}
	Workspaces  = []string{"worktree", "direct", "copy"}
	Isolations  = []string{"shared", "isolated", "restricted", "hardened"}
	DangerItems = []string{"network", "host-paths", "writable-root", "symlinks"}
)

// LoadHewittfile parses path. An empty path selects HewittfileName in dir, and a
// missing default file yields an empty Hewittfile; a missing explicit file fails.
func LoadHewittfile(path, dir string) (Hewittfile, error) {
	explicit := path != ""
	if !explicit {
		path = filepath.Join(dir, HewittfileName)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Hewittfile{}, fmt.Errorf("locate Hewittfile: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return Hewittfile{}, nil
		}
		return Hewittfile{}, fmt.Errorf("read Hewittfile: %w", err)
	}
	defer f.Close()
	h, err := ParseHewittfile(bufio.NewScanner(f), path)
	if err != nil {
		return Hewittfile{}, err
	}
	h.Dir = filepath.Dir(path)
	return h, nil
}

// ParseHewittfile reads Dockerfile-style instructions: one case-insensitive
// keyword per line followed by whitespace-separated arguments, # comments, and
// trailing backslash continuations. Settings may be given once each; MOUNT and
// DANGER accumulate. name is used only in error messages.
func ParseHewittfile(lines *bufio.Scanner, name string) (Hewittfile, error) {
	var h Hewittfile
	seen := map[string]int{}
	number, start := 0, 0
	var pending string
	for lines.Scan() {
		number++
		line := strings.TrimSpace(lines.Text())
		if pending == "" {
			start = number
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
		}
		if body, ok := strings.CutSuffix(line, "\\"); ok {
			pending += body + " "
			continue
		}
		line, pending = pending+line, ""
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		keyword, args := strings.ToUpper(fields[0]), fields[1:]
		if err := h.apply(keyword, args, seen, start); err != nil {
			return Hewittfile{}, fmt.Errorf("%s:%d: %w", name, start, err)
		}
	}
	if err := lines.Err(); err != nil {
		return Hewittfile{}, fmt.Errorf("read %s: %w", name, err)
	}
	if pending != "" {
		return Hewittfile{}, fmt.Errorf("%s:%d: unterminated line continuation", name, start)
	}
	return h, nil
}

func (h *Hewittfile) apply(keyword string, args []string, seen map[string]int, line int) error {
	if keyword != "MOUNT" && keyword != "DANGER" && keyword != "ALLOW" {
		if previous, ok := seen[keyword]; ok {
			return fmt.Errorf("%s already set on line %d", keyword, previous)
		}
		seen[keyword] = line
	}
	single := func(target *string, allowed ...string) error {
		if len(args) != 1 {
			return fmt.Errorf("%s takes exactly one argument", keyword)
		}
		if len(allowed) > 0 && !slices.Contains(allowed, args[0]) {
			return fmt.Errorf("%s must be one of %s", keyword, strings.Join(allowed, ", "))
		}
		*target = args[0]
		return nil
	}
	switch keyword {
	case "FROM":
		return single(&h.Image)
	case "AGENT":
		return single(&h.Agent)
	case "PROFILE":
		return single(&h.Profile)
	case "NAME":
		return single(&h.Name)
	case "MODE":
		if len(args) == 0 || !slices.Contains(Modes, args[0]) {
			return fmt.Errorf("MODE must be new, exec <container>, or sidecar <container>")
		}
		if (args[0] == "new") != (len(args) == 1) || len(args) > 2 {
			return fmt.Errorf("MODE new takes no container; exec and sidecar take exactly one")
		}
		h.Mode = args[0]
		if len(args) == 2 {
			h.Target = args[1]
		}
		return nil
	case "WORKSPACE":
		if len(args) == 0 || !slices.Contains(Workspaces, args[0]) {
			return fmt.Errorf("WORKSPACE must be worktree [base], direct [path[:ro|:rw]...], or copy [path...]")
		}
		if args[0] == "worktree" && len(args) > 2 {
			return fmt.Errorf("WORKSPACE worktree takes at most one base ref")
		}
		h.Workspace, h.WorkspaceArgs = args[0], args[1:]
		return nil
	case "WORKDIR":
		return single(&h.Workdir)
	case "GIT":
		return single(&h.Git, "rw", "ro", "none")
	case "MOUNT":
		if len(args) != 1 {
			return fmt.Errorf("MOUNT takes one src[:dst][:ro|:rw] argument")
		}
		h.Mounts = append(h.Mounts, args[0])
		return nil
	case "ISOLATION":
		return single(&h.Isolation, Isolations...)
	case "ROOTFS":
		return single(&h.RootFS, "ro", "rw")
	case "NETWORK":
		return single(&h.Network)
	case "RUNTIME":
		return single(&h.Runtime)
	case "MEMORY":
		return single(&h.Memory)
	case "CPUS":
		return single(&h.CPUs)
	case "PIDS":
		return single(&h.PIDs)
	case "RULES":
		return single(&h.Rules)
	case "ENV_FILE":
		return single(&h.EnvFile)
	case "ALLOW":
		if len(args) == 0 {
			return fmt.Errorf("ALLOW takes one or more hosts")
		}
		h.Allow = append(h.Allow, args...)
		return nil
	case "EGRESS_IMAGE":
		return single(&h.EgressImage)
	case "DANGER":
		if len(args) == 0 {
			return fmt.Errorf("DANGER takes one or more of %s", strings.Join(DangerItems, ", "))
		}
		for _, item := range args {
			if !slices.Contains(DangerItems, item) {
				return fmt.Errorf("unknown DANGER item %q (allowed: %s)", item, strings.Join(DangerItems, ", "))
			}
		}
		h.Danger = append(h.Danger, args...)
		return nil
	default:
		return fmt.Errorf("unknown instruction %q", keyword)
	}
}
