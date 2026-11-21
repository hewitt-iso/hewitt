# Using Hewitt

How to start agents, work in shared workspaces, return to running agents, and take results back to your repository. For installation, see the [README](../README.md).

## Starting Hewitt

```sh
git clone https://github.com/hewitt-iso/hewitt.git
cd hewitt
go mod download
make build
./bin/hewitt --help
./bin/hewitt --version
```

In a terminal, `hewitt` opens a selector for Claude, Codex, OpenCode, Pi,
Antigravity, or a project shell. Select with arrow keys or a number, then Enter;
press Q or Ctrl-C to cancel before workspace setup starts. Open `hewitt` in
another host tmux window to choose another agent in the same workspace container.
First-time users complete the optional 3-step onboarding flow first.

Onboarding can import host settings (Claude, Codex, and OpenCode rules,
skills, MCP servers, and settings; tmux; git and bash defaults) into the agent
profiles, and `run` refreshes them before each start. Agents can read and send
anything in their profiles, so secret values are removed from the copies by
default: MCP `env` and `headers`, provider `apiKey` entries, `bearer_token`,
secret-looking shell exports and git settings, URL credentials, secret command
flags, and well-known token formats. References such as `${GITHUB_TOKEN}` stay.
Onboarding lists what it removed and asks whether to keep the values instead.
Files that usually are credentials (`.env*` other than examples, `*.pem`,
`*.key`, `credentials`, `auth.json`, SSH private keys) are never copied from
imported directories. Copying host logins into profiles is a separate,
explicit choice; a `hewitt login` in the container is recommended instead.

After selection, progress messages show configuration sync, workspace lookup or
creation, profile and tool cache preparation, repository initialization, and
container startup. Existing workspaces report reuse or restart. The container
then reports agent installation checks, profile setup, and launch. First-time
image pulls and agent installation can take longer; the current step stays visible.
Progress goes to stderr; stdout belongs to the selected agent (or the workspace
ID with `--detach`). New container progress messages require a rebuilt image.

`hewitt run` without an agent also opens the selector in a terminal, even when
`agent` is configured in `hewitt.yml`; `hewitt run codex` launches directly.
`--detach` skips the selector. Without an interactive terminal, bare `hewitt`
prints help. Unknown commands and flags fail with a nonzero exit status.
Help and version do not load configuration or require external services.

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

Docker pulls a missing image on first use. For the default image, each `run`
and `login` also checks the registry for a newer `latest` and pulls it; offline,
Hewitt warns and keeps the local copy. A locally built image with the same tag
(no registry digest) and any custom `--image` are never pulled over. An idle
workspace whose container was created from an older image gets a new container
on the next `run`; its volume, branches, and commits are kept. A workspace with
connected agents keeps its container until they exit. Published images must be
public for users to pull them without a registry login.

To build locally instead (requires Docker and network access):

```sh
make images  # builds hewitt:local and hewitt-egress:local
# Or build only the workspace image:
make image-hewitt
```

Agents (`claude`, `codex`, `opencode`, `pi`, `antigravity`) are installed on demand
into the shared tool cache `<data_dir>/tools`. Agent and login containers mount
it read-only at `/hewitt/tools`, so a session cannot replace executables that
later sessions of any project run. Before launching an agent that is missing
from the cache, `run`, `login`, and `auth` install it in a short-lived installer
container that mounts only the tool cache: no project, profiles, env file, or
credentials. It uses Docker's default network, also for restricted and hardened
sessions, and is removed when it exits. Installations are serialized by a lock
held only while one runs; agents already in the cache start in parallel without
it. The installer moves each executable into place last, so running agents keep
their binary and later runs use the new one. The workspace container keeps
running during installation. An agent chosen inside a container, such as from
a legacy tmux window's picker, must already be in the cache; otherwise
`hewitt-agent` names the `hewitt run <agent>` command that installs it. Images
without `hewitt-agent install` cannot install agents; the default image is
refreshed automatically.

Start each agent in its own host terminal tab or tmux window:

```sh
hewitt                    # select Codex in host window 1
hewitt                    # select Claude in host window 2; same container
hewitt run codex          # host window 3: same container, branch codex-2
hewitt shell              # another connection: shell in the base checkout
```

### Project dependencies

The workspace image includes Node.js/npm, Go 1.26, Python 3 with pip and venv,
Make, GCC/G++, and pkg-config. Native npm modules can compile through node-gyp.
Project dependencies are installed inside each agent's worktree, for example
`npm ci`, `python3 -m venv .venv` followed by `.venv/bin/pip install -r requirements.txt`,
or `go mod download`. Host dependencies and uncommitted files are not copied.
These tools require a rebuilt workspace image.

