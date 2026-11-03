# Hewitt

Hewitt is a local-first CLI for running AI coding agents in isolated Docker
containers, each with its own Git repository and persistent terminal session.
The goal is to run multiple agents independently, detach and reconnect, inspect
their changes, and manage their environments from one CLI.

**Status:** early development. `login` uses the native Claude Code or Codex
subscription login flow with a dedicated persistent profile. `run` starts a
workspace container with a persistent tmux terminal inside Docker; `--detach` starts it in
the background, and `attach` reconnects. `ps` lists saved sessions and their
status. `stop` and `rm` manage session lifecycles and workspace cleanup.
Each session keeps its repository in a Docker volume; `sync` imports its
branches into the host repository. `logs` is not implemented yet.

## Requirements

- Linux or macOS
- Go 1.26 or newer
- Git on PATH to resolve repositories and import session branches
- Make and a C compiler for `make check` (Go's race detector requires CGO)

Docker and agent accounts are not needed to build or run unit tests. Container
startup requires the Docker CLI on PATH and access to a running Docker Engine
(Docker Desktop on macOS is one option). Hewitt uses the current Docker context
and Docker environment settings; it does not start or manage the daemon.

Claude Code and Codex use their
official Claude/ChatGPT subscription authentication flows; separate API credits
are not required. OpenCode, Pi, and Antigravity use their own native logins.

## Build and run

```sh
git clone https://github.com/hewitt-iso/hewitt.git
cd hewitt
go mod download
make build
./bin/hewitt --help
./bin/hewitt --version
```

With no arguments, Hewitt validates configuration and prints help (or runs a
3-step interactive onboarding prompt on the first terminal run to optionally
import host skills/MCP/tmux configs, choose whether to start a workspace, and
share or log in to agent credentials). Unknown
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

`run` and `login` automatically select the unified workspace image:

- `ghcr.io/hewitt-iso/hewitt:latest`

Docker pulls a missing image on first use. Once cached, Docker uses the local
copy; use `docker pull IMAGE` to update it. Published images must be public for
users to pull them without a registry login.

To build locally instead (requires Docker and network access):

```sh
make images  # builds hewitt:local and hewitt/egress:local
# Or build only the workspace image:
make image-hewitt
```

Agents (`claude`, `codex`, `opencode`, `pi`, `antigravity`) are installed on demand into `<data_dir>/tools`
(mounted at `/hewitt/tools`) the first time they are selected, so the base image
stays small and installed CLIs persist across containers. Every new tmux window
(`Ctrl-B, C`) prompts you to choose an agent (creating a fresh worktree and
branch `<branch>-2`, `-3`, … for it) or open a shell in the current project directory.

Log in and run:

```sh
./bin/hewitt login codex --profile personal
./bin/hewitt run --profile personal --repo /path/to/repo
# Or specify the initial agent directly:
./bin/hewitt run codex --profile personal --repo /path/to/repo
# Detach with Ctrl-D, then reconnect:
./bin/hewitt ps
./bin/hewitt attach <full-session-id>

# Start without connecting a terminal:
./bin/hewitt run claude --profile personal --detach
```

`run` uses the current directory's repository by default. Use `--repo PATH`
for another checkout and `--base REF` to start at a specific commit, branch, or
tag (default `HEAD`). The repository must have a commit. Login does not create
a workspace.

### Volume workspace (default)

The session repository lives in the Docker volume `hewitt-<name>`, mounted at
`/hewitt/work/<project>`. Its `.git`, hooks, and configuration belong to the
container; the host `.git` is never mounted writable, so nothing an agent writes
there can run on the host. The volume borrows history from the host through a
read-only mount of `.git/objects` (a Git alternate), so creation copies nothing.

- The session name comes from `--name` (or `name` in `hewitt.yml`) or is the
  first free `<agent>-<n>`, such as `claude-1`. It names the volume and the host
  branch namespace.
- The first branch keeps the host branch name when the base is a branch (`main`),
  otherwise `work`. Uncommitted host changes are not copied.
- Commits use the host repository's `user.name` and `user.email`; `origin` is
  copied without any credentials embedded in an HTTP(S) URL.
- `refs/hewitt/<name>/base` keeps the base commit reachable, so `git gc` on the
  host cannot remove objects the volume borrows. Moving or deleting the host
  repository breaks the volume.
- `hewitt sync <session>` imports every session branch as
  `hewitt/<name>/<branch>`, overwriting earlier imports. A helper container
  exports a Git bundle and the host fetches it; a bundle carries only objects and
  refs, never hooks or configuration. `stop` syncs automatically. Fetching into a
  branch checked out on the host fails; switch away first.
- The volume survives container crashes, `stop`, and Docker restarts. `rm` syncs
  first and refuses while any worktree in the volume has uncommitted or
  untracked files; `rm --force` discards them. Imported branches are kept.

```sh
hewitt run claude --detach        # session claude-1, branch main in the volume
hewitt sync claude-1              # Imported hewitt/claude-1/main
git log hewitt/claude-1/main
hewitt stop claude-1 && hewitt rm claude-1
```

### Push over SSH

`--ssh` (or `ssh: true`) forwards the host ssh-agent socket to
`/hewitt/ssh-agent.sock`, so the agent can `git push` from its volume. Keys stay
in the host agent: the container can ask it to sign but cannot read them. Every
key loaded in the agent is usable by the session, so load only the keys it
should have (or run a separate agent). The image trusts GitHub's published host
keys (fetched over TLS from `api.github.com/meta` at build time); other hosts
are accepted on first use. Sessions run as the host UID; when the image has no
passwd entry for it, `hewitt-agent` supplies one through `nss_wrapper`, which
ssh requires.

- Linux: `SSH_AUTH_SOCK` must point to a socket when the session starts.
  Restarting the host agent replaces the socket; start a new session afterwards.
- macOS (Docker Desktop): Docker Desktop's forwarded socket
  `/run/host-services/ssh-auth.sock` is used. It is root-owned in some Docker
  Desktop versions, so a non-root session may get "permission denied".
- Restricted and hardened sessions refuse `--ssh`: their egress proxy carries
  only HTTP(S).

### Worktree workspace (legacy)

`--workspace worktree` creates `<data_dir>/worktrees/<random-id>` on a
`hewitt/<random-id>` branch and mounts the host's shared `.git` read/write. Host
Git runs hooks and command settings (such as `core.fsmonitor`) from that
directory, so an agent can make the host run code. It therefore requires
`--danger host-paths` (or `git: ro`) at the default isolation level.

`--image` overrides the default image; `image` in `hewitt.yml` takes precedence
over the default for run sessions.
`--name` optionally names the Docker container. Implemented
adapters are `claude`, `codex`, `opencode`, `pi`, and `antigravity`. Other agents fail explicitly. `--profile`
defaults to `default` and selects an agent-scoped directory under
`<data_dir>/profiles`. Login uses the official native subscription flow; Codex
uses device authentication and a file credential store. No separate API credits
are required. Native credentials remain in the private profile directory; do not
commit it. Login output is not recorded by Docker.

Sessions may share a profile. Codex refreshes its own tokens in the shared
`auth.json` and reloads it when another session has already refreshed; if two
sessions refresh at the same moment, one may need `hewitt login codex` again.

Pi (`@earendil-works/pi-coding-agent`) logs in only through its `/login`
command, so `hewitt login pi` starts Pi and types `/login` for you; pick a
provider and paste the code it shows. `hewitt auth status pi` lists the models
of configured providers, and `hewitt auth logout pi` types `/logout`. Pi's
state, including `auth.json` (0600), lives in the profile as
`PI_CODING_AGENT_DIR`.

Antigravity (`agy`) stores its login in the Secret Service keyring. Hewitt runs
it inside a private D-Bus session with `gnome-keyring`, whose keyring file
lives in the profile (`data/keyrings`), so the login persists across
containers. `hewitt login antigravity` opens agy's sign-in menu; choose Google
OAuth and paste the code. API keys are not supported by agy.

Images set `LANG=C.UTF-8` and terminals attach with `tmux -u`, so non-ASCII
input such as Cyrillic works.

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
`no-new-privileges` is enabled. The selected profile and the session volume are
mounted read/write and the host object directory read-only; the container starts
in the volume repository. Images need Git and `sh` on PATH: Hewitt runs volume
helpers (initialize, export, inspect) in short-lived, network-less containers of
the session image, and once as root with only `CAP_CHOWN` to give a new volume
to the invoking user. Hewitt adds no host credential imports, Docker socket,
published ports, host namespaces, or privileged mode. The native client runs as
the invoking host UID/GID.

The caller owns created containers. Errors during saving, startup, or terminal
initialization identify the preserved container. Saving failure prevents startup;
later failures retain metadata. Cancellation interrupts local Docker clients
without automatic deletion; Docker may already have completed the operation.
A server initialization failure may leave a running container without an agent.
Inspect failures before cleanup; `attach` never retries agent startup.
Volumes, worktrees, and branches are preserved on container creation, saving, or
startup failure; errors identify them. Legacy worktrees are removed by `rm`,
which refuses uncommitted, untracked, or ignored files. Branches remain for
committed-work recovery.

```sh
docker ps -a --filter label=io.hewitt.managed=true
docker logs <container-id>
docker stop <container-id>
docker rm <container-id>      # Remove after stopping and inspecting
```

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
network: agents-net              # restricted/hardened default: Hewitt egress proxy
runtime: runsc                   # required for hardened
limits:                          # applied only when set
  memory: 4g
  cpus: 2
  pids: 512
env_file: .env.agent             # API keys as KEY=VALUE; default .env.agent if present
egress:
  allow: [pypi.org, "*.pythonhosted.org"]
  image: hewitt/egress:local
danger: [host-paths]             # network | host-paths | writable-root | symlinks
```

Weakening an isolation level (read-write host paths, a writable root, a
non-proxy network, symlinked mount sources) is refused unless the matching
`danger` item (or `--danger`) is given. `exec` mode accepts only `agent`,
`mode`, `target`, `workdir`, `isolation: shared`, and `danger`.

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
./bin/hewitt ps -a
./bin/hewitt ps --no-trunc
./bin/hewitt ps --config /path/to/config.json
```

`ps` lists saved sessions from the configured `data_dir`, ordered by session
ID. By default, stopped sessions are omitted; pass `-a` (`--all`) to include
stopped sessions. The table includes `ID` (12-character short ID by default, or
the full 64-character ID with `--no-trunc`), `AGENT`,
`IMAGE`, `NAME` (`-` when not supplied), `STATUS` (`running`, `stopped`, or
`unknown`), and `CREATED` (a UTC RFC 3339 timestamp). An empty or missing store
prints only the header and creates no files or directories. Invalid or
unreadable records fail the command without printing a partial list.

Listing does not require Docker; when Docker is unavailable or cannot be
queried, `STATUS` is reported as `unknown` and all saved sessions are shown.
Failed starts and containers removed outside Hewitt appear as `stopped` under
`ps -a` while their metadata exists. Containers whose metadata could not be
saved do not appear.

## Attach to a session

```sh
./bin/hewitt ps
./bin/hewitt attach <session>
./bin/hewitt attach              # interactive session picker on a terminal
```

`attach` resolves the session from the configured `data_dir` by full ID,
unambiguous hex ID prefix, or session name (or opens an interactive selector
when `<session>` is omitted on an interactive terminal), and connects to the existing tmux
session using `docker container exec --interactive --tty`. Docker handles
terminal mode and resizing; Hewitt restores terminal settings after
cancellation. Attachment uses the `xterm-256color` terminal type.

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
implements container creation/startup, `ps` lists saved sessions and their
status, `attach` connects to a running container terminal, `stop` stops a
session's agent, and `rm` removes a stopped session and all its resources.
`logs` returns a "not implemented yet" error and exits with status 1.

| Command | Capability |
| --- | --- |
| `hewitt run [agent]` | Run a workspace container with a persistent terminal and a Git repository in a volume |
| `hewitt sync [session]` | Import a session's branches as `hewitt/<name>/<branch>` |
| `hewitt stop [session]` | Stop a session and print its full ID |
| `hewitt rm [session]` | Sync, then remove a stopped session and its volume/resources (`--force` discards uncommitted work), and print its full ID |
| `hewitt logs <session>` | Read session output (not implemented yet) |

`run` and `login` select the `ghcr.io/hewitt-iso/hewitt:latest` image automatically.
`run --detach` starts without attaching.
`ps` accepts no positional arguments. Session commands (`attach`, `stop`, `rm`)
accept a full 64-character hex ID, an unambiguous hex ID prefix, or a
session name, and support shell tab completion (`hewitt completion`). When
`<session>` is omitted on an interactive terminal, they open an interactive
session picker.
