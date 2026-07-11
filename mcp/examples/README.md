# lantern-mcp client config examples

These files connect common MCP hosts to a running Lantern shared-context
server over Streamable HTTP:

| File | Install location |
|---|---|
| [`claude-desktop.json`](claude-desktop.json) | Claude Desktop MCP configuration |
| [`vscode-mcp.json`](vscode-mcp.json) | `.vscode/mcp.json` or VS Code user configuration |
| [`cursor-mcp.json`](cursor-mcp.json) | `~/.cursor/mcp.json` |

## Start the server

```shell
docker run --rm \
  -p 6390:6390 \
  -e LANTERN_ADDR=host.docker.internal:6380 \
  ghcr.io/anaregdesign/lantern-mcp:vX.Y.Z
```

Replace `vX.Y.Z` with a published release and pin it. The endpoint is
`http://localhost:6390/mcp`. The MCP surface is unauthenticated, so publish it
only on a trusted network. See the [operator reference](../README.md) for TLS,
upstream auth, TTL, and failover settings.

The server's session instructions already teach the announce/track/claim/read
coordination loop. No separate ambient-memory prompt is required or supported.

## stdio-only hosts

Bridge older hosts with `mcp-remote`:

```json
{
  "mcpServers": {
    "lantern": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://localhost:6390/mcp"]
    }
  }
}
```

## Sanity check

```shell
curl -fsS http://localhost:6390/healthz
```

A successful response is `ok`. If it fails, inspect the container logs; the
MCP process exits non-zero when its startup ping cannot reach Lantern.

## Inspect the live context graph

Point [lantern-admin](../../admin/) at the same Lantern server and open the
**CLI** (`/cli`) workspace. Run `bfs agents.<id> 2 16` to inspect an agent's
current working set, or seed a canonical resource key to see nearby decaying
activity. This is operational context only; expired presence and activity are
expected to disappear.
