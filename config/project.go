package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ProjectFile is looked up in the repository root when no file is selected
// explicitly; ProjectFileAlt is accepted as the alternative YAML extension.
const (
	ProjectFile    = "hewitt.yml"
	ProjectFileAlt = "hewitt.yaml"
)

// Project holds per-project session settings from hewitt.yml. It is parsed
// without side effects; paths stay relative to Dir until a session resolves
// them. Values set on the command line take precedence over the file.
type Project struct {
	// Dir is the absolute directory containing the file; relative paths resolve
	// against it. It is empty when no file was found.
	Dir string

	Image, Agent, Profile, Name string
	// Mode is new, exec, or sidecar; Target names the existing container for
	// exec and sidecar.
	Mode, Target string
	// Workspace is volume, worktree, direct, or copy. WorkspaceArgs is the base
	// ref for volume and worktree or the selected paths for direct and copy.
	Workspace     string
	WorkspaceArgs []string
	Workdir       string
	Git           string
	// SSH forwards the host ssh-agent socket so the agent can push over SSH.
	SSH       bool
	Mounts    []string
	Isolation string
	RootFS    string
	Network   string
	Runtime   string
	Memory    string
	CPUs      string
	PIDs      string
	Rules     string
	// EnvFile holds agent credentials such as API keys in KEY=VALUE lines.
	// Only its path is configuration; values are read by Docker, not Hewitt.
	EnvFile string
	// Allow adds hosts to the egress proxy of restricted sessions; EgressImage
	// replaces the proxy image.
	Allow       []string
	EgressImage string
	Danger      []string
	// Telemetry is an OTLP/HTTP base URL (scheme://host[:port]) that
	// supported agents export OpenTelemetry data to. It is not a credential.
	Telemetry string
}

// Allowed values shared by the file loader and command-line validation.
var (
	Modes       = []string{"new", "exec", "sidecar"}
	Workspaces  = []string{"volume", "worktree", "direct", "copy"}
	Isolations  = []string{"shared", "isolated", "restricted", "hardened"}
	DangerItems = []string{"network", "host-paths", "writable-root", "symlinks"}
)

// projectYAML is the on-disk schema. Unknown keys are rejected.
type projectYAML struct {
	Agent     string        `yaml:"agent"`
	Image     string        `yaml:"image"`
	Profile   string        `yaml:"profile"`
	Name      string        `yaml:"name"`
	Mode      string        `yaml:"mode"`
	Target    string        `yaml:"target"`
	Workspace workspaceYAML `yaml:"workspace"`
	Workdir   string        `yaml:"workdir"`
	Git       string        `yaml:"git"`
	SSH       bool          `yaml:"ssh"`
	Mounts    []string      `yaml:"mounts"`
	Rules     string        `yaml:"rules"`
	Isolation string        `yaml:"isolation"`
	RootFS    string        `yaml:"rootfs"`
	Network   string        `yaml:"network"`
	Runtime   string        `yaml:"runtime"`
	Limits    struct {
		Memory string `yaml:"memory"`
		CPUs   string `yaml:"cpus"`
		PIDs   string `yaml:"pids"`
	} `yaml:"limits"`
	EnvFile string `yaml:"env_file"`
	Egress  struct {
		Allow []string `yaml:"allow"`
		Image string   `yaml:"image"`
	} `yaml:"egress"`
	Danger    []string `yaml:"danger"`
	Telemetry struct {
		Endpoint string `yaml:"endpoint"`
	} `yaml:"telemetry"`
}

// workspaceYAML accepts either a bare type (workspace: copy) or a mapping with
// type, base (volume, worktree), and paths (direct, copy).
type workspaceYAML struct {
	Type  string   `yaml:"type"`
	Base  string   `yaml:"base"`
	Paths []string `yaml:"paths"`
}

func (w *workspaceYAML) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&w.Type)
	}
	type plain workspaceYAML
	return node.Decode((*plain)(w))
}

// LoadProject parses path. An empty path selects hewitt.yml (or hewitt.yaml)
// in dir, and a missing default file yields an empty Project; a missing
// explicit file fails.
func LoadProject(path, dir string) (Project, error) {
	if path == "" {
		found, err := findProject(dir)
		if err != nil || found == "" {
			return Project{}, err
		}
		path = found
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Project{}, fmt.Errorf("locate %s: %w", ProjectFile, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Project{}, fmt.Errorf("read %s: %w", ProjectFile, err)
	}
	p, err := ParseProject(data, path)
	if err != nil {
		return Project{}, err
	}
	p.Dir = filepath.Dir(path)
	return p, nil
}

func findProject(dir string) (string, error) {
	var found []string
	for _, name := range []string{ProjectFile, ProjectFileAlt} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("both %s and %s exist in %q; keep one", ProjectFile, ProjectFileAlt, dir)
	}
}

