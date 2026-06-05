# lantern-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server that
exposes a remote [Lantern](https://github.com/anaregdesign/lantern) gRPC
endpoint as **decaying graph memory** for LLM agents.

This module is a standalone executable. Real tool surface (`remember_fact`,
`recall_related`, …) lands in sub-issue
[#285](https://github.com/anaregdesign/lantern/issues/285); this scaffold
ships a single no-op `ping` tool so the end-to-end MCP round-trip can be
validated.

## Run

```bash
# from repo root
LANTERN_ADDR=localhost:6380 go run ./mcp/cmd
```

The binary speaks MCP over **stdio**. Diagnostics go to stderr; stdout is
owned by the JSON-RPC stream.

## Dependency boundary

```
mcp/  →  sdks/go/  →  pb/
```

`mcp/` never imports `core/` or `server/`. The MCP server is a vanilla
Lantern client and talks to the upstream over the wire like any external
caller.

## Environment

| Variable | Default | Purpose |
|---|---|---|
| `LANTERN_ADDR` | `localhost:6380` | Upstream Lantern gRPC target. |
| `LANTERN_MCP_PING_TIMEOUT` | `5s` | Bounds the startup health probe. |
| `LANTERN_MCP_TTL_<BUCKET>` | see below | Per-bucket TTL override. |

### TTL buckets

Twelve required-enum horizons that every `remember_*`-style tool will take
as a typed parameter (wiring lands in #285). Defaults:

| bucket | default |
|---|---|
| `seconds` | 30s |
| `transient` | 2m |
| `turn` | 10m |
| `conversation` | 1h |
| `task` | 4h |
| `workday` | 12h |
| `day` | 24h |
| `week` | 7d |
| `sprint` | 14d |
| `month` | 30d |
| `quarter` | 90d |
| `durable` | 180d |

Overrides use Go [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration)
syntax (no `d` suffix — use `168h` for a week). The resolved durations must
remain **strictly monotonic**; a misordered configuration is a fatal
startup error.
