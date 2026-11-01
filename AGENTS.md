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
- Future session management coordinates container runtime, Git workspaces,
  agent adapters, and authentication. Add those packages only with working
  behavior; do not create empty packages or speculative interfaces.
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

## Isolation and credentials

- Never commit tokens, login state, private keys, or session data. Config is for
  non-secret preferences, not credentials; redact secrets in diagnostics/logs.
- Future containers must not receive the host Docker socket, broad home-directory
  mounts, or privileged mode by default. Treat worktree paths and agent input as
  untrusted; pass subprocess arguments directly instead of interpolating a shell.
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
