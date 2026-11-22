# Hewitt in Agent of Empires

[Agent of Empires](https://www.agent-of-empires.com) (`aoe`) manages coding
agent sessions in tmux, with a dashboard of their states. Hewitt runs each
agent in a container; AoE keeps its tmux session and shows its state.

> This recipe follows the AoE 1.25 documentation and has not yet been run
> end to end. Please report what differs.

## Configure

Add Hewitt agents as AoE custom agents in AoE's configuration file (see
AoE's configuration guide for its location):

```toml
[session]
custom_agents = { "hewitt-claude" = "hewitt run claude", "hewitt-codex" = "hewitt run codex" }
agent_detect_as = { "hewitt-claude" = "claude", "hewitt-codex" = "codex" }
```

`agent_detect_as` makes AoE read the pane with its Claude and Codex rules, so
states appear as for a local agent.

## Run

```sh
cd ~/code/my-project
aoe add --tool hewitt-claude -l .
```

Leave AoE's sandbox (`-s`) and worktree (`-w`) options off; both are off by
default. Hewitt already runs the agent in a container and gives it its own
branch, and `hewitt sync` imports the branches.

## Suspicious requests and attach

AoE sessions are tmux sessions, so Hewitt's request popup opens over the
agent's pane, and `hewitt attach` returns to it, on AoE's tmux server
including a private one set with `[tmux] socket_name`.

## Status hooks

AoE installs status hooks into the agents' host configuration, such as
`~/.claude`, and reads them from `/tmp/aoe-hooks-<uid>/`. Agents in Hewitt use
their own profiles and cannot reach that directory, so AoE relies on reading
the pane. Turn the hooks off to keep AoE from editing host configuration that
Hewitt agents do not read:

```toml
[session]
agent_status_hooks = false
```
