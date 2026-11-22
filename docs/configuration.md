# Configuration

Per-project settings live in `hewitt.yml`; per-user settings in `config.json`.

## Project file (hewitt.yml)

`run` reads `hewitt.yml` (or `hewitt.yaml`) from `--repo`, or the file given
with `--file PATH`. Command-line flags override it. Unknown keys are errors.
Relative paths resolve against the file's directory. Every key is optional.

```yaml
agent: claude                    # claude | codex | opencode | pi | antigravity; omit for the agent picker
image: ghcr.io/hewitt-iso/hewitt:latest
profile: personal                # shared per agent; default "default"
name: my-session                 # session (volume, branch namespace) and container name
mode: new                        # new | exec | sidecar
# target: py-agent               # existing container, with mode exec or sidecar
workspace:                       # or just: workspace: copy
  type: volume                   # volume (default) | worktree | direct | copy
  base: main                     # volume and worktree
  # paths: [src, docs:ro]        # direct (path[:ro|:rw]) and copy
workdir: services/py-agent       # inside the workspace (absolute in exec mode)
git: rw                          # worktree only: rw | ro | none
ssh: true                        # forward the host ssh-agent for git push
mounts:
  - ./fixtures:/data:ro          # src[:dst][:ro|:rw], read-only by default
rules: .hewitt/claude            # default: .hewitt/<agent> if present
isolation: restricted            # shared | isolated (default) | restricted | hardened
rootfs: ro                       # ro | rw
network: agents-net              # default: a per-session network with the Hewitt gateway
runtime: runsc                   # required for hardened
limits:                          # applied only when set
  memory: 4g
  cpus: 2
  pids: 512
env_file: .env.agent             # API keys as KEY=VALUE; default .env.agent if present
egress:
  allow: [pypi.org, "*.pythonhosted.org", "otel.example.com:4318"]  # host (port 443) or host:port
  image: ghcr.io/hewitt-iso/egress:latest
danger: [host-paths]             # network | host-paths | writable-root | symlinks
telemetry:
  endpoint: https://otel.example.com:4318  # OTLP/HTTP collector; see OpenTelemetry
```

A `hewitt.yml` arrives with the repository, so settings that could expose agent
logins or host files apply only after you trust the file: `image`, `mode: exec`
or `sidecar`, `ssh`, `isolation: shared`, `network`, `runtime`, `egress.allow`,
`egress.image`, `danger`, `telemetry.endpoint`, and `mounts`, `rules`,
`env_file`, or `workspace.paths` outside the file's directory. In a terminal,
`run` shows such a file and asks; otherwise it fails until you run
`hewitt trust` (or `hewitt trust --file PATH`). Trust covers the file's current
contents; after any change, `run` asks again. Files without these settings,
such as one that selects the agent, base branch, and limits, need no trust.
Command-line flags are your own input and need none either.

Weakening an isolation level (read-write host paths, a writable root, a
non-proxy network, symlinked mount sources) is refused unless the matching
`danger` item (or `--danger`) is given. `exec` mode accepts only `agent`,
`mode`, `target`, `workdir`, `isolation: shared`, and `danger`.

## OpenTelemetry

Hewitt does not enable telemetry by default. Set `telemetry.endpoint` in
`hewitt.yml` to an OTLP/HTTP collector, such as an OpenTelemetry Collector,
Langfuse, or Phoenix, and `run` configures every agent that supports it:

```yaml
telemetry:
  endpoint: https://otel.example.com:4318   # scheme://host[:port]; no path or credentials
```

| Agent | Exports | Configured by Hewitt as | Through a restricted gateway |
|---|---|---|---|
| Claude Code | logs, metrics | `CLAUDE_CODE_ENABLE_TELEMETRY` and `OTEL_*` variables | `http://` and `https://` |
| Codex | logs, traces | `-c otel.exporter` and `otel.trace_exporter` overrides | `https://` only |
| OpenCode | logs, traces | `OTEL_EXPORTER_OTLP_ENDPOINT` | logs only, `https://` only |
| Pi | none built in | | |
| Antigravity | none built in | | |

Collector credentials are not configuration: put headers such as
`OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic <credentials>` in the env file,
which Claude Code and OpenCode read; keep that file out of Git. Codex takes
headers only from its own `config.toml`, so Hewitt sends its data without them.
Codex metrics need its `analytics_enabled` switch, which Hewitt leaves alone.
OpenCode adds spans for model calls when its own config sets
`experimental.openTelemetry: true`. Agents export as soon as they start, even
before login; Claude Code records prompt text only with `OTEL_LOG_USER_PROMPTS=1`.

The endpoint must be reachable from the container:

