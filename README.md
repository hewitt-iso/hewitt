# Hewitt

Hewitt is a local-first CLI for running AI coding agents in isolated Docker
containers, each with its own Git worktree and persistent terminal session.
The goal is to run multiple agents independently, detach and reconnect, inspect
their changes, and manage their environments from one CLI.

**Status:** early development. `login` uses the native Claude Code or Codex
subscription login flow with a dedicated persistent profile. `run` starts the
native agent in a persistent tmux terminal inside Docker; `--detach` starts it in
the background, and `attach` reconnects. `ps` lists saved sessions without Docker.
Each run creates a Git worktree on its own branch. The remaining session
commands are not implemented yet.

## Requirements

- Linux or macOS
- Go 1.26 or newer
- Git on PATH to create and manage session worktrees
- Make and a C compiler for `make check` (Go's race detector requires CGO)

Docker and agent accounts are not needed to build or run unit tests. Container
startup requires the Docker CLI on PATH and access to a running Docker Engine
(Docker Desktop on macOS is one option). Hewitt uses the current Docker context
and Docker environment settings; it does not start or manage the daemon.

Claude Code and Codex use their
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

`run` and `login` automatically select these images:

- `ghcr.io/hewitt-iso/hewitt-codex:latest`
- `ghcr.io/hewitt-iso/hewitt-claude:latest`

Docker pulls a missing image on first use. Once cached, Docker uses the local
copy; use `docker pull IMAGE` to update it. Published images must be public for
users to pull them without a registry login.

To build locally instead (requires Docker and network access):

```sh
make images  # also builds the existing OpenCode and egress images
# Or build only one:
make image-codex
make image-claude
```

Each build tags the image with both its default GHCR reference and a local
alias (`hewitt-codex:local` or `hewitt-claude:local`). Hewitt uses the locally
built GHCR tag without downloading it. To select specific client releases:

```sh
make image-codex CODEX_VERSION=<version>
make image-claude CLAUDE_VERSION=<version>
```

Local builds default to Codex `latest` and Claude Code `stable`. The images use
Node.js 24 on Debian Bookworm and include Git, Bash, ripgrep, tmux 3.3, `env`,
CA certificates, and terminal definitions. The Dockerfile supports Linux amd64
and arm64 builds and execution as the host UID/GID. Each build produces the
builder's native architecture; the initial publishing workflow builds amd64
images. Apple Silicon users can build locally
for native arm64 execution or use Docker's amd64 emulation.
Codex is installed from the official [`@openai/codex` package](https://github.com/openai/codex);
Claude Code uses its official [native installer](https://code.claude.com/docs/en/setup).
Claude auto-updates are disabled; updates go through image rebuilds. No login
state is included in either image. The build context is limited to `images/`.

Log in and run:

```sh
./bin/hewitt login codex --profile personal
./bin/hewitt run codex --profile personal --repo /path/to/repo
# Detach with Ctrl-B then D, then reconnect:
./bin/hewitt ps
./bin/hewitt attach <full-session-id>

# Start without connecting a terminal:
./bin/hewitt run claude --profile personal --detach
```

`run` uses the current directory's repository by default. Use `--repo PATH`
for another checkout and `--base REF` to start at a specific commit, branch, or
tag (default `HEAD`). The repository must have a commit. Each run creates
`<data_dir>/worktrees/<random-id>` on a new `hewitt/<random-id>` branch.
Uncommitted source changes are left alone and are not copied into the worktree.
Linked source worktrees are supported; the shared Git directory is resolved.
No existing branch is reset or reused. Login does not create a worktree.

`--image` overrides the Claude/Codex default; OpenCode still requires an image.
Hewittfile `FROM` takes precedence over the default for run sessions.
`--name` optionally names the Docker container. Implemented
adapters are `claude`, `codex`, and `opencode`. Other agents fail explicitly. `--profile`
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
`no-new-privileges` is enabled. The selected profile, worktree, and shared Git
directory are mounted read/write. The worktree and Git directory use the same
absolute paths inside and outside Docker so linked-worktree references work;
the container starts in the worktree. Images need Git on PATH for Git operations.
Agents can modify shared repository refs, objects, hooks, and configuration;
worktrees isolate checkout files, not Git administration. Use repositories you
trust, without credentials in Git configuration. Hewitt adds no host credential
imports, Docker socket, published ports, host namespaces, or privileged mode.
The native client runs as the invoking host UID/GID.

The caller owns created containers. Errors during saving, startup, or terminal
initialization identify the preserved container. Saving failure prevents startup;
later failures retain metadata. Cancellation interrupts local Docker clients
without automatic deletion; Docker may already have completed the operation.
A server initialization failure may leave a running container without an agent.
Inspect failures before cleanup; `attach` never retries agent startup.
Worktrees and branches are preserved on container creation, saving, or startup
failure; errors identify their paths. There is no automatic worktree deletion.
After stopping and removing the container, inspect the checkout before using
`git -C /path/to/repo worktree remove /path/to/worktree`. Do not use `--force`
if you need to preserve local files. Branches remain for committed-work recovery.

```sh
docker ps -a --filter label=io.hewitt.managed=true
docker logs <container-id>
docker stop <container-id>
docker rm <container-id>      # Remove after stopping and inspecting
```

## Publish agent images

Maintainers can publish tested local builds to GHCR:

```sh
docker login ghcr.io -u YOUR_GITHUB_USERNAME
make images
# Run the image smoke and integration checks before publishing.
make push-images
```

Use a GitHub personal access token (classic) with `write:packages` as the login
password. Never put tokens in configuration, Dockerfiles, or source control.
`IMAGE_REGISTRY` defaults to `ghcr.io/hewitt-iso`; `IMAGE_TAG` defaults to
`latest`. Override them when building and pushing to another destination.

The **Publish agent images** GitHub Actions workflow runs every six hours
(at 00:17, 06:17, 12:17, and 18:17 UTC) and can also be started manually.
Scheduled runs install Codex's npm `latest` version and Claude Code's `latest`
release channel. Manual runs can select exact versions or Claude's `stable`
channel. Builds pull the pinned base image and disable Docker's build cache so
upstream client and OS package updates are actually downloaded.

Each run rebuilds both images, checks the CLI under an arbitrary UID/GID,
tests the container and tmux lifecycle, and publishes the tested image as
`:latest` using the workflow's `GITHUB_TOKEN`. Failed checks prevent publishing
that image, leaving the previously published tag available. Runs are serialized
to avoid overlapping uploads. Images are rebuilt even if client versions have
not changed, also refreshing base-image packages.

This automation becomes active after the workflow and its supporting files
reach the repository's default branch. GitHub schedules can be delayed; these
are periodic rebuilds rather than instant upstream-release notifications.
For the existing packages, grant this repository Actions write access in each
package's settings if it was not inherited through the source repository link.

Publishing an update does not change running containers. Users should pull the
updated image and start a new Hewitt session; existing sessions keep their
original containers:

```sh
docker pull ghcr.io/hewitt-iso/hewitt-codex:latest
docker pull ghcr.io/hewitt-iso/hewitt-claude:latest
```

For a fresh local rebuild, use
`make images IMAGE_BUILD_FLAGS="--pull --no-cache"`.

The OCI source label links images to this repository. New GHCR packages start
private; maintainers must set their visibility to public in GitHub package
settings after the first push to enable anonymous pulls.

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
and read by `ps` and `attach`, and worktrees created by `run`. It must be an absolute
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
workspace/                 Git worktree creation and safe removal
.github/workflows/ci.yml    Linux and macOS checks
AGENTS.md                  Instructions for coding agents
Makefile                   Development commands
```

Hewitt is a modular monolith. `main` owns signals and exit status; `cmd` owns flags
and output; `config` owns non-secret preferences; `docker` owns Docker subprocess
execution and isolation defaults; `session` owns metadata and coordinates
worktree creation, create/save/start, terminal initialization, and attachment lookup. Cobra provides CLI parsing;
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
| `hewitt run <agent>` | Run a native agent in a persistent terminal and a new Git worktree |
| `hewitt stop <session>` | Stop a session |
| `hewitt rm <session>` | Remove a session and safely clean up its resources |
| `hewitt logs <session>` | Read session output |
| `hewitt diff <session>` | Inspect worktree changes |

`run` and `login` select Claude/Codex images automatically. OpenCode requires
an explicit image (`--image`, or `FROM` for run sessions). `run --detach` starts
without attaching.
`ps` accepts no positional arguments. All other commands require exactly one
nonempty session identifier. `attach` validates the full ID and saved record;
identifier format and existence checks for the remaining commands will be added
with session management.

The `workspace` package creates worktrees and offers a safe removal API. Removal
checks the repository and exact checkout path, refuses modified, untracked, or
ignored files, uses Git without force, and retains the branch. It is not yet
exposed through session `rm`; metadata deletion still removes only JSON files.
Tests use temporary repositories and fake Docker executables.
