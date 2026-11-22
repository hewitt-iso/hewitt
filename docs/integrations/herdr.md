# Hewitt in Herdr

[Herdr](https://herdr.dev) organizes terminals into workspaces, tabs, and panes
and shows each coding agent's state in its sidebar. Hewitt runs the agent in a
container; Herdr keeps the pane, its scrollback, and the session across
terminal restarts. Nothing needs configuring on either side.

## Run an agent

Open a pane in Herdr and start the agent as usual:

```sh
hewitt run claude
```

Herdr lists it as `claude` with its live state (`working`, `blocked`, `idle`,
`done`). Herdr recognizes agents by the process in the pane, which for Hewitt
is the Docker CLI, so Hewitt sets Herdr's wrapper hint `HERDR_AGENT` on that
process; Herdr then reads the agent's screen as it does for a local agent. The
hint stays on the host and never reaches the container.

To start agents from a script or another agent, run Hewitt in a pane instead
of using `herdr agent start`, which launches only local agent executables:

```sh
pane=$(herdr pane split --current --direction right --no-focus | jq -r .result.pane.pane_id)
herdr pane run "$pane" "hewitt run codex -p 'fix the flaky test in api/'"
```

## Suspicious requests

When an agent in a Herdr pane makes a suspicious request, Hewitt splits the
agent's pane, asks there (`[a]` approve, `[d]` deny, `[k]` abort, `[esc]`
later), and closes the split when you answer. A Herdr notification with its
request sound points to it from other workspaces. `hewitt requests` and
`hewitt approve | deny | abort` work from any pane.

## Return to an agent

`hewitt attach` in any pane of the same Herdr session focuses the agent's
pane. From another Herdr session or outside Herdr, it names the session to
open instead.

## Batches

`hewitt batch` runs its agents headless, so they do not appear as agents in
Herdr; the pane that runs the batch shows their progress, and their requests
are asked in a split of that pane.

## What to leave off

- **Herdr worktrees.** Hewitt gives every agent its own branch in the
  workspace volume and `hewitt sync` imports them; run Hewitt from the main
  checkout instead of from `herdr worktree` checkouts.
- **`herdr integration install`.** Herdr's integrations add hooks to the
  agents' host configuration, such as `~/.claude`, which agents in Hewitt do
  not read; Herdr reads their state from the screen without them.

Verified with Herdr 0.8.2 on Linux: agent recognition, the request split, and
attach. Herdr reads agent states with its own screen rules, so their accuracy
follows Herdr's support for each agent.