Projects needing other runtimes, OS libraries, or specific tool versions should
extend the workspace image and select it with `image` in `hewitt.yml` or
`--image`. For example:

```dockerfile
FROM ghcr.io/hewitt-iso/hewitt:latest
USER root
RUN apt-get update && apt-get install -y --no-install-recommends libpq-dev \
    && rm -rf /var/lib/apt/lists/*
USER 1000:1000
```

```sh
docker build -t my-project:hewitt -f Dockerfile.hewitt .
hewitt run codex --image my-project:hewitt
```

Hewitt does not infer or install every project's system dependencies. Restricted
and hardened workspaces also need dependency registry hosts in `egress.allow`.
Keep image builds and package installs free of credentials.

Codex runs with `--sandbox danger-full-access` inside the container: Docker
provides the sandbox, retaining dropped capabilities and `no-new-privileges`.
This avoids a nested namespace sandbox that cannot start under these restrictions.
Codex's approval policy still applies. Processes can access the workspace,
profiles, and mounts granted to that container; use separate workspaces for
separate security boundaries.

Use your host tmux's prefix and detach keys as usual. `exit` or Ctrl-D closes
a shell; Ctrl-C cancels its current command. Exiting one process leaves the
workspace container and other agents running. Outside tmux, `run` warns that the
agent cannot be reattached. Without host tmux, a lost terminal
connection cannot be reattached by Hewitt; use native agent conversation resume
where supported. `hewitt attach` can return to individual agent panes when their
launching host tmux connection still exists. Old host worktrees are preserved.

The image retains tmux for legacy sessions and explicit exec/sidecar and
worktree/direct/copy modes. Its palette forwarding tests still cover those modes.

When host config sync is enabled, OpenCode imports include `tui.json`,
`tui.jsonc`, and custom `themes/`, preserving theme and keybinding preferences.
Imported tmux styles and keybindings apply to legacy container terminals. Terminal fonts remain
controlled by the host terminal emulator. CLI binaries use the persistent
Hewitt tools cache; their versions are not automatically matched to host installs.

Log in and run:

```sh
./bin/hewitt login codex --profile personal
./bin/hewitt run --profile personal --repo /path/to/repo
# Or specify the initial agent directly:
./bin/hewitt run codex --profile personal --repo /path/to/repo
# Open another host terminal or tmux window; reuse the same workspace:
./bin/hewitt run claude --profile personal --repo /path/to/repo
./bin/hewitt shell --profile personal --repo /path/to/repo
./bin/hewitt ps

# Prepare a workspace container without launching an agent:
./bin/hewitt run --profile personal --detach
```

`run` uses the current directory's repository by default. Use `--repo PATH`
for another checkout and `--base REF` to start at a specific commit, branch, or
tag (default `HEAD`). The repository must have a commit. Login does not create
a workspace.

Docker availability is checked before the workspace name is anchored in Git,
so a failed daemon connection can be retried with the same name. Failures after
volume creation preserve the volume for inspection.

### Volume workspace (default)

The session repository lives in the Docker volume `hewitt-<name>`, mounted at
`/hewitt/work/<project>`. Its `.git`, hooks, and configuration belong to the
container; the host `.git` is never mounted writable, so nothing an agent writes
there can run on the host. The volume borrows history from the host through a
read-only mount of `.git/objects` (a Git alternate), so creation copies nothing.

- The workspace name comes from `--name` (or `name` in `hewitt.yml`) or is the
  first free `workspace-<n>`. It names the volume and host branch namespace.
- Matching repository, profile, image, name, base and isolation settings reuse
  the container, restarting it if stopped. Changing settings creates a separate
  workspace (use a different name when the old container has an explicit name).
  `--separate` always creates another workspace without replacing the
  default one. Use a distinct `--name` to select a named workspace repeatedly.
