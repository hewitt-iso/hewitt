// Package agent describes native coding clients independently of Docker and
// Cobra. Each client is a manifest: the built-in ones in manifests/, compiled
// into Hewitt, and the user's own in <data_dir>/agents, which may add clients
// or replace built-in ones of the same name.
package agent

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// ProfilePath is where authentication containers mount the one profile they
// use. Workspace containers mount every agent's profile under ProfilesPath.
const ProfilePath = "/hewitt/profile"

// ProfilesPath holds one profile per agent in workspace containers.
const ProfilesPath = "/hewitt/profiles"

// DefaultImage is the unified workspace image used when no image is specified.
const DefaultImage = "ghcr.io/hewitt-iso/hewitt:latest"

//go:embed manifests/*.yaml
var builtinFiles embed.FS

// order lists built-in agents in display order; user agents follow by name.
var order = []string{"claude", "codex", "opencode", "pi", "antigravity"}

var (
	agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	envKey    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	binary    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	npmName   = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	version   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`)
	hostRule  = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)
)

// ValidName reports whether name can name an agent, its profile, and its
// manifest file.
func ValidName(name string) bool { return agentName.MatchString(name) }

// Manifest describes how Hewitt installs, launches, and authenticates one
// native client. It holds no credentials.
type Manifest struct {
	Version int    `yaml:"version"`
	Name    string `yaml:"name"`
	// Binary is the client's executable in the tool cache.
	Binary  string  `yaml:"binary"`
	Image   string  `yaml:"image"`
	Install Install `yaml:"install"`
	// Env is set for every launch; {profile} is the agent's profile directory.
	Env map[string]string `yaml:"env"`
	// UnsetEnv names variables removed before launch even when an image sets
	// them; keys from the user's env file are kept.
	UnsetEnv []string `yaml:"unset_env"`
	// Wrap names a launch handler, such as keyring.
	Wrap     string   `yaml:"wrap"`
	Commands Commands `yaml:"commands"`
	// Task, when set, lets the client take a task from hewitt run -p.
	Task *Task `yaml:"task"`
	// Telemetry is appended to run arguments when telemetry is configured;
	// {endpoint} is the OTLP endpoint.
	Telemetry []string `yaml:"telemetry"`
	Token     *Token   `yaml:"token"`
	// ConfigDir is where the client reads user-level rules and settings, and
	// Instructions its main rules file; both lie in {profile}.
	ConfigDir    string   `yaml:"config_dir"`
	Instructions string   `yaml:"instructions"`
	Hosts        []string `yaml:"hosts"`
	// Herdr is the agent kind Herdr knows the client by.
	Herdr string `yaml:"herdr"`
	// Source is the manifest file, or "built-in".
	Source string `yaml:"-"`
}

// Install describes one installation handler: an npm package or an install
// script that leaves the executable at Output, relative to its temporary
// home. Script arguments may use {version} and {tmp}.
type Install struct {
	NPM     string   `yaml:"npm"`
	Scripts *bool    `yaml:"scripts"`
	Script  string   `yaml:"script"`
	Args    []string `yaml:"args"`
	Output  string   `yaml:"output"`
	Version string   `yaml:"version"`
}

// Commands are the client's arguments for each operation. Headless runs use
// Run unless Headless is set.
type Commands struct {
	Run      []string `yaml:"run"`
	Headless []string `yaml:"headless"`
	Login    Command  `yaml:"login"`
	Status   Command  `yaml:"status"`
	Logout   Command  `yaml:"logout"`
}

// Command is either the client's arguments or a slash command that Hewitt
// types into the client's interface.
type Command struct {
	Args  []string
	Slash string
}

func (c *Command) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		return n.Decode(&c.Args)
	}
	var v struct {
		Slash string `yaml:"slash"`
	}
	if err := n.Decode(&v); err != nil {
		return err
	}
	if !strings.HasPrefix(v.Slash, "/") || strings.ContainsAny(v.Slash, "\n\r") {
		return fmt.Errorf("line %d: slash must be one command starting with /", n.Line)
	}
	c.Slash = v.Slash
	return nil
}

// Task gives the arguments that pass a task, {prompt}, to the client.
type Task struct {
	Interactive []string `yaml:"interactive"`
	Headless    []string `yaml:"headless"`
}

// Token is a long-lived token that the client's login prints and Hewitt
// saves. Behind a gateway, agents hold Placeholder and the gateway replaces
// it with the token in requests to Hosts.
type Token struct {
	Variable    string   `yaml:"variable"`
	Placeholder string   `yaml:"placeholder"`
	Hosts       []string `yaml:"hosts"`
}

var builtin = sync.OnceValues(func() ([]Manifest, error) {
	var list []Manifest
	for _, name := range order {
		data, err := builtinFiles.ReadFile("manifests/" + name + ".yaml")
		if err != nil {
			return nil, err
		}
		m, err := parse(data, name, "built-in")
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, nil
})

// Builtin returns the manifests compiled into Hewitt.
func Builtin() []Manifest {
	list, err := builtin()
	if err != nil {
		panic(fmt.Sprintf("built-in agent manifests: %v", err))
	}
	return slices.Clone(list)
}

// Registry is the set of agents available to one data directory.
type Registry struct{ list []Manifest }

// Load returns the built-in agents and the user's manifests in
// <dataDir>/agents/<name>.yaml. A user manifest replaces the built-in agent
// of the same name. Invalid manifests are errors, never silently skipped.
func Load(dataDir string) (*Registry, error) {
	r := &Registry{list: Builtin()}
	if dataDir == "" {
		return r, nil
	}
	dir := filepath.Join(dataDir, "agents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent manifests: %w", err)
	}
	var added []Manifest
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok {
			name, ok = strings.CutSuffix(e.Name(), ".yml")
		}
		if !ok || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read agent manifest: %w", err)
		}
		m, err := parse(data, name, path)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(r.list, func(b Manifest) bool { return b.Name == m.Name }); i >= 0 {
			if r.list[i].Source != "built-in" {
				return nil, fmt.Errorf("agent %s is defined by both %s and %s", m.Name, r.list[i].Source, path)
			}
			r.list[i] = m
			continue
		}
		if slices.ContainsFunc(added, func(b Manifest) bool { return b.Name == m.Name }) {
			return nil, fmt.Errorf("agent %s is defined twice in %s", m.Name, dir)
		}
		added = append(added, m)
	}
	slices.SortFunc(added, func(a, b Manifest) int { return strings.Compare(a.Name, b.Name) })
	r.list = append(r.list, added...)
	return r, nil
}

// Names lists the agents in display order.
func (r *Registry) Names() []string {
	names := make([]string, len(r.list))
	for i, m := range r.list {
		names[i] = m.Name
	}
	return names
}

// TaskAgents lists the agents that accept a task from hewitt run -p.
func (r *Registry) TaskAgents() []string {
	var names []string
	for _, m := range r.list {
		if m.Task != nil {
			names = append(names, m.Name)
		}
	}
	return names
}

// All returns every manifest in display order.
func (r *Registry) All() []Manifest { return slices.Clone(r.list) }

// Lookup returns the manifest of one agent.
func (r *Registry) Lookup(name string) (Manifest, error) {
	for _, m := range r.list {
		if m.Name == name {
			return m, nil
		}
	}
	return Manifest{}, &UnknownAgentError{Name: name, Supported: r.Names()}
}

func parse(data []byte, name, source string) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, &ManifestError{Source: source, Err: err}
	}
	m.Source = source
	if err := m.validate(name); err != nil {
		return Manifest{}, &ManifestError{Source: source, Err: err}
	}
	return m, nil
}

func (m Manifest) validate(file string) error {
	switch {
	case m.Version != 1:
		return fmt.Errorf("version must be 1")
	case !ValidName(m.Name) || m.Name == "workspace" || m.Name == "shell":
		return fmt.Errorf("name %q must be 1-32 lowercase letters, digits, or hyphens, start with a letter, and not be workspace or shell", m.Name)
	case m.Name != file:
		return fmt.Errorf("name %s must match the file name %s", m.Name, file)
	case !binary.MatchString(m.Binary):
		return fmt.Errorf("binary %q must be a plain executable name", m.Binary)
	case strings.ContainsFunc(m.Image, func(r rune) bool { return r <= ' ' }) || strings.HasPrefix(m.Image, "-"):
		return fmt.Errorf("image %q is not a Docker image reference", m.Image)
	case m.Wrap != "" && m.Wrap != "keyring":
		return fmt.Errorf("wrap %q is not a launch handler (known: keyring)", m.Wrap)
	case len(m.Hosts) == 0:
		return fmt.Errorf("hosts must list the provider endpoints restricted sessions allow")
	}
	if err := m.Install.validate(); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	for key, value := range m.Env {
		if !envKey.MatchString(key) || strings.HasPrefix(key, "HEWITT_") {
			return fmt.Errorf("env: %q is not a variable name Hewitt passes to agents", key)
		}
		if strings.ContainsAny(value, "\x00\n") {
			return fmt.Errorf("env %s: value must be one line", key)
		}
	}
	for _, key := range m.UnsetEnv {
		if !envKey.MatchString(key) {
			return fmt.Errorf("unset_env: %q is not a variable name", key)
		}
	}
	args := map[string][]string{"run": m.Commands.Run, "headless": m.Commands.Headless, "login": m.Commands.Login.Args,
		"status": m.Commands.Status.Args, "logout": m.Commands.Logout.Args, "telemetry": m.Telemetry}
	if m.Task != nil {
		if !slices.ContainsFunc(m.Task.Interactive, hasPrompt) || !slices.ContainsFunc(m.Task.Headless, hasPrompt) {
			return fmt.Errorf("task: interactive and headless must both pass {prompt}")
		}
		args["task.interactive"], args["task.headless"] = m.Task.Interactive, m.Task.Headless
	}
	for field, list := range args {
		for _, arg := range list {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("commands.%s: arguments must not contain NUL", field)
			}
			if hasPrompt(arg) && !strings.HasPrefix(field, "task.") {
				return fmt.Errorf("%s: only task arguments may use {prompt}", field)
			}
		}
	}
	if m.Token != nil {
		if !envKey.MatchString(m.Token.Variable) || !slices.Contains(m.UnsetEnv, m.Token.Variable) {
			return fmt.Errorf("token: variable must be a variable name that unset_env also lists")
		}
		if len(m.Token.Hosts) > 0 && (m.Token.Placeholder == "" || strings.ContainsFunc(m.Token.Placeholder, func(r rune) bool { return r <= ' ' })) {
			return fmt.Errorf("token: hosts need a placeholder without whitespace")
		}
		for _, host := range m.Token.Hosts {
			if !hostRule.MatchString(host) || strings.HasPrefix(host, "*.") {
				return fmt.Errorf("token: %q must be an exact host", host)
			}
		}
	}
	for _, host := range m.Hosts {
		if !hostRule.MatchString(host) {
			return fmt.Errorf("hosts: %q must be a lowercase host, *.domain, or host:port", host)
		}
	}
	for field, path := range map[string]string{"config_dir": m.ConfigDir, "instructions": m.Instructions} {
		rest, ok := strings.CutPrefix(path, "{profile}/")
		if !ok || rest == "" || filepath.Clean(rest) != rest || strings.HasPrefix(rest, "..") {
			return fmt.Errorf("%s %q must be a path inside {profile}", field, path)
		}
	}
	if !strings.HasPrefix(m.Instructions, m.ConfigDir+"/") {
		return fmt.Errorf("instructions must lie in config_dir")
	}
	if m.Herdr != "" && !binary.MatchString(m.Herdr) {
		return fmt.Errorf("herdr %q must be a Herdr agent kind", m.Herdr)
	}
	return nil
}

func (i Install) validate() error {
	switch {
	case i.NPM != "" && i.Script != "":
		return fmt.Errorf("use either npm or script")
	case i.NPM != "":
		if !npmName.MatchString(i.NPM) || len(i.Args) > 0 || i.Output != "" {
			return fmt.Errorf("npm %q must be a package name; args and output apply to scripts", i.NPM)
		}
	case i.Script != "":
		if !strings.HasPrefix(i.Script, "https://") || strings.ContainsFunc(i.Script, func(r rune) bool { return r <= ' ' }) {
			return fmt.Errorf("script must be an https:// URL")
		}
		if i.Scripts != nil {
			return fmt.Errorf("scripts applies to npm packages")
		}
		if i.Output == "" || filepath.IsAbs(i.Output) || filepath.Clean(i.Output) != i.Output || strings.HasPrefix(i.Output, "..") {
			return fmt.Errorf("output must be a relative path inside the installer's temporary home")
		}
		for _, arg := range i.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("args must not contain NUL")
			}
		}
	default:
		return fmt.Errorf("set npm or script")
	}
	if i.Version != "" && !version.MatchString(i.Version) {
		return fmt.Errorf("version %q must be a version or tag", i.Version)
	}
	return nil
}

func hasPrompt(s string) bool { return strings.Contains(s, "{prompt}") }

// Profile returns path with {profile} replaced by root.
func Profile(path, root string) string { return strings.Replace(path, "{profile}", root, 1) }
