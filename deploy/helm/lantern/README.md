# lantern (Helm chart)

Tier-A HA deployment of [lantern](https://github.com/anaregdesign/lantern)
on Kubernetes: a `StatefulSet` of 3 pods behind a headless `Service` for
DNS-based peer discovery (see [docs/replication.md §9.1](../../../docs/replication.md))
plus a `ClusterIP` `Service` for clients, guarded by a `PodDisruptionBudget`.

## Quick install

```shell
helm install lantern deploy/helm/lantern
```

For a one-off render:

```shell
helm template lantern deploy/helm/lantern | less
```

To lint:

```shell
helm lint deploy/helm/lantern
```

## What gets deployed

| Object                                | Purpose                                                |
| ------------------------------------- | ------------------------------------------------------ |
| `StatefulSet/<release>-lantern`       | 3 lantern pods (default), `RollingUpdate` strategy.    |
| `Service/<release>-lantern`           | `ClusterIP` — client gRPC + scrapeable `/metrics`.     |
| `Service/<release>-lantern-headless`  | `clusterIP: None` — peer discovery DNS.                |
| `PodDisruptionBudget/<release>-lantern` | `minAvailable: 2` to keep quorum during drains.      |
| `ServiceAccount/<release>-lantern`    | Workload identity (token-only by default).             |
| `ServiceMonitor/<release>-lantern`    | Optional, when `metrics.serviceMonitor.enabled=true`.  |

The pump reads `LANTERN_PEER_DISCOVERY=dns` and resolves the headless
`Service` FQDN to obtain peer IPs. `LocalIPSet()` in the server filters
the pod's own IP so the supervisor never dials itself.

## Single-instance fallback

For dev / single-instance topologies set `replicaCount=1` and either
keep `replication.discovery.mode=dns` (the DNSSource will resolve to
just the local pod and filter it out — pump becomes a no-op) or set
`replication.discovery.mode=static` with `replication.peers=[]`. PDB
can be disabled via `podDisruptionBudget.enabled=false`.

For serverless container PaaS (Cloud Run / Azure Container Apps /
App Runner) there is no peer topology — do **not** use this chart;
deploy the container directly without `LANTERN_PEER_*` env. See the
HA runbook (#192) for the limits.

## Tuning

| Value                                       | Default                | Notes                                              |
| ------------------------------------------- | ---------------------- | -------------------------------------------------- |
| `replicaCount`                              | `3`                    | Tier-A HA default.                                 |
| `image.repository`                          | `ghcr.io/anaregdesign/lantern` | Override for private mirrors.              |
| `image.tag`                                 | `.Chart.AppVersion`    | Pin to a specific release tag in production.       |
| `service.port`                              | `6380`                 | gRPC.                                              |
| `metrics.port`                              | `9090`                 | `/metrics`, `/healthz`, `/readyz`.                 |
| `replication.discovery.mode`                | `dns`                  | `static` falls back to `LANTERN_PEERS`.            |
| `replication.discovery.dnsName`             | headless FQDN          | Auto-templated. Override only for cross-ns peers.  |
| `replication.discovery.defaultPort`         | `"6380"`               | Appended to each resolved IP.                      |
| `replication.discovery.intervalMs`          | `10000`                | `0` = resolve once at startup.                     |
| `replication.maxLag`                        | `10000`                | Per-(peer,origin) lag cap before readiness flips.  |
| `antiEntropy.intervalMs`                    | `30000`                | Background reconciliation cadence.                 |
| `podDisruptionBudget.minAvailable`          | `2`                    | Quorum-ish.                                        |
| `metrics.serviceMonitor.enabled`            | `false`                | Requires the prometheus-operator CRD.              |

See [`values.yaml`](values.yaml) for the full set including probes,
resources, security context, anti-affinity, and `extraEnv`.

## Smoke test

```shell
kubectl -n default get pods -l app.kubernetes.io/name=lantern
kubectl -n default get endpoints <release>-lantern-headless
kubectl -n default port-forward svc/<release>-lantern 6380:6380
# In another terminal:
lantern put-vertex --addr localhost:6380 key1 value1
lantern get-vertex --addr localhost:6380 key1
```

Then scale and observe convergence:

```shell
kubectl -n default scale statefulset <release>-lantern --replicas=5
# Within one discovery tick (default 10s) each pod resolves all 5,
# filters self, and opens replication streams to the other 4.
```

## Verifying peer discovery

```shell
# All pods should see N-1 peer addresses in their logs.
kubectl -n default logs <release>-lantern-0 | grep peer_discovery

# Prometheus: lantern_replication_peer_up{peer="..."} gauge series
# should show one per resolved peer, transitioning 0→1 on stream open.
```

## Cross-references

- RFC: [docs/replication.md](../../../docs/replication.md)
- Discovery spec: [docs/replication.md §9.1](../../../docs/replication.md#91-peer-discovery-190)
- Docker Compose alternative: [`deploy/compose/`](../../compose/)
- HA runbook: see issue #192 (in flight).
