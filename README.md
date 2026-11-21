<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hewitt-wordmark-dark.png">
    <img src="docs/assets/hewitt-wordmark-light.png" alt="hewitt" width="360">
  </picture>
</p>

<p align="center">
  <b>Run coding agents in isolated Docker workspaces on your machine.</b><br>
  The agent works freely; Hewitt keeps it within bounds.
</p>

<p align="center">
  <img alt="Status: early development" src="https://img.shields.io/badge/status-early%20development-E4572E?style=flat-square&labelColor=1B1A19">
  <img alt="Linux and macOS" src="https://img.shields.io/badge/platform-linux%20%7C%20macOS-EAE8E6?style=flat-square&labelColor=1B1A19">
  <img alt="Go 1.26" src="https://img.shields.io/badge/go-1.26-EAE8E6?style=flat-square&labelColor=1B1A19">
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quickstart">Quickstart</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#suspicious-requests">Suspicious requests</a> ·
  <a href="#documentation">Docs</a>
</p>

---

Code that runs only on your machine is like handwriting: others read it with
mistakes. A container turns it into print, the same everywhere. A coding agent
is print that moves and tries to step off the page. **Hewitt keeps it on the
page**: Claude Code, Codex, OpenCode, Pi, and Antigravity run in a container
with their own Git branch, while your host, your other projects, and your
logins stay out of reach.

- **One workspace, many agents.** Agents share a container and a repository,
  each on its own branch. Open another tmux window, pick another agent.
- **Your repository stays yours.** The session repository lives in a Docker
  volume; `hewitt sync` brings the branches back as `hewitt/<name>/<branch>`.
- **Logins agents cannot steal.** Claude's token never enters the container: a
  gateway replaces a placeholder on the way to Anthropic.
- **Nothing sensitive without you.** An agent reaching for another agent's
  login, a credential file, or an unlisted host waits for your decision,
  right in its tmux pane.
- **Native agents, native logins.** Each agent's own client and official
  login flow, kept in a private profile.

```text
╭─ Hewitt: suspicious request needs approval ─────────────────────╮
│                                                                 │
│  #1  codex wants file                                           │
│      /hewitt/profiles/claude/claude/.credentials.json           │
│      another agent's login and settings (claude) · session api  │
│      waiting 3s; refused after about a minute without a decision│
│                                                                 │
│  [a] approve   [d] deny   [k] abort codex   [esc] later         │
╰─────────────────────────────────────────────────────────────────╯
```

## Install

Hewitt needs **Linux or macOS**, **Git**, and the **Docker CLI** with a running
Docker Engine (Docker Desktop on macOS works). It uses your current Docker
context and never manages the daemon.

```sh
go install github.com/hewitt-iso/hewitt@latest
```

Or build from source (Go 1.26 or newer):

```sh
git clone https://github.com/hewitt-iso/hewitt.git
cd hewitt && make build      # bin/hewitt
```

Prebuilt binaries and Homebrew are not published yet. Images are pulled from
`ghcr.io/hewitt-iso` on first use; agents install on first launch.

## Quickstart

```sh
hewitt login claude          # official login, saved in a private profile
cd ~/code/my-project
hewitt                       # pick an agent; it starts on its own branch
```

Open another tmux window in the same project and run `hewitt` again to add a
second agent to the same workspace. When they are done:

```sh
hewitt sync                  # import their branches into your repository
git branch --list 'hewitt/*'
```

Give an agent its task up front, or run it without its interface and collect
the result:

```sh
hewitt run claude -p "fix the flaky test in api/"
hewitt run codex --headless --prompt-file task.md   # prints the result, exits
```

The first run offers a short setup: import your agent settings (with secret
values removed), choose a workspace, and log in. Run agents inside **tmux** to
keep them reattachable and to answer requests in their panes.

## How it works

```mermaid
%%{init: {"theme": "base", "themeVariables": {"primaryColor": "#F6F5F2", "primaryTextColor": "#1B1A19", "primaryBorderColor": "#5F5C58", "lineColor": "#5F5C58", "fontFamily": "Geist, ui-sans-serif, system-ui"}}}%%
flowchart LR
  you["You · tmux"] -->|"hewitt run"| ws
  subgraph docker["Docker on your machine"]
    ws["Workspace container<br/>claude · codex · opencode<br/>one branch each"]
    gw["Gateway<br/>token injection · allowlist"]
    ob["Observer<br/>file access"]
    vol[("Workspace volume<br/>session repository")]
    ws --- vol
    ws -->|"HTTPS"| gw
    ob -.->|"watches"| ws
  end
  gw --> net(("Internet"))
  gw -. "SRNA" .-> you
  ob -. "SRNA" .-> you
  classDef accent fill:#E4572E,stroke:#E4572E,color:#F6F5F2
  class gw,ob accent
```