- The base checkout keeps the host branch name (`main`), otherwise `work`.
  Each agent connection creates a branch such as `codex-1` or `claude-1` and
  its own worktree inside the volume. Shells open the base checkout.
  Uncommitted host changes are not copied.
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
hewitt run claude                 # workspace-1; commit changes on claude-1, then exit
hewitt sync workspace-1           # imports hewitt/workspace-1/claude-1
git log hewitt/workspace-1/claude-1
hewitt stop workspace-1           # stops ALL processes in this workspace
hewitt rm workspace-1
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
- Restricted and hardened sessions refuse `--ssh`: their gateway carries
  only tunnels to allowed hosts.

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
the full 64-character ID with `--no-trunc`), `AGENT` (for shared workspaces,
the connected agents, or `-`),
`IMAGE`, `NAME` (`-` when not supplied), `STATUS` (`running`, `idle` for a
running shared workspace with no connected agent, `stopped`, or `unknown`), and `CREATED` (a UTC RFC 3339 timestamp). An empty or missing store
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

For new launches, `hewitt attach` lists individual live agent runs across shared
workspace containers, including the agent name, workspace, and run ID.

```sh
hewitt attach                    # choose a running agent or shell
hewitt attach <agent-run-id>     # full ID or unambiguous prefix
hewitt attach workspace-1        # choose a run within this workspace
```

Inside the same host tmux server, selection switches to the existing agent pane.
Outside tmux, it attaches to that pane's existing host session. Another tmux
server is refused to avoid changing an unrelated client's terminal. No pane,
agent, or container is created by attachment. Runs opened in plain terminals
are shown with a limitation label; selecting one explains that raw Docker exec
terminals cannot be reattached. Use host tmux for reconnectable launches. When
nothing can be attached, the error names idle workspaces and how to open an agent.

## Errors

Errors print as `Error: ...`, followed by `Hint: ...` when Hewitt knows what to do
next, for example a mistyped command, an unreachable Docker daemon, a session
name that does not exist, or an agent that exited.

Tracking begins with new launches of this CLI; already-running untracked exec
processes cannot be discovered and reattached retroactively. Connections disappear
from the picker when their launching CLI exits. A crashed CLI's unlocked record
is ignored. The workspace and its data are unaffected. `ps` still lists workspace
containers and legacy sessions rather than individual connections.

The following attachment behavior applies to legacy and explicit container
tmux modes only; their terminals also appear in the picker.

`attach` resolves the session from the configured `data_dir` by full ID,
unambiguous hex ID prefix, or session name (or opens an interactive selector
when `<session>` is omitted on an interactive terminal), and connects to the existing tmux
session using `docker container exec --interactive --tty`. Docker handles
terminal mode and resizing; Hewitt restores terminal settings after
cancellation. Attachment preserves the host `TERM` (falling back to
`xterm-256color` when unset or `dumb`); panes use `tmux-256color`. Custom
images must provide the corresponding terminfo entries.

Press **Ctrl-B, D** to detach and leave the agent running. Ctrl-C may interrupt
the agent; Ctrl-D is also passed through to the CLI. Use tmux copy mode (**Ctrl-B, [**) to inspect scrollback. Each reconnect
attaches to the same terminal, including its final screen after the agent exits.
Attachment never starts stopped containers, creates terminals, launches agents,
or changes metadata. Removed or unavailable containers produce Docker errors.

Older metadata without a `terminal` field retains direct Docker attachment and
the **Ctrl-P, Ctrl-Q** detach sequence, without tmux history. Login containers
also retain direct Docker attachment. Output goes to command-provided streams;
Docker diagnostics go to stderr.

## Command reference

All commands are registered with help, examples, and argument validation. `run`
implements container creation/startup, `ps` lists saved sessions and their
status, `attach` selects live host agent connections or legacy terminals, `stop` stops all
processes in a workspace, and `rm` removes a stopped session and all its resources.
`logs` returns a "not implemented yet" error and exits with status 1.

| Command | Capability |
| --- | --- |
| `hewitt run [agent]` | Open a new agent in a shared workspace container and volume |
| `hewitt shell` | Open a shell in the workspace base checkout |
| `hewitt sync [session]` | Import a session's branches as `hewitt/<name>/<branch>` |
| `hewitt stop [session]` | Stop a session and print its full ID |
| `hewitt rm [session]` | Sync, then remove a stopped session and its volume/resources (`--force` discards uncommitted work), and print its full ID |
| `hewitt logs <session>` | Read session output (not implemented yet) |

`run` and `login` select the `ghcr.io/hewitt-iso/hewitt:latest` image automatically.
`run --detach` prepares a workspace without launching an agent (legacy modes start detached).
`ps` accepts no positional arguments. Session commands (`attach`, `stop`, `rm`)
accept a full 64-character hex ID, an unambiguous hex ID prefix, or a
session name, and support shell tab completion (`hewitt completion`). When
`<session>` is omitted on an interactive terminal, they open an interactive
session picker.
