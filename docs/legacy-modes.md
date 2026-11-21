# Legacy workspace modes

The default volume workspace keeps the session repository inside Docker. These modes remain for existing setups; they keep a terminal server inside the container.

## Worktree workspace (legacy)

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
are required. Native credentials remain in the private profile directory, and
Claude's long-lived token in `<data_dir>/secrets`, which only the gateway reads
(see Gateway, credentials, and approvals); do not commit either. Login output is not recorded by Docker.

Sessions may share a profile. Codex refreshes its own tokens in the shared
`auth.json` and reloads it when another session has already refreshed; if two
sessions refresh at the same moment, one may need `hewitt login codex` again.

`hewitt auth relogin <agent>` runs the native logout and then the native login
for one agent and profile, each in its own preserved container. It stops
without logging in if logout fails.

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

Images set `LANG=C.UTF-8` so non-ASCII input such as Cyrillic works.

Default volume `run` saves workspace metadata before startup and keeps the
container alive with an idle foreground process. Each connection uses
`docker exec --interactive` (with a TTY when stdin is a terminal) to launch
`hewitt-agent run`. No tmux server is started. Profile and isolation settings
belong to the workspace; processes in one container share its security boundary.
Use `--separate` or distinct names for separate container isolation.

`run --detach` prepares the workspace and prints its ID without launching an
agent. Foreground runs are kept connected by your host terminal or host tmux.
Hewitt records each foreground connection with a short run ID, agent name,
workspace identity, and host tmux pane when available. `attach` lists live
connections and returns to the selected pane. It neither starts another agent
nor takes over an arbitrary process's terminal. A later `run` opens a fresh agent
on a new branch; native conversation resume is agent-specific. `shell` opens a
fresh shell and is tracked in the same way.

Explicit worktree/direct/copy and exec/sidecar modes retain their previous
container tmux behavior: `--detach` starts the agent and `attach` reconnects to
its existing terminal. Saved tmux records are never converted or restarted by
attachment.

Containers use an init process, open stdin, and the label
`io.hewitt.managed=true`. All Linux capabilities are dropped and
`no-new-privileges` is enabled. The agent-scoped profiles and the workspace volume are
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
