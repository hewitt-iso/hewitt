# Agents

Hewitt knows each coding agent from a manifest: a YAML file that says how to
install the agent, how to start it, log in, check status and log out, which
variables to remove from its environment, how it takes a task, and which hosts
it needs. Hewitt itself contains no agent-specific code; behavior a manifest
cannot express is one of a few named handlers.

The built-in agents are `claude`, `codex`, `opencode`, `pi`, and `antigravity`.
Their manifests are in [`agent/manifests`](../agent/manifests) and are compiled
into `hewitt`, so they work without any setup.

## Adding an agent

Put a manifest at `<data_dir>/agents/<name>.yaml` (by default
`~/.hewitt/agents/<name>.yaml`). The agent then appears in the agent menu and
works with `hewitt run <name>`, `hewitt auth login <name>`, `-p`, and
`hewitt batch`. A manifest with the name of a built-in agent replaces it, for
example to pin another version.

```yaml
version: 1
name: my-agent                 # must match the file name
binary: myagent                # executable in the tool cache
install:
  npm: my-agent-cli            # or script: https://…/install.sh
  version: 1.4.2               # default: the package's latest
env:
  MY_AGENT_HOME: "{profile}/my-agent"
unset_env: [MY_AGENT_API_KEY]  # removed unless your env file sets it
commands:
  run: [--yes]
  headless: [exec]             # optional; headless runs use run otherwise
  login: [login]               # or {slash: /login}
  status: [whoami]
  logout: [logout]
task:                          # optional; without it the agent takes no -p
  interactive: [--, "{prompt}"]
  headless: [-p, --, "{prompt}"]
config_dir: "{profile}/my-agent"
instructions: "{profile}/my-agent/AGENTS.md"
hosts: [api.example.com, "*.example.net"]
herdr: my-agent                # optional; the kind Herdr shows
```

Hewitt checks manifests when it loads them and refuses invalid ones with the
file name and the problem; nothing is skipped silently.

A manifest describes the agent; it does not change isolation. The agent runs in
the same container, with the same gateway and observer, as every other agent.
Its `hosts` are allowed only in `restricted` and `hardened` workspaces, as for
built-in agents. A workspace created before you added an agent has no profile
for it: the next `hewitt run` replaces its container once no agent is connected
to it, and keeps the volume and its commits.

## Fields

| Field | Meaning |
| --- | --- |
| `version` | Always `1` |
| `name` | Agent name: lowercase letters, digits, and hyphens, starting with a letter |
| `binary` | Executable name the installation leaves in the tool cache |
| `image` | Workspace image for this agent; default `ghcr.io/hewitt-iso/hewitt:latest` |
| `install.npm` | npm package installed into the tool cache; `scripts: false` skips its install scripts |
| `install.script` | `https://` install script run with a temporary home; `output` is the executable's path in it, `args` its arguments (`{version}` and `{tmp}` are replaced) |
| `install.version` | Package version or tag, or the script's `{version}` |
| `env` | Variables set for every launch; `{profile}` is the agent's profile directory |
| `unset_env` | Variables removed before every launch, also when the image sets them; keys from the project's env file are kept |
| `commands` | Arguments for `run`, `headless`, `login`, `status`, and `logout`; `{workdir}` is the directory the agent starts in. A `{slash: /command}` entry starts the agent's interface and types the command, for agents whose login exists only there |
| `task` | Arguments that pass a task, `{prompt}`, to an interactive and a headless run; the prompt is one argument, never part of a shell command |
| `telemetry` | Arguments added to runs when `telemetry.endpoint` is set; `{endpoint}` is the endpoint |
| `token` | A long-lived token that `login` prints and Hewitt saves: `variable` holds it. With `hosts` and `placeholder`, agents behind a gateway hold only the placeholder, and the gateway puts the token into requests to those hosts |
| `wrap` | `keyring`: run the agent in a private Secret Service whose keyring lives in the profile |
| `config_dir`, `instructions` | Where the agent reads user-level rules, and its main rules file; `.hewitt/<name>` in a project is mounted there |
| `hosts` | Provider endpoints restricted sessions allow: `host`, `*.domain`, or `host:port` |
| `herdr` | The agent kind [Herdr](integrations/herdr.md) recognizes |

Every launch also points `HOME` and the XDG directories into the agent's
profile, so the agent keeps its login and settings there.

## Images

`hewitt-agent`, the script that starts agents in a container, knows no agent
by name: it runs the command lines Hewitt passes it. Hewitt carries the script
itself, writes it to `<data_dir>/agent` under a name derived from its content,
and mounts that directory read-only at `/hewitt/agent` in every container it
starts agents in. The script therefore always matches your `hewitt`, whatever
image you use; the image needs only Git, Bash, and base64. A workspace that an
older Hewitt created runs the image's own copy, so the next `hewitt run`
replaces its container once no agent is connected to it, keeping the volume
and its commits.
