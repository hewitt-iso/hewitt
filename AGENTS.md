# Working on Hewitt

Hewitt is a local-first Go CLI for running coding agents in isolated Docker
containers, each with a Git worktree and a persistent terminal session. Target
Linux and macOS. Support official Claude and ChatGPT subscription login flows;
do not make separate API credits a prerequisite.

## Structure and boundaries

- Follow the small, top-level package layout used by Ollama.
- `main.go`: process entry point, version injection, exit status.
- `cmd`: Cobra commands, argument validation, user-facing output.
- `config`: configuration defaults, file loading, and validation.
- `docker`: container creation, startup, and terminal attachment through the
  Docker CLI, persistent tmux server/client operations, subprocess errors, and cancellation. Callers own created containers; failed starts preserve them
  and report their IDs. Do not silently remove containers after a failed start.
- `session`: versioned local JSON metadata, create/save/start coordination, and saved
  session lookup for attachment. Native run sessions use a foreground tmux server;
  reconnecting must not launch another agent. Preserve legacy Docker attachment
  for records without a terminal marker.
  Save metadata before startup; preserve and identify containers if saving fails.
  Metadata deletion never removes containers or worktrees. Run sessions create
  Git workspaces and persist their locations before container startup. Native agent invocation and
  profile preparation already live in `agent` and `profile`. Add packages only with working behavior; do not create empty
  packages or speculative interfaces.
- `workspace`: Git worktree creation on unique branches and safe removal that
  refuses uncommitted, untracked, or ignored files and retains branches.
- `agent`: native Claude Code and Codex run/login descriptions and default image
  tags, independent of Cobra.
- `images`: shared Docker build stages with agent targets and the egress proxy.
  Keep default GHCR tags aligned with the Makefile; images contain no login state.
- `profile`: private agent-scoped directories for native client state; credentials
  are managed by the native clients.
- Keep Docker calls in the runtime boundary and Git operations in the workspace
  boundary. Agent adapters describe agent-specific execution; authentication
  handles credential lifecycles separately. Neither should depend on Cobra.
- Keep this a modular monolith: no daemon, database, or cloud dependency without
  an explicit requirement. Define small interfaces at consumers only when needed.

## Go and dependencies

- Use `gofmt`, standard Go naming, and small packages with concrete types first.
- Pass context into future subprocess/runtime operations; do not use package
  globals for mutable command or session state.
- Return errors with operation context and wrap causes with `%w`. Print errors
  once at the CLI boundary; only `main` decides process exit status.
- Prefer the standard library. Cobra is the CLI dependency; justify additional
  dependencies with a concrete need. Run `go mod tidy` and commit both module files.

## CLI and verification

- Keep help and version usable without Docker, credentials, or valid config.
- Send results to stdout and diagnostics to stderr; use command-provided writers
  so tests can capture output. Invalid input must exit nonzero.
- Never report success for unimplemented session commands.
- Test command behavior and config defaults/errors with Go's `testing` package.
  Use temporary directories; unit tests must not require Docker, network access,
  agent accounts, or the developer's real configuration.
- Before finishing, run `make fmt`, `make check`, and `make build`. Add regression
  tests for behavioral fixes; keep future Docker integration tests opt-in.
- Docker unit tests use a temporary fake executable on PATH. Real lifecycle tests
  require `-tags=integration` and `HEWITT_TEST_IMAGE`; clean up only their own
  containers. Never depend on a developer's daemon in ordinary unit tests.

## Isolation and credentials

- Never commit tokens, login state, private keys, or session data. Config is for
  non-secret preferences, not credentials; redact secrets in diagnostics/logs.
- Containers must not receive the host Docker socket, broad home-directory
  mounts, or privileged mode by default. Treat worktree paths and agent input as
  untrusted; pass subprocess arguments directly instead of interpolating a shell.
- Preserve dropped capabilities and `no-new-privileges` in container defaults.
  Container creation must not imply that an agent is authenticated or ready.
- Use official authentication flows. Do not copy host credential stores wholesale
  into images or worktrees. Restrict permissions on any future credential files.
- Worktree/session cleanup must not discard unrelated or uncommitted user work.

## Changes and documentation

- Make small, reviewable changes. Explain changes to lifecycle ownership,
  persistence, or isolation before implementing them; avoid frameworks for future
  possibilities.
- Update README examples and configuration documentation whenever behavior changes.
  Keep this file aligned with actual package responsibilities. Distinguish planned
  MVP capabilities from implemented commands. Do not create commits unless asked.
