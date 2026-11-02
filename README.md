# Hewitt

Hewitt is a local-first CLI for running AI coding agents in isolated Docker
containers, each with its own Git worktree and persistent terminal session.
The goal is to run multiple agents independently, detach and reconnect, inspect
their changes, and manage their environments from one CLI.

**Status:** early development. `login` uses the native Claude Code or Codex
subscription login flow with a dedicated persistent profile. `run` starts the
native agent in a persistent tmux terminal inside Docker; `--detach` starts it in
the background, and `attach` reconnects. `ps` lists saved sessions without Docker.
Git worktrees and the remaining session commands are not implemented yet.

## Requirements

- Linux or macOS
- Go 1.26 or newer
- Git to clone the repository
- Make and a C compiler for `make check` (Go's race detector requires CGO)

Docker and agent accounts are not needed to build or run unit tests. Container
startup requires the Docker CLI on PATH and access to a running Docker Engine
(Docker Desktop on macOS is one option). Hewitt uses the current Docker context
and Docker environment settings; it does not start or manage the daemon.

The planned MVP will add Git worktrees. Claude Code and Codex use their
official Claude/ChatGPT subscription authentication flows; separate API credits
are not required. OpenCode is a planned adapter.

## Build and run

```sh
git clone https://github.com/hewitt-iso/hewitt.git
cd hewitt
go mod download
make build
./bin/hewitt --help
./bin/hewitt --version
```

With no arguments, Hewitt validates configuration and prints help. Unknown
commands and flags fail with a nonzero exit status. Help and version do not load
configuration or require any external services.

Explore command help with `hewitt run --help` or `hewitt help attach`.

For a versioned build:

```sh
make build VERSION=v0.1.0
```

The default version is `dev`. Without Make, use `go build -o bin/hewitt .` or
`go run . --help`.

## Create and start a session

Supply an image containing `tmux` (3.3 or newer), `env`, and the selected native
CLI on PATH. The image must support the host user's UID/GID and include its
terminal definitions. Hewitt does not ship agent images yet.

```sh
./bin/hewitt login codex --image my-codex:local --profile personal
./bin/hewitt run codex --image my-codex:local --profile personal
# Detach with Ctrl-B then D, then reconnect:
./bin/hewitt ps
./bin/hewitt attach <full-session-id>

# Start without connecting a terminal:
./bin/hewitt run claude --image my-claude:local --profile personal --detach
```

`--image` is required; `--name` optionally names the Docker container. Implemented
adapters are `claude` and `codex`. Other agents fail explicitly. `--profile`
defaults to `default` and selects an agent-scoped directory under
`<data_dir>/profiles`. Login uses the official native subscription flow; Codex
uses device authentication and a file credential store. No separate API credits
are required. Native credentials remain in the private profile directory; do not
use one profile concurrently or commit it. Login output is not recorded by Docker.

`run` saves metadata before startup, starts a foreground tmux server inside the
container, and launches the native agent once in a detached tmux session. It
attaches by default, reporting the session ID and reconnect command to stderr.
`--detach` (or `-d`) prints only the full session ID to stdout. Noninteractive
callers should use `--detach`; interactive attachment requires a terminal.

The container and agent continue running when a client detaches or Hewitt is
cancelled. Reconnecting restores the same screen and tmux scrollback and never
starts another agent. When the agent exits, its final screen remains available;
the container stays running until explicitly stopped. This is process persistence
across client disconnections, not recovery across container stops, Docker daemon
restarts, or host reboots. Docker logs contain server diagnostics rather than the
agent's terminal history. tmux history is held in container memory.

Containers use an init process, open stdin, a TTY, and the label
`io.hewitt.managed=true`. All Linux capabilities are dropped and
`no-new-privileges` is enabled. Only the selected profile is mounted; Hewitt adds
no repository mounts, host credential imports, Docker socket, published ports,
host namespaces, or privileged mode. The native client runs as the invoking host
UID/GID. Git worktrees are not created yet.

The caller owns created containers. Errors during saving, startup, or terminal
initialization identify the preserved container. Saving failure prevents startup;
later failures retain metadata. Cancellation interrupts local Docker clients
without automatic deletion; Docker may already have completed the operation.
A server initialization failure may leave a running container without an agent.
Inspect failures before cleanup; `attach` never retries agent startup.

```sh
docker ps -a --filter label=io.hewitt.managed=true
docker logs <container-id>
docker stop <container-id>
docker rm <container-id>      # Remove after stopping and inspecting
```

## List sessions

```sh
./bin/hewitt ps
./bin/hewitt ps --config /path/to/config.json
```

`ps` lists all saved sessions from the configured `data_dir`, ordered by session
ID. The table includes `ID` (the full container ID), `AGENT`, `IMAGE`, `NAME`
(`-` when not supplied), and `CREATED` (a UTC RFC 3339 timestamp). An empty or
missing store prints only the header and creates no files or directories.
Invalid or unreadable records fail the command without printing a partial list.

Listing does not require or query Docker. Saved records describe creation intent,
not live container status or agent readiness. Failed starts and containers removed
outside Hewitt remain listed while their metadata exists. Containers whose
metadata could not be saved do not appear.

## Attach to a session

```sh
./bin/hewitt ps
./bin/hewitt attach <full-session-id>
```

`attach` reads the saved record from the configured `data_dir` and connects to
the existing tmux session using `docker container exec --interactive --tty`.
Use the full ID printed by `run` or `ps`; names and abbreviated IDs are not
accepted. Docker handles terminal mode and resizing; Hewitt restores terminal
settings after cancellation. Attachment uses the `xterm-256color` terminal type.

Press **Ctrl-B, D** to detach and leave the agent running. Ctrl-C may interrupt
the agent. Use tmux copy mode (**Ctrl-B, [**) to inspect scrollback. Each reconnect
attaches to the same terminal, including its final screen after the agent exits.
Attachment never starts stopped containers, creates terminals, launches agents,
or changes metadata. Removed or unavailable containers produce Docker errors.

Older metadata without a `terminal` field retains direct Docker attachment and
the **Ctrl-P, Ctrl-Q** detach sequence, without tmux history. Login containers
also retain direct Docker attachment. Output goes to command-provided streams;
Docker diagnostics go to stderr.

## Configuration

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
and read by `ps` and `attach`, with space reserved for future worktrees. It must be an absolute
path; `~` and environment
variables are not expanded. Omitted fields retain defaults. Loading configuration
creates no files or directories. Do not put credentials in this file.

## Session metadata

`run` writes `<data_dir>/sessions/<container-id>.json`. The full Docker container
ID is currently also the session ID; successful `run` output remains that ID.
Each record uses this versioned format:

```json
{
  "version": 1,
  "id": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "agent": "codex",
  "profile": "personal",
  "operation": "run",
  "terminal": "tmux",
  "image": "my-codex:local",
  "name": "hewitt-codex",
  "created_at": "2026-11-01T12:00:00Z"
}
```

`name` is omitted when no container name was supplied. `profile` and `operation`
identify native client invocations. `terminal: "tmux"` selects persistent terminal
attachment; absent terminal fields select legacy Docker attachment. Unknown
terminal values are rejected. The version remains 1 for backward compatibility. Records describe creation
intent, not live container status or agent readiness. They contain no credentials,
agent prompts, or login state. Docker operations performed outside Hewitt do not
update these records, and metadata written before a failed start remains present.

The `session` package supports saving, reading, listing, and deleting metadata.
It validates full container IDs, required fields, and format versions. Writes use
an atomic rename so readers see complete records; simultaneous writes to the same
ID use the last completed replacement. New data and session directories have
mode `0700`, and record files have mode `0600`; existing directory permissions are
left unchanged. Metadata access is confined beneath the data directory.

Reading or listing a missing store creates no files. Listing returns records in
ID order and reports malformed records instead of hiding them. Missing reads and
deletions return errors. Deleting metadata only removes its JSON file, never a
container or worktree. `ps` exposes metadata listing at the CLI. Metadata deletion
is currently a Go package API; `rm` and the other session management commands
remain unimplemented.

## Development

```sh
make fmt       # Format Go source
make check     # Check formatting, go vet, and tests with the race detector
make build     # Build bin/hewitt
make test      # Run unit tests with the race detector
make tidy      # Reconcile go.mod and go.sum
make run ARGS="--help"
```

Unit tests use temporary files and do not require Docker, network access, or
credentials. Docker tests use a fake executable to check subprocess arguments,
error propagation, cancellation, and the CLI's create/start sequence. Dependency
downloads need network access on the first build.
GitHub Actions runs formatting, module consistency, vet, tests, and build checks
on Linux and macOS. See [AGENTS.md](AGENTS.md) for contribution instructions.

To opt into the real Docker lifecycle test, select a Linux test image with a
`sleep` executable and no entrypoint:

```sh
HEWITT_TEST_IMAGE=your-local-test-image go test -tags=integration ./docker -run TestContainerLifecycle -v
```

This test can pull the selected image, creates and starts one container, verifies
its state, and removes that container and its anonymous volumes afterward.
To also test persistent terminal startup, two client reconnects, and retained
output after agent exit, select an image with `tmux`, `env`, `sh`, and `sleep`:

```sh
HEWITT_TEST_IMAGE=your-local-tmux-image go test -tags=integration ./docker -run TestPersistentTerminal -v
```

Each test cleans up only its own container. It is
excluded from normal checks and skips when `HEWITT_TEST_IMAGE` is unset.

## Structure and architecture

The layout follows [Ollama's](https://github.com/ollama/ollama) root entry point
and top-level packages, scaled to Hewitt's current scope:

```text
main.go                    Process entry point and build version
cmd/                       Cobra command construction and CLI tests
config/                    Config loading, defaults, validation, and tests
docker/                    Docker CLI runtime and lifecycle tests
session/                   Local metadata, native profiles, startup, and attachment
agent/                     Native Claude Code and Codex invocation descriptions
profile/                   Private persistent native client directories
.github/workflows/ci.yml    Linux and macOS checks
AGENTS.md                  Instructions for coding agents
Makefile                   Development commands
```

Hewitt is a modular monolith. `main` owns signals and exit status; `cmd` owns flags
and output; `config` owns non-secret preferences; `docker` owns Docker subprocess
execution and isolation defaults; `session` owns metadata and coordinates
create/save/start, terminal initialization, and attachment lookup. Cobra provides CLI parsing;
`golang.org/x/term` provides terminal restoration after attachment cancellation.
The foreground server and reconnect behavior follow the [tmux manual](https://github.com/tmux/tmux/blob/master/tmux.1).
The concrete Docker client accepts contexts and returns errors. Attachment
streams container output and Docker diagnostics to the supplied writers.

Extend focused packages with these boundaries as features arrive:

| Component | Responsibility |
| --- | --- |
| Session management | Extend the existing `session` package with lifecycle coordination |
| Container runtime | Extend the existing `docker` package with lifecycle operations and workspace mounts |
| Git workspaces | Create, inspect, and safely remove Git worktrees |
| Agent adapters | Extend `agent` with native launch commands and capabilities |
| Authentication | Native login flows with private directories managed by `profile` |

Session management coordinates the other components without depending on
Cobra. Keep runtime and workspace operations out of command handlers. Add small
interfaces only where a real consumer needs substitution or testing. No empty
packages, daemon, server, database, or cloud service are part of this foundation.

## Session commands and planned MVP

All commands are registered with help, examples, and argument validation. `run`
implements container creation/startup, and `ps` lists saved sessions as described
above; `attach` connects to a running container terminal. The remaining commands
return a "not implemented yet" error and exit with status 1.

| Command | Planned capability |
| --- | --- |
| `hewitt run <agent>` | Run a native agent in a persistent terminal; add worktrees later |
| `hewitt stop <session>` | Stop a session |
| `hewitt rm <session>` | Remove a session and safely clean up its resources |
| `hewitt logs <session>` | Read session output |
| `hewitt diff <session>` | Inspect worktree changes |

`run` and `login` require exactly one agent name and `--image`. `run --detach`
starts without attaching.
`ps` accepts no positional arguments. All other commands require exactly one
nonempty session identifier. `attach` validates the full ID and saved record;
identifier format and existence checks for the remaining commands will be added
with session management.

The next development step is Git worktree management with tests against temporary
repositories, including safeguards against removing uncommitted user work.
