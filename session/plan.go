package session

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/docker"
)

// settings are the merged run settings: command-line values over the
// hewitt.yml, then defaults. They are checked before any side effects.
type settings struct {
	config.Project
	repo   string
	danger map[string]bool
	mounts []mountSpec
}

type mountSpec struct {
	src, dst string
	readOnly bool
}

func mergeSettings(opts AgentOptions) (settings, error) {
	repo := opts.Repository
	if repo == "" {
		repo = "."
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		return settings{}, fmt.Errorf("locate repository: %w", err)
	}
	file, err := config.LoadProject(opts.File, repo)
	if err != nil {
		return settings{}, err
	}
	s := settings{Project: file, repo: repo, danger: map[string]bool{}}
	for _, value := range []struct {
		name, value string
		allowed     []string
	}{{"--mode", opts.Mode, config.Modes}, {"--workspace", opts.Workspace, config.Workspaces}, {"--isolation", opts.Isolation, config.Isolations}} {
		if value.value != "" && !slices.Contains(value.allowed, value.value) {
			return settings{}, fmt.Errorf("%s must be one of %s", value.name, strings.Join(value.allowed, ", "))
		}
	}
	for _, item := range opts.Danger {
		if !slices.Contains(config.DangerItems, item) {
			return settings{}, fmt.Errorf("unknown --danger item %q (allowed: %s)", item, strings.Join(config.DangerItems, ", "))
		}
	}
	override := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	override(&s.Agent, opts.Agent)
	override(&s.Image, opts.Image)
	override(&s.Name, opts.Name)
	override(&s.Profile, opts.Profile)
	override(&s.Isolation, opts.Isolation)
	if opts.Target != "" && opts.Mode == "" {
		return settings{}, fmt.Errorf("--target requires --mode exec or --mode sidecar")
	}
	if opts.Mode != "" {
		s.Mode, s.Target = opts.Mode, opts.Target
	}
	if opts.Workspace != "" && opts.Workspace != s.Workspace {
		// File arguments belong to the file's workspace mode.
		s.Workspace, s.WorkspaceArgs = opts.Workspace, nil
	}
	if opts.Base != "" {
		if s.Workspace != "" && s.Workspace != "worktree" {
			return settings{}, fmt.Errorf("--base applies only to the worktree workspace")
		}
		s.Workspace, s.WorkspaceArgs = "worktree", []string{opts.Base}
	}
	for _, item := range append(slices.Clone(s.Danger), opts.Danger...) {
		s.danger[item] = true
	}
	if s.Agent == "" {
		if s.Mode == "exec" {
			return settings{}, fmt.Errorf("select an agent: hewitt run <agent> or agent in hewitt.yml")
		}
		s.Agent = "workspace"
	}
	if s.Mode == "" {
		s.Mode = "new"
	}
	if s.Isolation == "" {
		s.Isolation = "isolated"
		if s.Mode == "exec" {
			s.Isolation = "shared"
		}
	}
	if s.Mode != "exec" && s.Image == "" {
		if s.Agent == "workspace" {
			s.Image = agent.DefaultImage
		} else {
			adapter, err := agent.Lookup(s.Agent)
			if err != nil {
				return settings{}, err
			}
			s.Image = adapter.DefaultImage()
		}
	}
	for _, spec := range s.Mounts {
		m, err := parseMount(spec, s.Dir, true)
		if err != nil {
			return settings{}, err
		}
		s.mounts = append(s.mounts, m)
	}
	if err := s.check(); err != nil {
		return settings{}, err
	}
	if s.Mode != "exec" {
		if s.Workspace == "" {
			s.Workspace = "worktree"
		}
		if s.Git == "" {
			s.Git = "rw"
		}
		if s.Profile == "" {
			s.Profile = "default"
		}
	}
	return s, nil
}

