# lantern-mcp client config examples

Ready-to-copy MCP server entries for the major agent runtimes. All three
files share the same shape: spawn the `ghcr.io/anaregdesign/lantern-mcp`
container with stdio attached and point it at a Lantern gRPC endpoint
via `LANTERN_ADDR`.

| File | Drop into |
|---|---|
| [`claude-desktop.json`](claude-desktop.json) | `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) / `%APPDATA%\Claude\claude_desktop_config.json` (Windows) |
| [`vscode-mcp.json`](vscode-mcp.json) | `.vscode/mcp.json` in your workspace, or `~/.config/Code/User/mcp.json` for user scope |
| [`cursor-mcp.json`](cursor-mcp.json) | `~/.cursor/mcp.json` |

## Things to change before pasting

- **Pin the image tag.** The examples use `v0.1.0`; bump to whatever the
  newest [`mcp/vX.Y.Z` release](https://github.com/anaregdesign/lantern/releases?q=mcp%2F)
  ships. Do not use `:latest` — agent runtimes treat the tool surface as
  contract, and a silent schema bump is hostile.
- **Pick the right `LANTERN_ADDR`.**
  - macOS / Windows Docker Desktop, Lantern on the host: `host.docker.internal:6380` (used in the snippets).
  - Linux Docker, Lantern on the host: `127.0.0.1:6380` with `--network=host` (drop the `-e` and add `--network=host` to `args`).
  - Lantern on a remote host: `lantern.example.com:6380` (consider TLS — see [#212](https://github.com/anaregdesign/lantern/issues/212)).
- **TTL ladder overrides** (optional): append `-e LANTERN_MCP_TTL_TURN=15m`
  etc. to `args` for any of the 12 buckets. Defaults are listed in
  [`../README.md`](../README.md#ttl-buckets-required-parameter-for-every-remember_-tool).

## Sanity check

```shell
docker run --rm -i \
  -e LANTERN_ADDR=host.docker.internal:6380 \
  ghcr.io/anaregdesign/lantern-mcp:v0.1.0 <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}
EOF
```

If Lantern is reachable you will see the standard MCP `initialize`
response on stdout. If not, the container exits non-zero with a single
line on stderr identifying the address that failed.
