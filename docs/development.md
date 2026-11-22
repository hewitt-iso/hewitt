# Development

Building, testing, and publishing Hewitt. See also [AGENTS.md](../AGENTS.md).

## Build and test

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
on Linux and macOS. See [AGENTS.md](../AGENTS.md) for contribution instructions.

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

The shared workspace lifecycle test exercises concurrent stub agents, shell exit,
container reuse and restart, without agent accounts:

```sh
HEWITT_TEST_IMAGE=hewitt:local go test -tags=integration ./session -run TestSharedWorkspaceLifecycle -v
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
| Agents | Add a manifest to `agent/manifests`; a new handler goes in `agent` |
| Authentication | Native login flows with private directories managed by `profile` |

Session management coordinates the other components without depending on
Cobra. Keep runtime and workspace operations out of command handlers. Add small
interfaces only where a real consumer needs substitution or testing. No empty
packages, daemon, server, database, or cloud service are part of this foundation.

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
Agents are not part of the images: they are installed into the tool cache on
first use (see [Using Hewitt](usage.md)) and are not updated by image releases. Scheduled builds
pull the pinned base images and disable Docker's build cache, so they pick up
Debian package updates.

Each run rebuilds the workspace image (`ghcr.io/hewitt-iso/hewitt`) and the
gateway image (`ghcr.io/hewitt-iso/egress`, the `hewitt` binary built from
this source), checks the CLI under an
arbitrary UID/GID, tests the container and tmux lifecycle, and publishes both
tested images as `:latest` using the workflow's `GITHUB_TOKEN`. Failed checks
prevent publishing, leaving the previously published tags available. Runs are
serialized to avoid overlapping uploads. `run` checks the default registry
images for updates; custom and locally built images are left alone.

This automation becomes active after the workflow and its supporting files
reach the repository's default branch. GitHub schedules can be delayed; these
are periodic rebuilds rather than instant upstream-release notifications.
For the existing packages, grant this repository Actions write access in each
package's settings if it was not inherited through the source repository link.

Publishing an update does not change running containers. The next `hewitt run`
pulls it and replaces idle workspace containers; legacy sessions keep their
original containers.

For a fresh local rebuild, use
`make images IMAGE_BUILD_FLAGS="--pull --no-cache"`.

The OCI source label links images to this repository. New GHCR packages start
private; maintainers must set their visibility to public in GitHub package
settings after the first push to enable anonymous pulls.