// check enforces each isolation level. Weakening a level requires a matching
// danger item; settings a mode cannot enforce are refused, never ignored.
func (s settings) check() error {
	if (s.Mode == "new") != (s.Target == "") {
		return fmt.Errorf("mode %s: exec and sidecar require a target container; new takes none", s.Mode)
	}
	if s.Mode == "exec" {
		if s.Isolation != "shared" {
			return fmt.Errorf("exec mode runs inside an external container and cannot enforce %s isolation; use shared isolation or mode new/sidecar", s.Isolation)
		}
		for _, field := range []struct{ name, value string }{
			{"image", s.Image}, {"name", s.Name}, {"profile", s.Profile}, {"workspace", s.Workspace}, {"git", s.Git},
			{"rootfs", s.RootFS}, {"network", s.Network}, {"runtime", s.Runtime},
			{"limits.memory", s.Memory}, {"limits.cpus", s.CPUs}, {"limits.pids", s.PIDs}, {"rules", s.Rules}, {"env_file", s.EnvFile}, {"egress.image", s.EgressImage},
		} {
			if field.value != "" {
				return fmt.Errorf("%s is not supported in exec mode; the external container's own configuration applies", field.name)
			}
		}
		if len(s.mounts) > 0 || len(s.Allow) > 0 {
			return fmt.Errorf("mounts and egress.allow are not supported in exec mode; the external container's own configuration applies")
		}
		if s.Workdir != "" && !filepath.IsAbs(s.Workdir) {
			return fmt.Errorf("exec mode workdir must be an absolute container path")
		}
		return nil
	}
	if s.Image == "" {
		return fmt.Errorf("select an image: --image or image in hewitt.yml")
	}
	if s.Git != "" && s.Git != "rw" && s.Workspace != "" && s.Workspace != "worktree" {
		return fmt.Errorf("git applies only to the worktree workspace")
	}
	if s.Workdir != "" && !filepath.IsLocal(s.Workdir) {
		return fmt.Errorf("workdir must be a path inside the workspace")
	}
	level := slices.Index(config.Isolations, s.Isolation)
	need := func(item, what string) error {
		if s.danger[item] {
			return nil
		}
		return fmt.Errorf("%s isolation forbids %s; add danger: [%s] (or --danger %s) to allow it explicitly", s.Isolation, what, item, item)
	}
	var checks []error
	if s.Network == "host" {
		checks = append(checks, need("network", "host networking"))
	}
	if strings.HasPrefix(s.Network, "container:") {
		return fmt.Errorf("use mode sidecar to share another container's network")
	}
	if s.Mode == "sidecar" && s.Network != "" {
		return fmt.Errorf("sidecar mode always uses the target container's network; remove network")
	}
	if level >= 1 {
		if s.Workspace == "direct" {
			specs := s.WorkspaceArgs
			if len(specs) == 0 {
				specs = []string{"."}
			}
			for _, spec := range specs {
				if !strings.HasSuffix(spec, ":ro") {
					checks = append(checks, need("host-paths", "direct read-write access to "+spec))
				}
			}
		}
		for _, m := range s.mounts {
			if !m.readOnly {
				checks = append(checks, need("host-paths", "read-write mount "+m.src))
			}
		}
	}
	if level >= 2 {
		if s.RootFS == "rw" {
			checks = append(checks, need("writable-root", "a writable root filesystem"))
		}
		if s.Mode == "sidecar" {
			checks = append(checks, need("network", "sharing the target container's network"))
		}
		if s.Network == "bridge" || s.Network == "default" {
			checks = append(checks, need("network", "the default bridge network"))
		}
	}
	if level == 3 && (s.Runtime == "" || s.Runtime == "runc") {
		return fmt.Errorf("hardened isolation requires runtime naming a separate sandbox runtime, such as runsc or kata-runtime")
	}
	if (len(s.Allow) > 0 || s.EgressImage != "") && !s.egress() {
		return fmt.Errorf("egress.allow and egress.image apply only to restricted or hardened sessions without network")
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

// apply sets level defaults and resource limits on create options.
func (s settings) apply(create *docker.CreateOptions) {
	level := slices.Index(config.Isolations, s.Isolation)
	create.ReadOnlyRoot = level >= 2 && s.RootFS != "rw" || s.RootFS == "ro"
	create.Network = s.Network
	if s.Mode == "sidecar" {
		create.Network = "container:" + s.Target
	}
	create.Runtime, create.Memory, create.CPUs, create.PIDs = s.Runtime, s.Memory, s.CPUs, s.PIDs
}

// egress reports whether the session uses a Hewitt-owned egress proxy: the
// default network of restricted and hardened sessions in new mode.
func (s settings) egress() bool {
	return s.Mode == "new" && s.Network == "" && slices.Index(config.Isolations, s.Isolation) >= 2
}

// describe reports the effective settings when danger weakens the level, so the
// weakened configuration stays visible. It returns "" otherwise.
func (s settings) describe(create docker.CreateOptions) string {
	if len(s.danger) == 0 {
		return ""
	}
	value := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	rootfs := "rw"
	if create.ReadOnlyRoot {
		rootfs = "ro"
	}
	network := create.Network
	if s.egress() {
		network = "egress"
	} else if network == "" && s.Mode != "exec" {
		network = "default"
	}
	danger := make([]string, 0, len(s.danger))
	for _, item := range config.DangerItems {
		if s.danger[item] {
			danger = append(danger, item)
		}
	}
	return fmt.Sprintf("Settings: mode=%s target=%s isolation=%s workspace=%s rootfs=%s network=%s runtime=%s memory=%s cpus=%s pids=%s danger=%s\n",
		s.Mode, value(s.Target), s.Isolation, value(s.Workspace), rootfs, value(network), value(create.Runtime),
		value(create.Memory), value(create.CPUs), value(create.PIDs), value(strings.Join(danger, ",")))
}

// parseMount reads Docker's src[:dst][:ro|:rw] syntax. Relative sources resolve
// against base. Without a destination the source is mounted at its host path.
func parseMount(spec, base string, defaultReadOnly bool) (mountSpec, error) {
	parts := strings.Split(spec, ":")
	m := mountSpec{readOnly: defaultReadOnly}
	if last := parts[len(parts)-1]; len(parts) > 1 && (last == "ro" || last == "rw") {
		m.readOnly = last == "ro"
		parts = parts[:len(parts)-1]
	}
	switch len(parts) {
	case 1:
	case 2:
		m.dst = parts[1]
		if !filepath.IsAbs(m.dst) {
			return mountSpec{}, fmt.Errorf("mount %q: destination must be an absolute container path", spec)
		}
	default:
		return mountSpec{}, fmt.Errorf("mount %q must be src[:dst][:ro|:rw]", spec)
	}
	m.src = parts[0]
	if m.src == "" {
		return mountSpec{}, fmt.Errorf("mount %q has an empty source", spec)
	}
	if !filepath.IsAbs(m.src) {
		if base == "" {
			return mountSpec{}, fmt.Errorf("mount %q: relative source without a hewitt.yml directory", spec)
		}
		m.src = filepath.Join(base, m.src)
	}
	m.src = filepath.Clean(m.src)
	return m, nil
}

// resolveSource returns the real path of a mount source. Symlinks below base,
// or anywhere in a source outside base, require danger: [symlinks]; symlinks in
// base itself (for example macOS /tmp) are trusted user configuration.
func (s settings) resolveSource(path, base string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve mount source: %w", err)
	}
	if real == path || s.danger["symlinks"] {
		return real, nil
	}
	if base != "" {
		if rel, err := filepath.Rel(base, path); err == nil && filepath.IsLocal(rel) {
			if baseReal, err := filepath.EvalSymlinks(base); err == nil && filepath.Join(baseReal, rel) == real {
				return real, nil
			}
		}
	}
	return "", fmt.Errorf("mount source %q follows a symlink to %q; mount the real path or add danger: [symlinks]", path, real)
}

// hostMounts resolves MOUNT lines and agent rules before any workspace exists.
func (s settings) hostMounts(adapter agent.Adapter) ([]docker.BindMount, error) {
	var mounts []docker.BindMount
	for _, m := range s.mounts {
		src, err := s.resolveSource(m.src, s.Dir)
		if err != nil {
			return nil, err
		}
		dst := m.dst
		if dst == "" {
			dst = src
		}
		mounts = append(mounts, docker.BindMount{Source: src, Target: dst, ReadOnly: m.readOnly})
	}
	if adapter == nil {
		return mounts, nil
	}
	rules, base := s.Rules, s.Dir
	if rules != "" && !filepath.IsAbs(rules) {
		rules = filepath.Join(base, rules)
	}
	if rules == "" {
		// By convention, the project keeps agent-specific rules in .hewitt/<agent>.
		base = s.repo
		rules = filepath.Join(base, ".hewitt", adapter.Name())
		if _, err := os.Lstat(rules); os.IsNotExist(err) {
			return mounts, nil
		}
	}
	real, err := s.resolveSource(filepath.Clean(rules), base)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	if !info.IsDir() {
		return append(mounts, docker.BindMount{Source: real, Target: adapter.InstructionFile(), ReadOnly: true}), nil
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	// Each entry is mounted read-only over the client's configuration directory,
	// so credentials and other profile state stay in the shared profile.
	for _, entry := range entries {
		mounts = append(mounts, docker.BindMount{Source: filepath.Join(real, entry.Name()), Target: adapter.ConfigDir() + "/" + entry.Name(), ReadOnly: true})
	}
	return mounts, nil
}

// DefaultEnvFile is used from the repository when ENV_FILE is not set.
const DefaultEnvFile = ".env.agent"

// envFile resolves the credential env file and returns the variable names it
// sets. Values are never retained; Docker reads the file itself. Bare KEY lines
// are refused because Docker would copy them from Hewitt's own environment.
func (s settings) envFile() (string, []string, error) {
	path, base := s.EnvFile, s.Dir
	if path == "" {
		base = s.repo
		path = filepath.Join(base, DefaultEnvFile)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return "", nil, nil
		}
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	real, err := s.resolveSource(filepath.Clean(path), base)
	if err != nil {
		return "", nil, fmt.Errorf("env file: %w", err)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", nil, fmt.Errorf("read env file: %w", err)
	}
	var keys []string
	for number, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.ContainsFunc(key, func(r rune) bool {
			return !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			// The line is not echoed: it may contain a secret.
			return "", nil, fmt.Errorf("env file %q line %d must be KEY=VALUE", real, number+1)
		}
		keys = append(keys, key)
	}
	return real, keys, nil
}

// directMounts resolves direct workspace paths relative to the repository.
func (s settings) directMounts() ([]docker.BindMount, string, error) {
	root, err := filepath.EvalSymlinks(s.repo)
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace: %w", err)
	}
	specs := s.WorkspaceArgs
	if len(specs) == 0 {
		specs = []string{"."}
	}
	var mounts []docker.BindMount
	for _, spec := range specs {
		m, err := parseMount(spec, root, false)
		if err != nil {
			return nil, "", err
		}
		if m.dst != "" {
			return nil, "", fmt.Errorf("direct workspace path %q must not set a destination", spec)
		}
		if rel, err := filepath.Rel(root, m.src); err != nil || !filepath.IsLocal(rel) && rel != "." {
			return nil, "", fmt.Errorf("direct workspace path %q must stay inside %q", spec, root)
		}
		src, err := s.resolveSource(m.src, root)
		if err != nil {
			return nil, "", err
		}
		mounts = append(mounts, docker.BindMount{Source: src, Target: src, ReadOnly: m.readOnly})
	}
	return mounts, root, nil
}
