# Docker Compose: HA lantern cluster

3-replica peer-discovery cluster (Tier-A) on a single Docker host. Mirrors
the Helm chart in [`../helm/lantern/`](../helm/lantern/) for local dev and
single-host HA experiments.

## Topology

- 3 × explicit lantern services — `lantern-0`, `lantern-1`, `lantern-2` —
  on host ports **`6380`**, **`6381`**, **`6382`** respectively. Each
  service is its own entry (no `deploy.replicas`) so the host-port
  mapping is pinned across restarts; this is what `#435` fixes. The
  admin SPA's default gateway `http://localhost:6380` therefore keeps
  working out of the box.
- All three services share the network alias `lantern`. Compose's
  embedded DNS resolves `lantern` to one A per healthy replica, so the
  peer pump (#190) — and any SDK / MCP container dialing
  `http://lantern:6380` — continues to round-robin across the live
  replicas without knowing about the per-service names.
- 1 × `admin` browser SPA (`ghcr.io/anaregdesign/lantern-admin`)
  on host port `8080`. Caddy SPA host on `:8080`; the browser talks to
  the gateway directly (not proxied), so each lantern service has
  `LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080` baked in. The
  admin container *does* reverse-proxy Prometheus same-origin under
  `/api/prom` (`LANTERN_ADMIN_PROMETHEUS_UPSTREAM=http://prometheus:9090`
  is set on the admin service) so the Ops Metrics charts work without a
  cross-origin call to Prometheus.
- `prometheus` scrapes via `dns_sd_configs` against the `lantern`
  alias, so it picks up every replica automatically (no static targets
  file to maintain).

| service     | host port → container | compose container name              |
|-------------|-----------------------|-------------------------------------|
| `lantern-0` | `6380` → `6380`       | `${COMPOSE_PROJECT_NAME}-lantern-0-1` |
| `lantern-1` | `6381` → `6380`       | `${COMPOSE_PROJECT_NAME}-lantern-1-1` |
| `lantern-2` | `6382` → `6380`       | `${COMPOSE_PROJECT_NAME}-lantern-2-1` |

Use the service name (`lantern-0`, ...) when invoking
`docker compose` subcommands; use the full container name only when
shelling out to `docker` directly (e.g. `docker inspect`,
`docker logs --details`). The project name defaults to the directory
basename (`compose`).

## Run

```shell
cd deploy/compose
docker compose up -d
docker compose ps
```

Tear down:

```shell
docker compose down -v
```

> `docker compose up -d --scale lantern=N` no longer applies — the
> canonical compose uses three explicit services so the host-port
> mapping stays stable across restarts (#435). For >3 replicas, add a
> `lantern-3` block (host port `6383`, etc.) using the
> `x-lantern-common` YAML anchor, or switch to the Helm chart in
> [`../helm/lantern/`](../helm/lantern/) which has no host-port
> constraint.

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

The **Ops** page's Prometheus time-series charts query Prometheus
same-origin under `/api/prom`; the admin service's
`LANTERN_ADMIN_PROMETHEUS_UPSTREAM=http://prometheus:9090` makes the
admin container reverse-proxy that path to the bundled `prometheus`
service, so the charts render out of the box. Point the **Prometheus**
button in the Metrics toolbar elsewhere to override at runtime.

The admin container is **not** auth-fronted. Run it only on trusted
networks, or put your own ingress-level auth proxy in front.

## Verifying peer discovery

```shell
# Each replica should log "peer_discovery" lines with the other 2 IPs.
docker compose logs lantern-0 lantern-1 lantern-2 | grep peer_discovery

# Prometheus (http://localhost:9091):
#   lantern_replication_peer_up         — 1 gauge per active peer link
#   lantern_replication_lag             — per (peer, origin) lag
```

## Single-instance fallback

For non-HA development bring up only one replica:

```shell
docker compose up -d lantern-0 admin prometheus
```

`LANTERN_PEER_DNS_NAME=lantern` still resolves — to the single A
record for `lantern-0`, which `LocalIPSet()` filters as self, so the
pump becomes a no-op.

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
# Single-node (small file): bring lantern + the MCP server up together.
docker compose -f docker-compose.mcp.yml up -d

# HA cluster (this file): same lantern-mcp container, but talking to
# the 3-replica cluster — Compose DNS round-robins `lantern:6380`
# across the live replicas.
docker compose --profile mcp up -d lantern-mcp
```

`lantern-mcp` serves the Model Context Protocol over **Streamable
HTTP**, so it's a long-lived service: bring it up with `up -d` and
point your agent at `http://localhost:6390/mcp` (a `GET /healthz`
returns `200 ok` for probes).

In production, the agent runtime (Claude Desktop, VS Code, Cursor, …)
connects to that URL — see
[`../../mcp/examples/`](../../mcp/examples/) for those configs and
[`../../mcp/README.md`](../../mcp/README.md) for the full operator
reference.

> The standalone `docker-compose.mcp.yml` covers the single-node case
> and stays useful as a template for embedding `lantern-mcp` in your
> own agent-runtime compose file. The HA file's `mcp` profile covers
> the multi-replica case without duplicating the lantern + Prometheus
> stack.