- `shared` and `isolated` sessions use their gateway's network, which reaches
  the outside directly; HTTPS goes through the gateway, which resolves names
  from Docker's default network. `localhost` is the container itself. Docker Desktop resolves
  `host.docker.internal` to the host; Linux does not, and a host firewall such
  as ufw usually drops traffic from `docker0`. Prefer a collector reachable by
  DNS name, or allow the bridge in the host firewall and listen on the bridge
  address (usually `172.17.0.1`), not `127.0.0.1`.
- `restricted` and `hardened` sessions reach the collector only through the
  gateway, and `run` adds the endpoint's host and port to its allowlist.
  The proxy refuses plain HTTP, and Codex and OpenCode send plain HTTP to an
  `http://` endpoint, so use `https://`; `run` warns otherwise, and warns that a
  port other than 443 is not limited to HTTPS. OpenCode's trace exporter
  ignores the proxy, so only its logs arrive. The proxy resolves the name from
  Docker's default network; names that exist only inside another Docker network
  do not work.

Codex receives its exporters as `-c` arguments from its
[manifest](agents.md)'s `telemetry` field. `exec` mode refuses `telemetry`, since the external
container's own configuration applies.

## User configuration (config.json)

The optional JSON config file lives at:

- Linux: `$XDG_CONFIG_HOME/hewitt/config.json`, or
  `$HOME/.config/hewitt/config.json` when `XDG_CONFIG_HOME` is unset.
- macOS: `$HOME/Library/Application Support/hewitt/config.json`.

Use `--config /path/to/config.json` to select a different file. A missing default
file uses defaults; a missing explicitly selected file is an error. Invalid JSON,
unknown fields, and invalid values are errors. Configuration is validated before
command execution, after argument validation. The flag works before or after the
command name. Help bypasses configuration loading for every command.

```json
{
  "data_dir": "/absolute/path/to/hewitt-data"
}
```

`data_dir` defaults to `$HOME/.hewitt` and holds session metadata written by `run`
and read by `ps` and `attach`, and worktrees created by `run`. It must be an absolute
path; `~` and environment
variables are not expanded. Omitted fields retain defaults. Loading configuration
creates no files or directories. Do not put credentials in this file.

## Session metadata

`run` writes `<data_dir>/sessions/<container-id>.json`. The full Docker container
ID is also the workspace/session ID; `run --detach` prints that ID. Foreground
run output belongs to the agent. Shared volume records use `agent: "workspace"`,
`terminal: "host"`, `workspace_key`, and `volume`. Individual foreground processes
do not create additional workspace records. Live connections are recorded privately
in `<data_dir>/runs/<run-id>.json` with a version, agent, workspace ID, and optional
host tmux socket and pane. The launching CLI holds a kernel lock for the run's
lifetime; unlocked leftovers are ignored after crashes and reboots. These run
records contain no credentials or agent input and own no container resources.
The following is a legacy worktree record:

```json
{
  "version": 1,
  "id": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "agent": "codex",
  "profile": "personal",
  "operation": "run",
  "terminal": "tmux",
  "workspace": {
    "repository": "/path/to/repo",
    "git_dir": "/path/to/repo/.git",
    "path": "/absolute/path/to/hewitt-data/worktrees/example",
    "branch": "hewitt/example"
  },
  "image": "my-codex:local",
  "name": "hewitt-codex",
  "created_at": "2026-11-01T12:00:00Z"
}
```

`workspace` records the source checkout, shared Git directory, worktree path,
and branch. It is absent for login and legacy sessions. These fields are saved
before container startup; attachment does not create another worktree.
`name` is omitted when no container name was supplied. `profile` and `operation`
identify native client invocations. `terminal: "host"` identifies a shared workspace container; `workspace_key`
records its settings fingerprint for reuse. `terminal: "tmux"` selects persistent terminal
attachment; absent terminal fields select legacy Docker attachment. Unknown
terminal values are rejected. The version remains 1 for backward compatibility. Records describe creation
intent, not live container status or agent readiness. They contain no credentials,
agent prompts, or login state. Docker operations performed outside Hewitt do not
update these records, and metadata written before a failed start remains present.

The `session` package supports saving, reading, resolving, listing, and deleting
metadata. It validates full container IDs, required fields, and format versions,
and resolves unambiguous hex ID prefixes and session names. Writes use
an atomic rename so readers see complete records; simultaneous writes to the same
ID use the last completed replacement. New data and session directories have
mode `0700`, and record files have mode `0600`; existing directory permissions are
left unchanged. Metadata access is confined beneath the data directory.

Reading or listing a missing store creates no files. Listing returns records in
ID order and reports malformed records instead of hiding them. Missing reads and
deletions return errors. Deleting metadata only removes its JSON file, never a
container or worktree. `ps` exposes metadata listing and status at the CLI; `stop`
and `rm` print the resolved full session ID to stdout.
