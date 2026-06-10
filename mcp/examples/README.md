# lantern-mcp client config examples

Ready-to-copy MCP server entries for the major agent runtimes. The
`lantern-mcp` server speaks **Streamable HTTP** (MCP spec 2025-06-18): you
run it as a long-lived process that listens on `:6390` and serves the MCP
endpoint at `/mcp`, then point your agent at the URL. All three files
share the same shape — a single `url` pointing at the running endpoint.

| File | Drop into |
|---|---|
| [`claude-desktop.json`](claude-desktop.json) | `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) / `%APPDATA%\Claude\claude_desktop_config.json` (Windows) |
| [`vscode-mcp.json`](vscode-mcp.json) | `.vscode/mcp.json` in your workspace, or `~/.config/Code/User/mcp.json` for user scope |
| [`cursor-mcp.json`](cursor-mcp.json) | `~/.cursor/mcp.json` |

## Start the server first

The agent no longer spawns the container — it connects to an
already-running endpoint. Start one with Docker:

```shell
docker run --rm \
  -p 6390:6390 \
  -e LANTERN_ADDR=host.docker.internal:6380 \
  ghcr.io/anaregdesign/lantern-mcp:v0.4.0
```

The container binds `0.0.0.0:6390` internally (so the published port is
reachable) and the host sees it at `http://localhost:6390/mcp`. The
endpoint is **unauthenticated**, so only publish the port on trusted
networks; the handler still applies cross-origin and DNS-rebinding
protection. See [`../README.md`](../README.md) for the full security note
and for running the bare binary (which defaults to loopback only).

## Things to change before pasting

- **Pin the image tag.** The `docker run` above uses `v0.4.0`; bump to
  whatever the newest
  [`mcp/vX.Y.Z` release](https://github.com/anaregdesign/lantern/releases?q=mcp%2F)
  ships. Do not use `:latest` — agent runtimes treat the tool surface as
  contract, and a silent schema bump is hostile.
- **Pick the right `LANTERN_ADDR`** (passed to the *server*, not the agent
  config):
  - macOS / Windows Docker Desktop, Lantern on the host: `host.docker.internal:6380` (used above).
  - Linux Docker, Lantern on the host: add `--network=host` and use `127.0.0.1:6380`.
  - Lantern on a remote host: `lantern.example.com:6380` (consider TLS — see [#212](https://github.com/anaregdesign/lantern/issues/212)).
- **Change the URL** only if you remapped the port (`-p 7000:6390` →
  `http://localhost:7000/mcp`) or run the server on another host.
- **TTL ladder overrides** (optional): pass `-e LANTERN_MCP_TTL_TURN=15m`
  etc. to the `docker run` for any of the 12 buckets. Defaults are listed
  in [`../README.md`](../README.md#ttl-buckets-required-parameter-for-every-remember_-tool).

### Hosts that only support stdio MCP servers

If your runtime predates remote/HTTP MCP support and only accepts a
`command`/`args` stdio server, bridge it with
[`mcp-remote`](https://www.npmjs.com/package/mcp-remote):

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

Liveness probe (no MCP handshake, always safe):

```shell
curl -fsS http://localhost:6390/healthz   # -> ok
```

Full MCP `initialize` over Streamable HTTP:

```shell
curl -sS http://localhost:6390/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}'
```

If Lantern is reachable you will see the standard MCP `initialize`
response (as JSON or a single SSE `data:` frame). If the upstream Lantern
endpoint is unreachable, the server exits non-zero at startup with a
single line on stderr identifying the address that failed — so a failing
`curl` against a dead container means the server never came up; check
`docker logs`.
