# Security model

Hewitt runs agents in Docker containers on your machine. It reduces what an
agent can reach; it does not make running untrusted code safe. This page
states what each layer does and where it stops.

## What Hewitt protects

| Layer | What it keeps from agents |
|---|---|
| Container defaults | Host files outside explicit mounts, the Docker socket, capabilities (`--cap-drop ALL`), privilege escalation (`no-new-privileges`) |
| Volume workspace | The host repository: the session repository lives in a Docker volume, and host Git never opens it |
| Read-only tool cache | Replacing the agent binaries that other sessions and projects run |
| Trusted `hewitt.yml` | Settings from a cloned repository that would expose logins or host files, until you review the file |
| Secret-free imports | Secret values from your host configs, unless you choose to keep them |
| Gateway | Claude's long-lived token (agents hold a placeholder), and, at `restricted` and `hardened`, hosts outside the allowlist |
| Observer | Other agents' logins and credential files, until you approve each access |

## Where it stops

- Containers share the host kernel. Only `hardened`, with a separate sandbox
  runtime such as gVisor (`runsc`) or Kata, adds a kernel boundary.
- Agent profiles are writable by their agent, and Codex, OpenCode, Pi, and
  Antigravity keep their logins there. The observer holds access across agents
  in one workspace; it does not stop an agent from using its own login.
- An allowlist does not stop deliberate exfiltration through an allowed host:
  an agent that can reach `github.com` can push to any repository there.
- At `shared` and `isolated`, the network is open apart from the gateway's
  credential injection.
- The observer needs Linux fanotify permission events; where it cannot attach,
  `run` warns and the session continues without it.

## Gateway, credentials, and approvals

Every session in `new` mode on the default network gets a Hewitt gateway: a
separate container (`ghcr.io/hewitt-iso/egress`) on a per-session network that
agents reach as their HTTPS proxy. Agents cannot read its configuration,
state, or credentials.

- **Credential injection.** `hewitt login claude` saves the long-lived token in
  `<data_dir>/secrets/claude/<profile>/`, which only the gateway mounts. Claude
  receives the placeholder `sk-ant-oat01-hewitt-gateway-managed`; the gateway
  terminates TLS for `api.anthropic.com` only, with a certificate from a CA in
  `<data_dir>/gateway-ca` that agent containers trust through
  `NODE_EXTRA_CA_CERTS`, and replaces the placeholder with the token. Other
  hosts are tunneled unread. A token saved in a profile by an earlier version
  moves to `secrets/` on the next run. A new login applies without restarting
  the gateway. Sessions on a custom `network` have no gateway and receive the
  token itself, as before. Codex, OpenCode, Pi, and Antigravity keep their
  logins in their profiles.
- **Open and restricted gateways.** `shared` and `isolated` sessions pass every
  HTTPS host; their network reaches the outside, so plain HTTP goes direct.
  `restricted` and `hardened` sessions have an internal network and pass only
  tunnels to the agent's provider hosts and `egress.allow` entries, on port 443
  unless an entry names another port (`host:port`). A port opens for that host
  only, and `run` warns about each such entry because its traffic need not be
  HTTPS. Plain HTTP requests through a restricted gateway are refused, so
  clients must use HTTPS or tunnel through the proxy, as Claude Code's
  OpenTelemetry exporter does for an `http://` endpoint.
- **Suspicious requests need approval (SRNA).** A restricted gateway holds a
  connection to any other host for about a minute instead of refusing it.
  The file observer (below) holds file access the same way. See
  [Approving suspicious requests](#approving-suspicious-requests).

## File observer

Agents of one workspace share its container and UID, so mounts cannot keep
them out of each other's profiles. In `isolated`, `restricted`, and
`hardened` workspaces, a Hewitt observer container holds such access instead:

- An agent opening another agent's profile (for example Codex reading
  `/hewitt/profiles/claude`, where Claude's login lives) waits for approval.
  An agent's own profile, and everything the `hewitt shell` user opens, pass.
- Opening or creating a credential file in the workspace (`.env*` other than
  examples, `*.pem`, `*.key`, `credentials`, `auth.json`, SSH keys) waits for
  approval.
- Processes are attributed to the agent launch they descend from: each launch
  gets `HEWITT_RUN` and `HEWITT_AGENT`, and the topmost process of a launch
  decides, so a child cannot claim another agent's identity. A process outside
  every launch is unattributed (`-`), and its access to any profile waits.
- The observer starts as root in the host PID namespace with `CAP_SYS_ADMIN`,
  `CAP_SYS_PTRACE`, `CAP_DAC_READ_SEARCH`, `CAP_SETUID`, and `CAP_SETGID`, only
  to find the workspace container and attach fanotify permission marks to its
  profile and workspace mounts. It then switches to your UID with no
  capabilities, has no network, and exits when the workspace stops; agent
  containers keep their dropped capabilities. It needs a kernel with fanotify
  permission events; if it cannot attach, `run` warns and the session continues
  without it. `docker logs` of the observer shows every decision.

## Approving suspicious requests

`hewitt ps` shows sessions with pending requests as `SRNA (n)`. `hewitt
requests` lists them with a number, the session, the agent that made the
request, and its target; `-a` includes decided ones. A number is assigned the
first time a request is listed and never changes, so numbers you read stay
valid when new requests arrive.

```sh
hewitt requests
hewitt approve 1-3 7      # allow; later requests for the same target pass
hewitt deny 6-9           # refuse; later requests for the same target fail
hewitt abort 4 5          # refuse and kill the agent launches that asked
hewitt approve {10..12}   # shell brace expansion also works
```

Every selected number must exist, or nothing is decided. A waiting request
proceeds or fails at once; one that waited about a minute without a decision
is refused, and an agent that retries follows the decision made meanwhile.
Network decisions apply to the session's later connections to the same host
and port; file decisions apply to the same file for the same agent. `abort`
kills every process of the launch in the workspace container, including
background children; a request without an agent launch is denied but stops
nothing. Restricted sessions name the agent of network requests through
per-launch proxy credentials (`http://<run>:<agent>@hewitt-egress:3128`).

An allowlist does not stop deliberate exfiltration through an allowed host:
an agent that can reach `github.com` can push data to any repository there.
The gateway keeps credentials out of agents and makes unexpected destinations
visible; it is not a data-loss filter.
