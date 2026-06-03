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

- **Go SDK** (`#189`): pass all replica endpoints; the SDK uses
  gRPC's `round_robin` LB policy.
  ```go
  c, _ := lantern.NewLanternWithEndpoints([]string{
      "localhost:6380", "localhost:6381", "localhost:6382",
  })
  ```
- **CLI / grpcurl**: hit any pod directly; writes propagate via the
  peer pump.
- **Your own LB** (nginx-plus stream `resolve`, Envoy, HAProxy with
  DNS resolution): point upstream at `lantern:6380`.

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