// ParseProject decodes and validates one YAML document. name is used only in
// error messages.
func ParseProject(data []byte, name string) (Project, error) {
	var y projectYAML
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return Project{}, fmt.Errorf("%s: %w", name, err)
	}
	p := Project{
		Image: y.Image, Agent: y.Agent, Profile: y.Profile, Name: y.Name,
		Mode: y.Mode, Target: y.Target, Workspace: y.Workspace.Type,
		Workdir: y.Workdir, Git: y.Git, SSH: y.SSH, Mounts: y.Mounts, Rules: y.Rules,
		Isolation: y.Isolation, RootFS: y.RootFS, Network: y.Network, Runtime: y.Runtime,
		Memory: y.Limits.Memory, CPUs: y.Limits.CPUs, PIDs: y.Limits.PIDs,
		EnvFile: y.EnvFile, Allow: y.Egress.Allow, EgressImage: y.Egress.Image, Danger: y.Danger,
		Telemetry: y.Telemetry.Endpoint,
	}
	if err := p.validate(y.Workspace); err != nil {
		return Project{}, fmt.Errorf("%s: %w", name, err)
	}
	return p, nil
}

func (p *Project) validate(w workspaceYAML) error {
	oneOf := func(key, value string, allowed []string) error {
		if value != "" && !slices.Contains(allowed, value) {
			return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
		}
		return nil
	}
	for _, err := range []error{
		oneOf("mode", p.Mode, Modes),
		oneOf("workspace.type", w.Type, Workspaces),
		oneOf("isolation", p.Isolation, Isolations),
		oneOf("git", p.Git, []string{"rw", "ro", "none"}),
		oneOf("rootfs", p.RootFS, []string{"ro", "rw"}),
	} {
		if err != nil {
			return err
		}
	}
	if p.Target != "" && (p.Mode == "" || p.Mode == "new") {
		return fmt.Errorf("target requires mode exec or sidecar")
	}
	if (p.Mode == "exec" || p.Mode == "sidecar") && p.Target == "" {
		return fmt.Errorf("mode %s requires target", p.Mode)
	}
	if (w.Base != "" || len(w.Paths) > 0) && w.Type == "" {
		return fmt.Errorf("workspace.base and workspace.paths require workspace.type")
	}
	switch w.Type {
	case "volume", "worktree":
		if len(w.Paths) > 0 {
			return fmt.Errorf("workspace.paths applies to direct and copy; use workspace.base for %s", w.Type)
		}
		if w.Base != "" {
			p.WorkspaceArgs = []string{w.Base}
		}
	case "direct", "copy":
		if w.Base != "" {
			return fmt.Errorf("workspace.base applies only to volume and worktree")
		}
		p.WorkspaceArgs = w.Paths
	}
	for _, item := range p.Danger {
		if !slices.Contains(DangerItems, item) {
			return fmt.Errorf("unknown danger item %q (allowed: %s)", item, strings.Join(DangerItems, ", "))
		}
	}
	for _, list := range [][]string{p.Mounts, p.Allow} {
		if slices.Contains(list, "") {
			return fmt.Errorf("mounts and egress.allow entries must be nonempty")
		}
	}
	if p.Telemetry != "" {
		endpoint, err := TelemetryEndpoint(p.Telemetry)
		if err != nil {
			return err
		}
		p.Telemetry = endpoint
	}
	return nil
}

var telemetryHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// TelemetryEndpoint validates an OTLP/HTTP base URL and returns it as
// scheme://host[:port]. Credentials, paths, and queries are refused: agents
// append the signal paths, and headers belong in the env file.
func TelemetryEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	invalid := fmt.Errorf("telemetry.endpoint %q must be http:// or https:// followed by a lowercase host and optional port", raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || !telemetryHost.MatchString(u.Hostname()) {
		return "", invalid
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", fmt.Errorf("telemetry.endpoint %q must not contain credentials, a path, or a query; put headers in env_file", raw)
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", invalid
	}
	return u.Scheme + "://" + u.Host, nil
}