1. **`hewitt run`** finds or creates the project's workspace container and
   opens the agent in it with its own worktree and branch. Matching runs reuse
   the container; your terminal or tmux owns the connection.
2. **The gateway** is each session's HTTPS proxy. It holds Claude's real token
   and swaps it in for the placeholder agents hold. At `restricted` and above it
   passes only allowlisted hosts and asks you about the rest.
3. **The observer** holds an agent's access to another agent's profile and to
   credential files until you decide. It is the only privileged helper and
   drops to your UID once attached.
4. **`hewitt sync`** imports the branches; your host repository is only read
   while agents work.

## Isolation levels

| Level | Filesystem | Network | Use it for |
|---|---|---|---|
| `shared` | No host paths unless mounted | Open | Trusted helpers |
| `isolated` *(default)* | Read-write host paths need `--danger host-paths` | Open; credentials via the gateway | Everyday work |
| `restricted` | Read-only root filesystem | Allowlist; others wait for approval | Unfamiliar code |
| `hardened` | As `restricted`, in a sandbox runtime (`runsc`, Kata) | As `restricted` | Untrusted code |

```sh
hewitt run codex --isolation restricted
```

Weakening a level always takes an explicit `--danger` item. What each layer
protects, and where it stops, is in [docs/security.md](docs/security.md).

## Suspicious requests

When an agent reaches for something it should not need, Hewitt holds the
request instead of failing silently: a **suspicious request needs approval
(SRNA)**. It appears as a popup in the agent's own tmux pane; answer with one
key, or decide in batches from any terminal:

```sh
hewitt requests              # numbered list: session, agent, target
hewitt approve 1-3 7         # allow; later requests for the same target pass
hewitt deny 6-9              # refuse; later requests for the same target fail
hewitt abort 4 5             # refuse and stop the agents that asked
```

Numbers never change once shown. `hewitt ps` marks waiting sessions as
`SRNA (n)`. To see pending requests in your tmux status line:

```tmux
set -g status-right '#(hewitt status --tmux) %H:%M'
set -g status-interval 5
```

## Everyday commands

| Command | What it does |
|---|---|
| `hewitt` | Pick an agent or a shell and open it in the project's workspace |
| `hewitt run <agent>` | Open a specific agent: `claude`, `codex`, `opencode`, `pi`, `antigravity` |
| `hewitt run <agent> -p "<task>"` | Start the agent on a task; add `--headless` to run it without its interface |
| `hewitt shell` | Open a shell in the workspace |
| `hewitt ps` | List workspaces, their agents, and pending requests |
| `hewitt attach` | Return to a running agent's tmux pane |
| `hewitt sync` | Import a workspace's branches into your repository |
| `hewitt stop` / `hewitt rm` | Stop a workspace with all its agents / remove it after syncing |
| `hewitt login <agent>` | Log in with the agent's official flow; `hewitt auth` has status, logout, relogin |
| `hewitt requests` · `approve` · `deny` · `abort` | Review and decide suspicious requests |
| `hewitt trust` | Allow a reviewed `hewitt.yml` to apply its sensitive settings |

## Project settings

Put a `hewitt.yml` in your repository to set defaults for everyone:

```yaml
agent: claude
isolation: restricted
egress:
  allow: [pypi.org, "*.pythonhosted.org"]
limits:
  memory: 4g
```

Settings that could expose logins or host files, such as `image`, `danger`, or
an egress allowlist, apply only after you run `hewitt trust`, and again after
every change. The full reference is in [docs/configuration.md](docs/configuration.md).

## Documentation

| Guide | Contents |
|---|---|
| [Using Hewitt](docs/usage.md) | Workspaces, agents, SSH push, project dependencies, attach, errors, command reference |
| [Configuration](docs/configuration.md) | `hewitt.yml` reference, OpenTelemetry, `config.json`, session metadata |
| [Security model](docs/security.md) | Gateway, credential injection, observer, approvals, limits |
| [Legacy modes](docs/legacy-modes.md) | Worktree, direct, copy, exec, and sidecar modes |
| [Development](docs/development.md) | Building, testing, architecture, publishing images |

## Status

Hewitt is in early development. Volume workspaces, the gateway, the observer,
and suspicious request approval work on Linux; macOS runs through Docker
Desktop, and the observer there depends on its kernel. `hewitt logs` is not
implemented yet. Contributions follow [AGENTS.md](AGENTS.md).
