# lantern-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server that
exposes a remote [Lantern](https://github.com/anaregdesign/lantern)
endpoint as a **shared working context for multi-agent fleets** —
presence, advisory claims, activity heat, and a blackboard, all built on
decaying state (#851).

The pitch: Lantern's primitive — additive TTL edges + decaying vertices —
models **activity, not knowledge**. Expiry is semantically *correct*
here: a stale claim SHOULD vanish, last hour's activity SHOULD outweigh
last week's, a crashed agent's presence SHOULD evaporate. TTL is the
lease/heartbeat mechanism; additive edges are who-touches-what heat that
strengthens with repetition and fades with neglect; Illuminate answers
"what is happening around X right now". One Lantern instance per
team/fleet is the unit of shared context — pair it with bearer-token
auth (`LANTERN_AUTH_TOKENS` server-side, `LANTERN_TOKEN` here, #850).

Two profiles exist behind `LANTERN_MCP_PROFILE`:

- **`context` (default)** — the working-context surface documented below.
- **`memory`** — the legacy decaying-memory verbs (`remember_*` /
  `recall_*`), kept for one `mcp/vX` release for existing deployments,
  then retired. See [Legacy memory profile](#legacy-memory-profile).

Both profiles share one keyspace on the server; switching does NOT
migrate or clean the other profile's data — TTL decay handles it.

## Two-agent walkthrough

Agent A (id `builder-1`) and agent B (id `reviewer-2`) share one Lantern:

1. **A announces + works**: `announce {task:"refactoring auth middleware"}`
   → visible in `list_agents`. As it edits, it calls
   `track {resources:["repo.api.middleware.auth","ticket.API-17"]}` —
   each call adds a decaying edge; repetition strengthens it.
2. **A claims before the risky part**: `claim {resource:"repo.api.middleware.auth",
   note:"rewriting, ~20min"}` → granted, ~10-minute lease, renewed by
   re-claiming.
3. **B looks before touching**: `whats_happening {key:"repo.api.middleware.auth"}`
   → sees builder-1 (task line), the live claim (holder + expiry), and
   builder-1's co-active resources. B works elsewhere.
4. **B hits the conflict anyway**: `claim` on the same key returns
   `{granted:false, holder:"builder-1", expires_at:…}` — a structured
   result, not an error. B waits or coordinates; `force:true` steals
   only after coordinating.
5. **A signals**: `post_note {text:"auth middleware API changed, rebase
   before touching", severity:"warn", links:["repo.api.middleware.auth"],
   ttl:"task"}` → B's next `whats_happening` on that resource surfaces it.
6. **A finishes**: `release` the claim. Then A crashes mid-session —
   nothing to clean up: its presence (~2m) and any remaining leases
   (~10m) expire on their own. The board never lies about the present.

For MCP client configs (Claude Desktop / VS Code / Cursor), see
[examples/](examples/). This file is the reference for operators and
contributors.

## Run

The binary serves MCP over **Streamable HTTP** (MCP spec 2025-06-18). It
listens on `LANTERN_MCP_HTTP_ADDR` (default `127.0.0.1:6390`), serves the
MCP endpoint at `/mcp`, and answers a plain `/healthz` probe with
`200 ok`. All diagnostics go to stderr.

### From a container (recommended)

```shell
# Pin to a release tag — never `:latest` for agent runtimes.
docker run --rm \
  -p 6390:6390 \
  -e LANTERN_ADDR=host.docker.internal:6380 \
  ghcr.io/anaregdesign/lantern-mcp:v0.4.0
```

Point your agent at `http://localhost:6390/mcp`. The image sets
`LANTERN_MCP_HTTP_ADDR=0.0.0.0:6390` so the published port is reachable
(the bare binary defaults to loopback — see **Network & security**).

The image is published on every `mcp/vX.Y.Z` tag push (see
[mcp-publish.yml](../.github/workflows/mcp-publish.yml)). Both `vX.Y.Z`
and bare `X.Y.Z` tag forms are available, plus `latest` and `sha-<short>`.

### From source

```shell
# from repo root — serves http://127.0.0.1:6390/mcp
LANTERN_ADDR=http://localhost:6380 go run ./mcp/cmd
```

### Network & security

The MCP endpoint is **unauthenticated**. The defaults are conservative:

- The bare binary binds **loopback only** (`127.0.0.1:6390`), so nothing
  off-host can reach it unless you opt in via `LANTERN_MCP_HTTP_ADDR`.
- The container and Helm chart set `0.0.0.0:6390` because the port has to
  cross the container boundary — only publish it on trusted networks.
- The handler wraps the MCP endpoint in `net/http` cross-origin
  protection (a DNS-rebinding defence) and the go-sdk rejects loopback
  requests carrying a non-loopback `Host` header. `/healthz` is
  unprotected so orchestrator probes (which send no `Origin`) pass.

`SIGINT`/`SIGTERM` triggers a graceful drain (5s) of in-flight requests
before exit.

## Tools (context profile — the default)

The tool set is advertised at session-open via the instructions string in
[`server.go`](server.go); summaries below paraphrase the source
descriptions.

| Tool | Purpose |
|---|---|
| `announce` | "I am here, working on T" — presence heartbeat + task line at `agents.<id>` (TTL ≈ 2m, the `transient` bucket). Re-call to refresh; go silent and you disappear from the board. |
| `list_agents` | Who is active right now. Expired presences are already gone, so the listing is always live. |
| `track` | "I touched R" — adds a decaying `agents.<id>` → `R` edge (~1h, `conversation`). Repetition strengthens, silence fades; this feeds `whats_happening` for everyone. |
| `claim` | Advisory lease on a resource at `claims.<resource>` (~10m, `turn`). A live foreign holder returns `{granted:false, holder, expires_at}` — a structured result, not an error; `force:true` steals. Re-claim to renew. The read-check-write window is documented and accepted (agents are slow writers); a server-side CAS RPC is the noted follow-up if contention materialises. |
| `release` | Drop your lease. Non-holder release is refused with the holder reported; expired/never-claimed is idempotent success. |
| `list_claims` | Live leases, optionally under a resource-key prefix. |
| `post_note` | Blackboard signal at `notes.<id>` (`severity` ∈ info/warn/blocker; explicit TTL bucket), linked note→resources + author→note so it surfaces in `whats_happening` nearby. |
| `whats_happening` | **The flagship read**: active neighborhood of a resource/agent/note — agents on it (with task lines), co-active resources (activity-weighted), live notes, live claims. A never-touched key returns a well-formed empty context. |
| `context_stats` | Fleet glance: live agents / claims / notes / tracked resources, four numbers. |

A `ping` tool also exists so operators can sanity-check the wire without
mutating state.

**Identity**: every call is attributed to `LANTERN_MCP_AGENT_ID` when
set, else a stable per-process fallback `<hostname>-<pid>-<rand4>`.
Fleet operators own id uniqueness — two sessions sharing an id
last-writer-win on the presence vertex. There is deliberately no
per-call id parameter (it would be spoofable and weaker than env-level).

**Key scheme**: `agents.<id>`, `claims.<resource>`, `notes.<id>` are
reserved; everything else is a resource, named by fleet convention with
dotted keys (`repo.<name>.<path>`, `ticket.<id>`, `dataset.<name>`).
Prefix scans give every listing for free; expiry makes them self-cleaning.

## Legacy memory profile

`LANTERN_MCP_PROFILE=memory` restores the decaying-memory verbs below for
one release cycle. The framing fought the engine — knowledge should not
decay, and single-agent memory never got dense enough for the graph to
earn its keep — which is exactly why the context profile replaced it
(#851 has the full decision record).

| Tool | Purpose |
|---|---|
| `remember_fact` | Store a fact in Lantern with a **required TTL bucket**. Writing the same key again overwrites the value and resets the TTL — that is the canonical way to refresh a fact since `recall_*` does NOT refresh. |
| `remember_facts` | Store **several facts in one call** — the batch counterpart to `remember_fact`. Pass `items[]`, each with its own key, value, and TTL bucket. Cuts round-trips when capturing many facts at once. An invalid item rejects the whole call (nothing written, safe to fix + resubmit); a partial server-side failure is reported per item with how many committed. |
| `recall_fact` | Look up a single fact by exact key. Returns `{found=false}` for missing keys (structured result, not a tool error). Does NOT refresh TTL. |
| `touch` | Extend a fact's TTL **without rewriting its value** — the cheap keep-alive. Since recall does NOT refresh TTL, touch a fact you want to keep instead of re-supplying its whole value via `remember_fact`. Reads the current value and re-stores it unchanged with a fresh expiry of now + the chosen bucket. A missing key returns `{found=false}` (structured result, not a tool error); touch never creates a fact. The vertex-side counterpart to `remember_relation` (which strengthens an edge rather than just keeping it alive). |
| `search_facts` | Find facts by a case-insensitive substring matched against both keys **and** values — the approximate counterpart to `recall_fact` for when you recall a topic but not the exact key. Returns compact `{key, snippet, expires_at}` previews (same shape as `list_under` `projection=snippet`); pass a `prefix` to scope and speed the scan. Does NOT refresh TTL. |
| `forget` | Delete a fact by exact key. Idempotent. Edges incident to the key are NOT cascade-deleted; they decay on their own TTL. |
| `forget_under` | Bulk-delete an **entire key namespace** in one call: every fact whose key starts with `prefix` is removed. The prefix-scoped counterpart to `forget` — tear down a working namespace (e.g. `session.verify.`) instead of forgetting keys one at a time. Refuses an empty or `*` prefix so you cannot wipe the whole store. Pass `dry_run=true` first to get a fast index estimate of how many facts would be deleted, then re-run with `dry_run=false`. Drains in rounds until empty; `truncated=true` means it stopped early (huge or concurrently-written namespace) — re-run to finish. Edges incident to deleted keys are NOT cascade-deleted; they decay on their own TTL. |
| `list_under` | Enumerate facts whose key starts with the given prefix, in ascending key order. Defaults to 50 entries, max 500. A `projection` (`keys` / `snippet` / `full`, default `full`) controls how much of each value is returned. |
| `list_namespaces` | Discover the **shape** of the key space: return the distinct child namespace segments under a prefix, each with a count of facts beneath it, and **no values**. An empty prefix (allowed here, unlike `list_under`) lists top-level namespaces; `depth` controls how many dot-delimited segments deep to aggregate (default 1). Does NOT refresh TTL. |
| `memory_stats` | Report how much is currently remembered and how soon it will decay — **counts only, never values**. Returns the total fact (vertex) and relation (edge) counts, a per-scope breakdown (`session.` / `task.` / `project.` / `user.`), and a remaining-life histogram bucketing facts by how soon they expire (the `seconds` / `transient` / `turn` buckets are what is about to be forgotten). Pass a `prefix` to scope every number to one namespace. Counts come from a cheap radix lookup; the histogram is a bounded sample, so a huge keyspace reports `sampled < vertices` and `truncated=true`. Does NOT refresh TTL. |
| `remember_relation` | Add (or reinforce) a directed relation from one fact to another. **Additive** — writing the same relation twice strengthens it; this is the Hebbian primitive. Returns the resulting `accumulated_weight` (distinct from this write's `increment`) so you can observe an association getting stronger. |
| `remember_relations` | Add (or reinforce) **several directed relations in one call** — the batch counterpart to `remember_relation`. Pass `edges[]`, each with `from`, `to`, TTL, and optional `weight`. **Additive**, so a PARTIAL failure must be resumed with only the edges that did not commit (resending a committed edge double-counts). For efficiency the batch path does NOT read back `accumulated_weight` — use `remember_relation` / `recall_relation` for that. |
| `recall_related` | Walk the graph from a seed key with `step`, `k`, a `direction` (`out` default — forward BFS over out-edges; `in` — the seed's direct predecessors, so seeding a pure sink is no longer empty; `both` — union), and the orthogonal axes (`algorithm` ∈ `none` / `mst` / `spt` / `ppr`, `objective` ∈ `min` / `max`, `weighting` ∈ `raw` / `tfidf` / `bm25`; see #410, #800, #801). `algorithm=ppr` runs Personalized PageRank (random-walk-with-restart proximity to the seed), tuned by the optional `restart_prob` (α) and `epsilon` (ε) knobs. Returns related facts with cumulative weights. Reverse predecessors carry `key` + `weight` only (no payload — call `recall_fact`); the reverse scan is bounded and sets `truncated` when it stops early. Read-only by default; pass `reinforce=true` to add a decaying weight pulse to the edges it traverses (opt-in — see below). |
| `recall_relation` | Read a **single** directed edge by its exact `(from, to)` endpoints, returning its current accumulated `weight` and `expires_at`. The per-edge counterpart to `recall_related` (which sums all incoming edges into a node-level score). Returns `{found=false}` for a missing or fully-decayed edge (structured result, not a tool error). Direction matters: `from→to` only. Does NOT refresh TTL or weight. |

A `ping` tool also exists so operators can sanity-check the wire without
mutating state.

### TTL buckets (used by `post_note`, and every `remember_*` tool in the memory profile)

Twelve enum horizons. Picking a shorter bucket is almost always the
right call ("when will this stop being true?"); writing again is cheap.

| Bucket | Default TTL | Env override |
|---|---|---|
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

Overrides use Go [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration)
syntax (no `d` suffix — use `168h` for a week). The resolved durations
must remain **strictly monotonic**; a misordered configuration is a
fatal startup error.

### Capping long buckets to a low server `LANTERN_TOMBSTONE_TTL`

The upstream Lantern server **rejects** any write whose expiry exceeds its
`LANTERN_TOMBSTONE_TTL` with `invalid_argument`. The server default
(`8760h`, 1 year) sits above the longest bucket, so out of the box every
bucket is accepted. But a node deliberately run with a short tombstone
window (e.g. `LANTERN_TOMBSTONE_TTL=24h`) would make `week` and longer
buckets fail hard.

Set `LANTERN_MCP_MAX_TTL` to the same (or a smaller) value to have the MCP
**clamp** each bucket's horizon down to that cap *before* writing, instead
of surfacing an error. When a write is clamped, the tool result sets
`"capped": true`, reports the shortened `expires_at`, and the text content
reminds you to re-`remember_*` before it expires. The bucket label is
preserved so the intent is still legible. Unset (the default) means **no
cap** — buckets resolve to their nominal horizons.

### Value projections for `list_under`

Surveying a namespace can dump large values into the model context when
you only wanted to know *which* keys exist. `list_under` accepts a
`projection` argument to control that:

| Projection | Returns | Use when |
|---|---|---|
| `keys` | `key` + `expires_at` only (no value) | Surveying which keys exist — cheapest. |
| `snippet` | `key` + a truncated ~120-char `snippet` of the value + `expires_at` | You want a preview without full payloads. |
| `full` | `key` + the entire `value` + `expires_at` | You need the values (the **default**, preserving prior behaviour). |

`full` remains the default for backward compatibility. Each entry always
carries its `key`; `value` is set only for `full`, `snippet` only for
`snippet`. The chosen mode is echoed back as `projection` on the result.

### Approximate recall with `search_facts`

`recall_fact` needs the **exact** key. When you remember the *topic* of a
fact but not where you filed it, `search_facts` does a case-insensitive
substring match over both keys and values and returns the same compact
`{key, snippet, expires_at}` rows as `list_under` `projection=snippet`. Read
a full value by passing a returned key back to `recall_fact`.

v1 is an unindexed scan-and-filter (no server change), so:

- Pass a `prefix` whenever you know the rough namespace — it scopes the scan
  to that subtree, making the search both faster and more precise.
- A single call scans at most `10000` vertices before stopping. If that
  ceiling is reached (or the `limit`, default 20 / max 100, is filled),
  `truncated` is `true` and `suggestion` tells you to narrow the prefix or
  raise the limit. `scanned` reports how many vertices were examined.

### Namespace discovery with `list_namespaces`

Before guessing keys, ask what's there. `list_namespaces` returns the
distinct child namespace segments under a prefix with a per-segment fact
count and **no values** — the cheap way to learn the schema of memory.

- An **empty prefix** lists the top-level namespaces of the whole keyspace
  (`user`, `project`, `session`, …). This is allowed here (unlike
  `list_under`) precisely because only segment names and counts are
  returned, never values.
- `depth` controls how many dot-delimited segments below the prefix collapse
  into each namespace (default 1 = immediate children, max 10).
- `has_children` on a result marks namespaces you can drill into further
  (with a longer prefix or a larger `depth`).
- Results are ordered most-populated first and capped at 500 namespaces. Like
  `search_facts`, the survey scans at most `10000` vertices; when either
  ceiling is hit, `truncated` is `true` (counts become lower bounds) and
  `suggestion` advises narrowing.

### Non-blocking key linting on `remember_fact` / `remember_facts`

Key quality decides whether a fact can later be recalled: a well-shaped key
round-trips through all three recall surfaces (exact `recall_fact`, prefix
`list_under`, substring `search_facts`), while a sloppy one is silently
accepted and only hurts later. To teach the conventions without ever breaking
a write, both `remember_fact` and every item of `remember_facts` run a shared
**non-blocking** key linter (`lintKey`). The fact is **always stored**; when
the key violates a convention the result simply carries a `warnings[]` field
(per item, on the batch path) with actionable hints. A clean key yields no
`warnings`.

| Rule | Fires when | Why it hurts recall |
|---|---|---|
| Whitespace | the key contains any space/tab | Awkward to reproduce verbatim for exact `recall_fact`; reads oddly in scans. |
| Unrecognized scope head | the first segment isn't `session` / `task` / `project` / `user` | A scope head keeps memory organized and predictable to address. |
| High-cardinality token mid-key | a date, UUID, or 6+ digit run sits in any segment **except the leaf** | Per-write tokens in the middle fragment the prefix tree that `list_under` / `search_facts` walk. A date/UUID is fine **as the leaf** (a unique event id). |
| Free-text leaf | the final segment has more than 3 alphabetic words | A descriptive phrase is hard to reconstruct exactly; prefer a concise canonical leaf. Pure-numeric tokens (a trailing date) don't count. |
| Excessive depth | the key has more than 5 dot-delimited segments | Deep nesting is hard to recall and address. |

Linting is advisory by design — it never rejects a key, so it nudges toward
the documented namespacing without the friction of a hard failure. (Hard
validation errors — an empty key, an unknown TTL bucket, an unencodable value
— are separate and *do* reject the write.)

### Reinforce-on-recall with `recall_related`

Recall is **read-only by default**: walking the graph does not refresh the
TTL of any vertex or edge it touches (the single most-violated invariant —
weak relations are supposed to decay). `recall_related` accepts an opt-in
`reinforce` flag that makes a *use-strengthens-memory* exception for the
edges the walk actually traverses — the Hebbian, edge-side analog of `touch`
(which keeps a single vertex alive). It is **off unless you ask for it**.

| Argument | Default | Effect |
|---|---|---|
| `reinforce` | `false` | When `true`, every edge traversed by this walk receives an additive weight pulse. `false` keeps the call purely read-only. |
| `reinforce_weight` | `1.0` | The weight added to each traversed edge. Additive — it stacks on the edge's existing weight. |
| `reinforce_ttl` | `conversation` | The decay horizon of the pulse, from the same 12-bucket ladder as `remember_*` (clamped by `LANTERN_MCP_MAX_TTL`, so a clamp sets `reinforce_capped`). |

Because the store keeps **every contribution as an independent
`(weight, expiration)` pulse** and an edge's life is its *latest*
contribution's expiry, a reinforcement **never shortens** an edge — a short
`reinforce_ttl` simply adds a pulse that itself fades on that horizon while
leaving any longer-lived contribution untouched. Frequent use stacks pulses
(sustained strength and a continually-extended life); sporadic use lets them
fade back to baseline. This is exactly the "use it or lose it" loop.

Reinforcement is a **best-effort side effect**: the recall result is produced
first and is never failed by a reinforcement write that does not land. The
result reports `reinforced` (how many traversed edges were bumped); to read an
edge's resulting `accumulated_weight`, call `recall_relation` (the hot recall
path deliberately skips a per-edge read-back). `reinforce` works for every
`direction` — it bumps the forward-walk edges for `out`, the predecessor edges
for `in`, and the union for `both`.

## Environment reference

| Variable | Default | Purpose |
|---|---|---|
| `LANTERN_ADDR` | `http://localhost:6380` | Upstream Lantern endpoint URL (Connect-on-h2c by default; use `https://` for TLS). Accepts a **comma-separated list** of endpoints for multi-node failover — see [Multi-node failover](#multi-node-failover). |
| `LANTERN_MCP_HTTP_ADDR` | `127.0.0.1:6390` | Address the Streamable-HTTP MCP endpoint listens on. Loopback by default; set `0.0.0.0:6390` to expose on all interfaces (the container/Helm defaults). |
| `LANTERN_MCP_PING_TIMEOUT` | `5s` | Bounds the startup health probe. A failed probe aborts startup with a non-zero exit so MCP clients surface a clear error. |
| `LANTERN_MCP_TTL_<BUCKET>` | see table above | Per-bucket TTL override. |
| `LANTERN_MCP_MAX_TTL` | unset (no cap) | Clamp every resolved bucket to at most this duration, matching a low upstream `LANTERN_TOMBSTONE_TTL`. Clamped writes report `"capped": true`. `time.ParseDuration` syntax. |
| `LANTERN_MCP_PROFILE` | `context` | Tool surface: `context` (working context, #851) or `memory` (legacy decaying-memory verbs, one-release deprecation window). |
| `LANTERN_MCP_AGENT_ID` | auto (`<hostname>-<pid>-<rand4>`) | Identity every context-profile call is attributed to. Fleet operators own uniqueness. |
| `LANTERN_TOKEN` | unset | Bearer token sent to a Lantern running with `LANTERN_AUTH_TOKENS` (#850). |

Logging is structured JSON via `log/slog` to stderr at `INFO` level;
there is no log-level or format knob today.

## Multi-node failover

In a high-availability Lantern deployment the cluster runs several
full-mesh-replicated nodes that hold identical state. Point the MCP at all
of them by passing a **comma-separated** `LANTERN_ADDR` so the loss of any
single node does not cut the agent off from its memory:

```sh
LANTERN_ADDR=http://lantern-0:6380,http://lantern-1:6380,http://lantern-2:6380 \
  go run ./mcp/cmd
```

Behaviour:

- **Startup probe.** The health check passes as long as **at least one**
  endpoint reports `SERVING`; the MCP sticks to that endpoint.
- **Per-call failover.** Each tool call is sent to the current endpoint.
  If — and only if — that endpoint is **unreachable** (a connection
  refused / `Unavailable` transport error), the client rotates to the next
  endpoint and retries, walking the ring once. Application errors
  (`not found`, `invalid argument`, rate limiting) are returned as-is and
  never trigger failover, because a consistent replica answers them
  identically.
- **Stickiness.** After a rotation the client stays on the endpoint that
  answered, so a downed node is not retried on every subsequent call.
- **Writes.** Failover for the additive edge writes only fires on a
  pre-delivery `Unavailable` (nothing was committed on the dead node), so a
  rotation does not double-apply a write in the common node-down case. The
  residual at-least-once window (a write that commits but whose response is
  lost) is inherent to any failover and is harmless here — edge weights are
  additive and `reinforce` is best-effort.

A single (non-comma) `LANTERN_ADDR` keeps the original single-endpoint
behaviour exactly.

## Dependency boundary

```
mcp/  →  sdks/go/  →  pb/
```

`mcp/` never imports `core/` or `server/`. The MCP server is a vanilla
Lantern client and talks to the upstream over the wire like any external
caller — which is exactly why the image can be cut independently of the
server release cadence.

## Container image

Built by [mcp-publish.yml](../.github/workflows/mcp-publish.yml) on every
`mcp/vX.Y.Z` tag push:

- Image: `ghcr.io/anaregdesign/lantern-mcp`
- Tag forms: `vX.Y.Z`, `X.Y.Z`, `latest` (non-prerelease only), `sha-<short>`.
- Multi-arch: `linux/amd64` + `linux/arm64`.
- Base: `gcr.io/distroless/static:nonroot` (no shell, no writable FS —
  the server needs neither, but `docker exec` and in-container
  healthchecks are not supported; probe `GET /healthz` from outside).
- Signed with cosign keyless (`cosign verify --certificate-identity-regexp
  '^https://github.com/anaregdesign/lantern' ...`).

The version baked at build time is reported in the startup `slog` record
as the `version` field.

## Client configuration

See [`examples/`](examples/) for ready-to-copy snippets:

- [`examples/claude-desktop.json`](examples/claude-desktop.json) — Claude Desktop `claude_desktop_config.json`.
- [`examples/vscode-mcp.json`](examples/vscode-mcp.json) — VS Code workspace `.vscode/mcp.json`.
- [`examples/cursor-mcp.json`](examples/cursor-mcp.json) — Cursor `~/.cursor/mcp.json`.

All three follow the same shape: a single `url` pointing at the running
endpoint (`http://localhost:6390/mcp`). Hosts that only speak stdio can
bridge via `mcp-remote` — see [`examples/README.md`](examples/README.md).

Connecting the server only makes the tools *available*. To make the agent
**use** them without being asked — recall before answering, capture durable
facts after — install the ambient-memory instruction profile:

- [`examples/ambient-memory.instructions.md`](examples/ambient-memory.instructions.md) — copy-paste capture+recall policy, plus VS Code / Claude Desktop wiring and an Admin Illuminate "watch your mind map" pointer in [`examples/README.md`](examples/README.md#make-the-agent-use-lantern-automatically).

## Development

```shell
(cd mcp && go test ./...)              # unit tests
(cd mcp && go test -race -shuffle=on ./...)
go test ./tests/integration -run MCP   # in-process integration test
```

The integration test in [`tests/integration/mcp_test.go`](../tests/integration/mcp_test.go)
wires the server module against an in-process Lantern via `mcp.InMemoryTransport`
— no Docker required.
