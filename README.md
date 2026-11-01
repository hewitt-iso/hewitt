# Hewitt

Hewitt is a local-first CLI for running AI coding agents in isolated Docker
containers, each with its own Git worktree and persistent terminal session.
The goal is to run multiple agents independently, detach and reconnect, inspect
their changes, and manage their environments from one CLI.

**Status:** early development. `run` creates and starts a Docker container from an
explicit image. Help, version, configuration, and command argument validation
work. Agent installation/authentication, Git worktrees, persistent terminal
sessions, and the other session commands are not implemented yet.

## Requirements

- Linux or macOS
- Go 1.26 or newer
- Git to clone the repository
- Make and a C compiler for `make check` (Go's race detector requires CGO)

Docker and agent accounts are not needed to build or run unit tests. Container
startup requires the Docker CLI on PATH and access to a running Docker Engine
(Docker Desktop on macOS is one option). Hewitt uses the current Docker context
and Docker environment settings; it does not start or manage the daemon.

The planned MVP will use Git worktrees. Claude Code and Codex
will use their official Claude/ChatGPT subscription authentication flows;
separate API credits must not be required. OpenCode is also a planned adapter.

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

## Create and start a container

Supply an image whose default entrypoint and command launch your chosen agent:

```sh
./bin/hewitt run codex --image my-codex:local --name hewitt-codex
```

`my-codex:local` is an example name for an image you provide; Hewitt does not yet
ship agent images. `--image` is required and `--name` is optional. The agent
argument accepts `claude`, `codex`, or `opencode`; it records your intent in the
invocation but does not select an executable or verify the image's contents.
Hewitt runs the image's default command. It does not install agents, supply login
state, mount your repository, or create a Git worktree yet.

The runtime uses [`docker container create`](https://docs.docker.com/reference/cli/docker/container/create/)
followed by [`docker container start`](https://docs.docker.com/reference/cli/docker/container/start/).
Docker pulls the image if it is missing. Containers get an init process, open
stdin, a TTY, and the label `io.hewitt.managed=true`. They run with all Linux
capabilities dropped and `no-new-privileges` enabled. Hewitt adds no host mounts,
credentials, published ports, host namespaces, or privileged mode. The image's
user and Docker's default network are retained.

On successful startup, stdout contains only the container ID. This means Docker
accepted the start request, not that the agent is ready or will keep running.
The container's lifetime follows the image's command; a command that finishes
immediately leaves an exited container. A TTY alone does not implement Hewitt's
planned reconnectable terminal sessions.

The caller owns created containers. If startup fails, the error identifies the
preserved container so you can inspect or retry it. Cancellation interrupts the
Docker CLI, but Docker may already have created or started the container. No
automatic deletion or local session metadata is implemented. Until Hewitt's
management commands are implemented, use Docker directly:

```sh
docker ps -a --filter label=io.hewitt.managed=true
docker logs <container-id>
docker start <container-id>   # Retry a failed start
docker stop <container-id>
docker rm <container-id>      # Remove after stopping and inspecting
```

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

`data_dir` defaults to `$HOME/.hewitt` and reserves a location for future session
data and worktrees. It must be an absolute path; `~` and environment variables
are not expanded. Omitted fields retain defaults. Loading configuration creates
no files or directories. Do not put credentials in this file.

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
its state, and removes that container and its anonymous volumes afterward. It is
excluded from normal checks and skips when `HEWITT_TEST_IMAGE` is unset.

## Structure and architecture

The layout follows [Ollama's](https://github.com/ollama/ollama) root entry point
and top-level packages, scaled to Hewitt's current scope:

```text
main.go                    Process entry point and build version
cmd/                       Cobra command construction and CLI tests
config/                    Config loading, defaults, validation, and tests
docker/                    Docker CLI runtime and lifecycle tests
.github/workflows/ci.yml    Linux and macOS checks
AGENTS.md                  Instructions for coding agents
Makefile                   Development commands
```

Hewitt is a modular monolith. `main` owns signals and exit status; `cmd` owns flags
and output; `config` owns non-secret preferences; `docker` owns Docker subprocess
execution and isolation defaults. Cobra is the only direct Go dependency. The
concrete Docker client accepts contexts and returns errors without printing them.

As features arrive, add focused packages with these boundaries:

| Component | Responsibility |
| --- | --- |
| Session management | Coordinate session lifecycle and local metadata |
| Container runtime | Extend the existing `docker` package with lifecycle operations and workspace mounts |
| Git workspaces | Create, inspect, and safely remove Git worktrees |
| Agent adapters | Agent-specific launch commands and capabilities |
| Authentication | Official login flows and credential lifecycle |

Session management will coordinate the other components without depending on
Cobra. Keep runtime and workspace operations out of command handlers. Add small
interfaces only where a real consumer needs substitution or testing. No empty
packages, daemon, server, database, or cloud service are part of this foundation.

## Session commands and planned MVP

All commands are registered with help, examples, and argument validation. `run`
currently implements container creation/startup as described above. The remaining
commands return a "not implemented yet" error and exit with status 1.

| Command | Planned capability |
| --- | --- |
| `hewitt run <agent>` | Add agent setup, worktrees, and persistent sessions to container startup |
| `hewitt ps` | List independent sessions |
| `hewitt attach <session>` | Reconnect to a persistent terminal session |
| `hewitt stop <session>` | Stop a session |
| `hewitt rm <session>` | Remove a session and safely clean up its resources |
| `hewitt logs <session>` | Read session output |
| `hewitt diff <session>` | Inspect worktree changes |

`run` requires exactly one agent name and `--image`.
`ps` accepts no positional arguments. All other commands require exactly one
nonempty session identifier; identifier format and existence checks will be
added with session management.

The next development step is Git worktree management with tests against temporary
repositories, including safeguards against removing uncommitted user work.
