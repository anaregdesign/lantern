# lantern-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server that
exposes a remote [Lantern](https://github.com/anaregdesign/lantern) endpoint as
a **shared working context for multi-agent fleets**: presence, advisory claims,
activity heat, and a blackboard, all built on decaying state.

Lantern models activity rather than durable knowledge. A stale claim should
vanish, recent activity should outweigh old activity, and a crashed agent's
presence should expire. One Lantern cluster per fleet is the shared-context
unit; pair it with upstream bearer-token auth (`LANTERN_AUTH_TOKENS` on the
server and `LANTERN_TOKEN` here).

> **Breaking change:** the deprecated `memory` profile and its
> `remember_*`/`recall_*` tools have been removed. `LANTERN_MCP_PROFILE` has no
> effect and should be removed from deployment configuration. Deployments must use the context tools below;
> use a durable knowledge store for long-term memory.

## Two-agent walkthrough

1. Agent A calls `announce`, then `track` for each resource it touches.
2. Before a risky edit, A calls `claim` on the shared dotted resource key.
3. Agent B calls `whats_happening` on that key and sees A, its live claim,
   co-active resources, and linked notes.
4. A calls `post_note` when the whole fleet needs a short-lived signal.
5. A calls `release` when finished. If it crashes, presence and leases expire
   automatically.

## Run

The binary serves Streamable HTTP MCP at `/mcp` and a plain `GET /healthz`
probe. The bare binary listens on `127.0.0.1:6390`; the container listens on
`0.0.0.0:6390` so its port can be published.

```shell
docker run --rm \
  -p 6390:6390 \
  -e LANTERN_ADDR=host.docker.internal:6380 \
  ghcr.io/anaregdesign/lantern-mcp:vX.Y.Z
```

Or run from source:

```shell
LANTERN_ADDR=http://localhost:6380 go run ./mcp/cmd
```

Point the MCP client at `http://localhost:6390/mcp`. Pin a real release tag in
production rather than using `latest`.

### Network and security

The MCP endpoint itself is unauthenticated. Keep it on a trusted network. The
bare binary binds loopback by default; only opt into `0.0.0.0` deliberately.
The handler applies cross-origin/DNS-rebinding protection. `/healthz` remains
unprotected for orchestrator probes. `SIGINT`/`SIGTERM` drains in-flight
requests for up to five seconds.

## Tools

| Tool | Purpose |
|---|---|
| `announce` | Publish presence and a one-line task at `agents.<id>`. Re-call as a heartbeat; silence removes the agent after the `transient` TTL. |
| `list_agents` | List live agents and their current task lines. |
| `track` | Add decaying `agents.<id> -> resource` activity edges. Repetition strengthens the signal. |
| `whats_happening` | Read the active neighborhood around a resource, agent, or note: agents, co-active resources, notes, and claims. |
| `claim` | Acquire or renew an advisory lease at `claims.<resource>`. A live foreign holder returns structured `{granted:false,...}`; `force` is for coordinated takeover only. |
| `release` | Drop a lease held by the current agent. |
| `list_claims` | List live leases, optionally under a resource-key prefix. |
| `post_note` | Post a TTL-bound info/warn/blocker signal linked to resource keys. |
| `context_stats` | Return live agent, claim, note, and tracked-resource counts. |
| `ping` | Verify both the MCP process and upstream Lantern connection. |

### Typed traversal in `whats_happening`

`whats_happening` accepts exactly one optional typed family arm. Omitting all
arms uses a safe bounded BFS (`step=2`, `fan_out=16`). Family-specific fields
are nested, so a BFS reduction cannot accidentally be sent to PPR.

```json
{"key":"repo.lantern.core"}
{"key":"repo.lantern.core","bfs":{"step":3,"fan_out":24,"reduction":"spt","objective":"minimize"}}
{"key":"repo.lantern.core","ppr":{"top_n":20,"restart_prob":0.2,"epsilon":0.0001}}
{"key":"repo.lantern.core","community":{"max_size":32,"reduction":"mst"},"weighting":"bm25"}
```

- `bfs`: `step`, `fan_out`, `reduction` (`none|mst|spt`), and `objective`
  (`maximize|minimize`).
- `ppr`: `top_n`, `restart_prob`, and `epsilon`.
- `community`: `max_size`, `restart_prob`, `epsilon`, and optional tree-view
  `reduction`/`objective`.
- Shared `weighting`: `raw` (default), `tfidf`, or `bm25`.

The structured result includes the selected `family`. Multiple family arms,
unknown enum values, and invalid rank domains are rejected before an RPC.

### Identity and keys

Every call is attributed to `LANTERN_MCP_AGENT_ID`, or a stable per-process
fallback `<hostname>-<pid>-<rand4>`. Fleet operators own uniqueness.

`agents.<id>`, `claims.<resource>`, and `notes.<id>` are reserved. Everything
else is a fleet-defined dotted resource key such as `repo.<name>.<path>`,
`ticket.<id>`, or `dataset.<name>`. Coordination only works when agents use
the same canonical key.

## TTL buckets

`announce`, `track`, and `claim` use fixed semantic buckets. `post_note`
requires an explicit bucket so the author states when a signal should stop
being true.

| Bucket | Default | Environment override |
|---|---:|---|
| `seconds` | 30s | `LANTERN_MCP_TTL_SECONDS` |
| `transient` | 2m | `LANTERN_MCP_TTL_TRANSIENT` |
| `turn` | 10m | `LANTERN_MCP_TTL_TURN` |
| `conversation` | 1h | `LANTERN_MCP_TTL_CONVERSATION` |
| `task` | 4h | `LANTERN_MCP_TTL_TASK` |
| `workday` | 12h | `LANTERN_MCP_TTL_WORKDAY` |
| `day` | 24h | `LANTERN_MCP_TTL_DAY` |
| `week` | 7d | `LANTERN_MCP_TTL_WEEK` |
| `sprint` | 14d | `LANTERN_MCP_TTL_SPRINT` |
| `month` | 30d | `LANTERN_MCP_TTL_MONTH` |
| `quarter` | 90d | `LANTERN_MCP_TTL_QUARTER` |
| `durable` | 180d | `LANTERN_MCP_TTL_DURABLE` |

Overrides use Go `time.ParseDuration` syntax and must remain strictly
monotonic. `LANTERN_MCP_MAX_TTL` can clamp the ladder to an upstream server's
shorter `LANTERN_TOMBSTONE_TTL`.

## Environment reference

| Variable | Default | Purpose |
|---|---|---|
| `LANTERN_ADDR` | `http://localhost:6380` | Upstream endpoint, or a comma-separated static failover set. |
| `LANTERN_MCP_HTTP_ADDR` | `127.0.0.1:6390` | Streamable HTTP listen address. |
| `LANTERN_MCP_PING_TIMEOUT` | `5s` | Startup health-probe timeout. |
| `LANTERN_MCP_TTL_<BUCKET>` | table above | Per-bucket override. |
| `LANTERN_MCP_MAX_TTL` | unset | Maximum resolved bucket duration. |
| `LANTERN_MCP_AGENT_ID` | auto | Stable fleet identity for this process. |
| `LANTERN_TOKEN` | unset | Bearer token for an authenticated upstream. |

Logs are structured JSON on stderr at INFO level.

## Multi-node failover

Pass a comma-separated fixed endpoint set:

```shell
LANTERN_ADDR=http://lantern-0:6380,http://lantern-1:6380,http://lantern-2:6380 \
  go run ./mcp/cmd
```

Startup succeeds when one node answers. Calls remain sticky to the current
node and rotate only for `Unavailable` transport failures; application errors
do not cause failover. The set is static—use DNS or a proxy for membership
discovery. Additive writes retain the usual small at-least-once response-loss
window.

## Dependency boundary

```text
mcp/ -> sdks/go/ -> pb/
```

`mcp/` never imports `core/` or `server/`; it is an external Lantern client.

## Container image and clients

`mcp/vX.Y.Z` tags publish `ghcr.io/anaregdesign/lantern-mcp` for
`linux/amd64` and `linux/arm64`, signed with keyless cosign. Image tags include
`vX.Y.Z`, `X.Y.Z`, `latest` for stable releases, and `sha-<short>`.

See [`examples/`](examples/) for Claude Desktop, VS Code, and Cursor remote-MCP
configuration. Hosts that only support stdio can bridge with `mcp-remote`.

## Development

```shell
(cd mcp && go test ./...)
(cd mcp && go test -race -shuffle=on ./...)
go test ./tests/integration -run TestMCP_ -count=1
```

The integration suite drives MCP in-memory transport into the Go SDK and then
over a real Connect/h2c server path.
