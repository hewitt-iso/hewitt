# Hewitt

Hewitt is a local-first CLI for running AI coding agents in isolated Docker
containers, each with its own Git worktree and persistent terminal session.
The goal is to run multiple agents independently, detach and reconnect, inspect
their changes, and manage their environments from one CLI.

**Status:** initial Go foundation. Help, version, and basic configuration loading
work. Session commands, Docker integration, agent adapters, and authentication
are not implemented yet.

## Requirements

- Linux or macOS
- Go 1.26 or newer
- Git to clone the repository
- Make and a C compiler for `make check` (Go's race detector requires CGO)

Docker and agent accounts are not needed to build or test this foundation.
The planned MVP will require Docker and Git worktrees. Claude Code and Codex
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

For a versioned build:

```sh
make build VERSION=v0.1.0
```

The default version is `dev`. Without Make, use `go build -o bin/hewitt .` or
`go run . --help`.

## Configuration

The optional JSON config file lives at:

- Linux: `$XDG_CONFIG_HOME/hewitt/config.json`, or
  `$HOME/.config/hewitt/config.json` when `XDG_CONFIG_HOME` is unset.
- macOS: `$HOME/Library/Application Support/hewitt/config.json`.

Use `--config /path/to/config.json` to select a different file. A missing default
file uses defaults; a missing explicitly selected file is an error. Invalid JSON,
unknown fields, and invalid values are errors.

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
credentials. Dependency downloads need network access on the first build.
GitHub Actions runs formatting, module consistency, vet, tests, and build checks
on Linux and macOS. See [AGENTS.md](AGENTS.md) for contribution instructions.

## Structure and architecture

The layout follows [Ollama's](https://github.com/ollama/ollama) root entry point
and top-level packages, scaled to Hewitt's current scope:

```text
main.go                    Process entry point and build version
cmd/                       Cobra command construction and CLI tests
config/                    Config loading, defaults, validation, and tests
.github/workflows/ci.yml    Linux and macOS checks
AGENTS.md                  Instructions for coding agents
Makefile                   Development commands
```

Hewitt is a modular monolith. `main` owns exit status; `cmd` owns flags and output;
`config` owns non-secret preferences. Cobra is the only direct dependency.

As features arrive, add focused packages with these boundaries:

| Component | Responsibility |
| --- | --- |
| Session management | Coordinate session lifecycle and local metadata |
| Container runtime | Docker lifecycle, mounts, and process execution |
| Git workspaces | Create, inspect, and safely remove Git worktrees |
| Agent adapters | Agent-specific launch commands and capabilities |
| Authentication | Official login flows and credential lifecycle |

Session management will coordinate the other components without depending on
Cobra. Keep runtime and workspace operations out of command handlers. Add small
interfaces only where a real consumer needs substitution or testing. No empty
packages, daemon, server, database, or cloud service are part of this foundation.

## Planned MVP

These commands are planned and are **not registered or implemented**:

| Command | Planned capability |
| --- | --- |
| `hewitt run <agent>` | Start an agent with an isolated container and worktree |
| `hewitt ps` | List independent sessions |
| `hewitt attach <session>` | Reconnect to a persistent terminal session |
| `hewitt stop <session>` | Stop a session |
| `hewitt rm <session>` | Remove a session and safely clean up its resources |
| `hewitt logs <session>` | Read session output |
| `hewitt diff <session>` | Inspect worktree changes |

The next development step is Git worktree management with tests against temporary
repositories, including safeguards against removing uncommitted user work.
