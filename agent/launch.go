package agent

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Operations a manifest describes.
const (
	OpRun    = "run"
	OpLogin  = "login"
	OpStatus = "status"
	OpLogout = "logout"
)

// LaunchOptions select one launch of a client.
type LaunchOptions struct {
	// Profile is the container path of the agent's profile. Empty omits the
	// profile environment, for containers Hewitt does not own.
	Profile  string
	Prompt   string
	Headless bool
	// Telemetry is the OTLP endpoint of a run, if any.
	Telemetry string
	// Workdir replaces {workdir}. Empty leaves a marker that hewitt-agent
	// replaces with the directory it starts the client in.
	Workdir string
	// Allow keeps variables that UnsetEnv would remove, such as keys from the
	// user's env file or a saved token.
	Allow []string
}

// Launch is the command line of one launch. Env carries it to hewitt-agent,
// which runs Argv, or types Slash into the interface Argv starts.
type Launch struct {
	Argv  []string
	Slash string
	// Marker is replaced by the client's working directory in the container.
	Marker string
}

// keyringScript starts agy-style clients in a private Secret Service whose
// keyring file lives in the profile's XDG_DATA_HOME, which protects it; the
// fixed password only lets the daemon create and unlock it without a prompt.
const keyringScript = `mkdir -p "${XDG_DATA_HOME:-$HOME/.local/share}/keyrings"; printf hewitt | gnome-keyring-daemon --replace --unlock --components=secrets >/dev/null 2>&1; exec "$@"`

// Launch builds the command line of operation op. Arguments are passed
// directly; nothing is interpolated into a shell command.
func (m Manifest) Launch(op string, o LaunchOptions) (Launch, error) {
	var cmd Command
	switch op {
	case OpRun:
		cmd.Args = m.Commands.Run
		if o.Headless && m.Commands.Headless != nil {
			cmd.Args = m.Commands.Headless
		}
	case OpLogin:
		cmd = m.Commands.Login
	case OpStatus:
		cmd = m.Commands.Status
	case OpLogout:
		cmd = m.Commands.Logout
	default:
		return Launch{}, fmt.Errorf("unknown agent operation %q", op)
	}
	if o.Prompt != "" && op != OpRun {
		return Launch{}, fmt.Errorf("only runs take a task")
	}
	if o.Prompt != "" && m.Task == nil {
		return Launch{}, fmt.Errorf("%s does not accept a task; start it without -p", m.Name)
	}
	if o.Headless && o.Prompt == "" {
		return Launch{}, fmt.Errorf("a headless run needs a task")
	}
	if strings.ContainsRune(o.Prompt, 0) {
		return Launch{}, fmt.Errorf("task contains a NUL byte")
	}
	l := Launch{Marker: o.Workdir}
	if l.Marker == "" {
		l.Marker = "{workdir:" + strings.ToLower(rand.Text()) + "}"
	}
	argv := []string{"env"}
	for _, key := range m.UnsetEnv {
		if !slices.Contains(o.Allow, key) {
			argv = append(argv, "-u", key)
		}
	}
	if o.Profile != "" {
		argv = append(argv, ProfileEnv(o.Profile)...)
		for _, key := range slices.Sorted(maps.Keys(m.Env)) {
			argv = append(argv, key+"="+Profile(m.Env[key], o.Profile))
		}
	} else {
		// Without a profile, only settings that do not name it apply.
		for _, key := range slices.Sorted(maps.Keys(m.Env)) {
			if !strings.Contains(m.Env[key], "{profile}") {
				argv = append(argv, key+"="+m.Env[key])
			}
		}
	}
	if m.Wrap == "keyring" {
		argv = append(argv, "dbus-run-session", "--", "bash", "-c", keyringScript, "keyring")
	}
	argv = append(argv, m.Binary)
	for _, arg := range cmd.Args {
		argv = append(argv, strings.ReplaceAll(arg, "{workdir}", l.Marker))
	}
	if op == OpRun && o.Telemetry != "" {
		for _, arg := range m.Telemetry {
			argv = append(argv, strings.ReplaceAll(arg, "{endpoint}", o.Telemetry))
		}
	}
	if o.Prompt != "" {
		task := m.Task.Interactive
		if o.Headless {
			task = m.Task.Headless
		}
		for _, arg := range task {
			argv = append(argv, strings.ReplaceAll(arg, "{prompt}", o.Prompt))
		}
	}
	l.Argv, l.Slash = argv, cmd.Slash
	return l, nil
}

// ProfileEnv points a client's home and XDG directories into the profile at
// root, so its state persists there.
func ProfileEnv(root string) []string {
	return []string{
		"HOME=" + root + "/home",
		"XDG_CONFIG_HOME=" + root + "/config",
		"XDG_DATA_HOME=" + root + "/data",
		"XDG_STATE_HOME=" + root + "/state",
		"XDG_CACHE_HOME=" + root + "/cache",
	}
}

// Env is the environment that hands l to hewitt-agent: the command line as
// NUL-terminated arguments in base64, since environment entries cannot hold
// arbitrary bytes, the slash command, and the working directory marker.
func (l Launch) Env() []string {
	env := []string{"HEWITT_LAUNCH=" + encode(l.Argv)}
	if strings.HasPrefix(l.Marker, "{workdir:") {
		env = append(env, "HEWITT_WORKDIR_MARK="+l.Marker)
	}
	if l.Slash != "" {
		env = append(env, "HEWITT_SLASH="+l.Slash)
	}
	return env
}

// Pick is the environment of hewitt-agent's own agent menu: the names and one
// launch per agent, keyed by the agent's name.
func Pick(launches map[string]Launch, names []string) []string {
	env := []string{"HEWITT_AGENTS=" + strings.Join(names, " ")}
	for _, name := range names {
		l, ok := launches[name]
		if !ok {
			continue
		}
		env = append(env, PickVariable(name)+"="+encode(l.Argv))
		if strings.HasPrefix(l.Marker, "{workdir:") {
			env = append(env, PickVariable(name)+"_MARK="+l.Marker)
		}
	}
	return env
}

// PickVariable names the menu launch of one agent.
func PickVariable(name string) string {
	return "HEWITT_LAUNCH_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// InstallEnv hands the installation of m to hewitt-agent install.
func (m Manifest) InstallEnv() []string {
	i := m.Install
	var args []string
	if i.NPM != "" {
		pkg := i.NPM
		if i.Version != "" {
			pkg += "@" + i.Version
		}
		args = []string{"npm", m.Binary, pkg}
		if i.Scripts != nil && !*i.Scripts {
			args = append(args, "--ignore-scripts")
		}
	} else {
		args = []string{"script", m.Binary, i.Script, i.Output}
		for _, arg := range i.Args {
			args = append(args, strings.ReplaceAll(arg, "{version}", i.Version))
		}
	}
	return []string{"HEWITT_INSTALL=" + encode(args)}
}

func encode(args []string) string {
	var b strings.Builder
	for _, arg := range args {
		b.WriteString(arg)
		b.WriteByte(0)
	}
	return base64.StdEncoding.EncodeToString([]byte(b.String()))
}

// ImageFor returns the manifest's image or the default workspace image.
func (m Manifest) ImageFor() string {
	if m.Image != "" {
		return m.Image
	}
	return DefaultImage
}

// ConfigPath is ConfigDir in the profile at root; InstructionPath likewise.
func (m Manifest) ConfigPath(root string) string      { return Profile(m.ConfigDir, root) }
func (m Manifest) InstructionPath(root string) string { return Profile(m.Instructions, root) }
