# lantern-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server that
exposes a remote [Lantern](https://github.com/anaregdesign/lantern) gRPC
endpoint as **decaying graph memory** for LLM agents.

The pitch: **facts decay on a TTL ladder; relations are additive and
decay independently**. Treating Lantern as a Redis-style KV with TTLs
misses the point — the value lives in the graph that emerges when you
let an agent reinforce useful relations and let weak ones die on their
own schedule.

For the end-user docs (Claude Desktop / VS Code / Cursor configs, agent
session walkthrough), see the **"Use as an MCP server"** section in the
[root README](../README.md#use-as-an-mcp-server). This file is the
reference for operators and contributors.

## Run

The binary speaks MCP over **stdio**. Diagnostics go to stderr; stdout
is owned by the JSON-RPC stream.

### From a container (recommended)

```shell
# Pin to a release tag — never `:latest` for agent runtimes.
docker run --rm -i \
  -e LANTERN_ADDR=host.docker.internal:6380 \
  ghcr.io/anaregdesign/lantern-mcp:v0.1.0
```

The image is published on every `mcp/vX.Y.Z` tag push (see
[mcp-publish.yml](../.github/workflows/mcp-publish.yml)). Both `vX.Y.Z`
and bare `X.Y.Z` tag forms are available, plus `latest` and `sha-<short>`.

### From source

```shell
# from repo root
LANTERN_ADDR=http://localhost:6380 go run ./mcp/cmd
```

## Tools

Six tools, all advertised at session-open via the server-instructions
string in [`server.go`](server.go). Descriptions are reproduced verbatim
from the source so the LLM and the human reader see the same contract.

| Tool | Purpose |
|---|---|
| `remember_fact` | Store a fact in Lantern with a **required TTL bucket**. Writing the same key again overwrites the value and resets the TTL — that is the canonical way to refresh a fact since `recall_*` does NOT refresh. |
| `recall_fact` | Look up a single fact by exact key. Returns `{found=false}` for missing keys (structured result, not a tool error). Does NOT refresh TTL. |
| `forget` | Delete a fact by exact key. Idempotent. Edges incident to the key are NOT cascade-deleted; they decay on their own TTL. |
| `list_under` | Enumerate facts whose key starts with the given prefix, in ascending key order. Defaults to 50 entries, max 500. |
| `remember_relation` | Add (or reinforce) a directed relation from one fact to another. **Additive** — writing the same relation twice strengthens it; this is the Hebbian primitive. |
| `recall_related` | Walk the graph from a seed key with `step`, `k`, and three orthogonal axes (`algorithm` ∈ `none` / `mst` / `spt`, `objective` ∈ `min` / `max`, `weighting` ∈ `raw` / `tfidf`; see #410). Returns related facts with cumulative weights. Does NOT refresh TTL. |

A `ping` tool also exists so operators can sanity-check the wire without
mutating state.

### TTL buckets (required parameter for every `remember_*` tool)

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

## Environment reference

| Variable | Default | Purpose |
|---|---|---|
| `LANTERN_ADDR` | `http://localhost:6380` | Upstream Lantern endpoint URL (Connect-on-h2c by default; use `https://` for TLS). |
| `LANTERN_MCP_PING_TIMEOUT` | `5s` | Bounds the startup health probe. A failed probe aborts startup with a non-zero exit so MCP clients surface a clear error. |
| `LANTERN_MCP_TTL_<BUCKET>` | see table above | Per-bucket TTL override. |

Logging is structured JSON via `log/slog` to stderr at `INFO` level
(stdout is owned by the JSON-RPC stream); there is no log-level or
format knob today.

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
  fine for stdio MCP, but `docker exec` is not supported).
- Signed with cosign keyless (`cosign verify --certificate-identity-regexp
  '^https://github.com/anaregdesign/lantern' ...`).

The version baked at build time is reported in the startup `slog` record
as the `version` field.

## Client configuration

See [`examples/`](examples/) for ready-to-copy snippets:

- [`examples/claude-desktop.json`](examples/claude-desktop.json) — Claude Desktop `claude_desktop_config.json`.
- [`examples/vscode-mcp.json`](examples/vscode-mcp.json) — VS Code workspace `.vscode/mcp.json`.
- [`examples/cursor-mcp.json`](examples/cursor-mcp.json) — Cursor `~/.cursor/mcp.json`.

All three follow the same shape: `command: docker`, `args` invoke the
container with stdio and your `LANTERN_ADDR`.

## Development

```shell
(cd mcp && go test ./...)              # unit tests
(cd mcp && go test -race -shuffle=on ./...)
go test ./tests/integration -run MCP   # in-process integration test
```

The integration test in [`tests/integration/mcp_test.go`](../tests/integration/mcp_test.go)
wires the server module against an in-process Lantern via `mcp.InMemoryTransport`
— no Docker required.
