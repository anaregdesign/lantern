# Docker Compose: HA lantern cluster

3-replica peer-discovery cluster (Tier-A) on a single Docker host. Mirrors
the Helm chart in [`../helm/lantern/`](../helm/lantern/) for local dev and
single-host HA experiments.

## Topology

- 3 × `lantern` replicas (`deploy.replicas: 3`), each on host ports
  `6380`, `6381`, `6382`.
- Compose's embedded DNS resolves the service name `lantern` to one A
  per running replica. Each lantern container's peer pump (#190)
  resolves that name, filters its own IP via `LocalIPSet()`, and opens
  replication streams to the other peers.
- 1 × `admin` browser SPA (`ghcr.io/anaregdesign/lantern-admin`)
  on host port `8080`. Pure SPA host (Caddy on `:8080`), no reverse
  proxy — the browser talks to the gateway directly, so the `lantern`
  service has `LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080`
  baked in.
- `prometheus` scrapes via `dns_sd_configs` so it picks up every
  replica automatically (no static targets file to maintain).

## Run

```shell
cd deploy/compose
docker compose up -d
docker compose ps
```

Scale up or down without restarting; each lantern container reconciles
within one discovery tick (default `LANTERN_PEER_DISCOVERY_INTERVAL_MS=10000`):

```shell
docker compose up -d --scale lantern=5
# Logs on any one container should show 4 active peers within ~10s.
docker compose logs --tail=20 lantern
```

Tear down:

```shell
docker compose down -v
```

## Client access

The example does **not** include an in-cluster client LB sidecar.
Pick one of:

- **Reverse proxy / sidecar.** Drop in Caddy / Traefik / envoy with a
  DNS-resolved upstream pool against `lantern:6380`. The SDK then dials
  one URL (`http://lantern-proxy:6380`), TLS terminates at the edge,
  and replica scaling is automatically picked up.
  ```go
  c, _ := lantern.NewLantern("http://lantern-proxy:6380")
  ```
- **DNS round-robin from the client.** If the SDK lives in the same
  Compose network, dial the service name and let the OS resolver hand
  out IPs:
  ```go
  c, _ := lantern.NewLantern("http://lantern:6380")
  ```
- **CLI / grpcurl**: hit any container directly; writes propagate via
  the peer pump.

> The pre-#367 `NewLanternWithEndpoints([]string{...})` SDK-side
> round-robin LB was removed when the SDK collapsed to Connect-only
> ([#367](https://github.com/anaregdesign/lantern/issues/367)). Use a
> reverse proxy or DNS round-robin instead.

## Admin UI

The compose file ships the `lantern-admin` browser SPA alongside the
cluster. Open **<http://localhost:8080/>** after `docker compose up -d`
finishes warming up. The admin connects to whichever lantern node the
**Gateway** button (top-right of the SPA header) is set to — defaults
to `http://localhost:6380`; change to `:6381` or `:6382` to point at
the other replicas.

The browser fetches directly against the gateway, so the lantern
service sets `LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080`.
If you map the admin to a different external port / host, override
that env to match (otherwise the browser preflight blocks the
request). Override the image with
`LANTERN_ADMIN_IMAGE=ghcr.io/anaregdesign/lantern-admin:v0.1.1
docker compose up -d`.

The admin container is **not** auth-fronted. Run it only on trusted
networks, or put your own ingress-level auth proxy in front.

## Verifying peer discovery

```shell
# Each replica should log "peer_discovery" lines with the other 2 IPs.
docker compose logs lantern | grep peer_discovery

# Prometheus (http://localhost:9091):
#   lantern_replication_peer_up         — 1 gauge per active peer link
#   lantern_replication_lag             — per (peer, origin) lag
```

## Single-instance fallback

For non-HA development just override the replicas to 1:

```shell
docker compose up -d --scale lantern=1
```

`LANTERN_PEER_DNS_NAME=lantern` still resolves — to a single A record
which `LocalIPSet()` filters as self, so the pump becomes a no-op.

## Cross-references

- Helm chart (production path): [`../helm/lantern/`](../helm/lantern/)
- Peer discovery spec: [`../../docs/replication.md` §9.1](../../docs/replication.md#91-peer-discovery-190)
- HA runbook: issue #192 (in flight).
- Testbed (single-instance observability QA): [`../../testbed/`](../../testbed/)

## Single-node + MCP server

For local LLM-agent experiments there is a smaller compose file in
[`docker-compose.mcp.yml`](docker-compose.mcp.yml) that stands up one
`lantern` + one `lantern-mcp`. The HA compose file in this directory
also ships a `lantern-mcp` service behind a Compose profile, so the
same MCP container can be exercised against the 3-replica cluster:

```shell
# Single-node (small file): bring lantern up in the background, then
# attach the MCP container interactively.
docker compose -f docker-compose.mcp.yml up -d lantern
docker compose -f docker-compose.mcp.yml run --rm lantern-mcp

# HA cluster (this file): same lantern-mcp container, but talking to
# the 3-replica cluster — Compose DNS round-robins `lantern:6380`
# across the live replicas.
docker compose --profile mcp run --rm lantern-mcp
```

`docker compose up` is the wrong verb in both cases — the MCP server
is **stdio-only** (`mcp/cmd/main.go` mounts `&mcp.StdioTransport{}`),
so backgrounding the container would leave nothing consuming stdout.

In production, the agent runtime (Claude Desktop, VS Code, Cursor, …)
owns the MCP container's lifetime — see
[`../../mcp/examples/`](../../mcp/examples/) for those configs and
[`../../mcp/README.md`](../../mcp/README.md) for the full operator
reference.

> The standalone `docker-compose.mcp.yml` covers the single-node case
> and stays useful as a template for embedding `lantern-mcp` in your
> own agent-runtime compose file. The HA file's `mcp` profile covers
> the multi-replica case without duplicating the lantern + Prometheus
> stack.
