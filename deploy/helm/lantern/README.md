# lantern (Helm chart)

Minimal, cost-optimised deployment of
[lantern](https://github.com/anaregdesign/lantern) on Kubernetes — tuned for
**GKE Autopilot**: a `StatefulSet` of 2 pods behind a headless `Service` for
DNS-based peer discovery (see [docs/replication.md §9.1](../../../docs/replication.md))
plus a `ClusterIP` `Service` for in-cluster clients, guarded by a
`PodDisruptionBudget`. Lantern is a full-replica store, so 2 replicas give
rolling-update / single-node-drain survival at the lowest footprint. The
Service is ClusterIP-only — Lantern is never exposed outside the cluster;
reach it from other pods via its Service FQDN, or from a laptop with
`kubectl port-forward` for verification. Admin, MCP and Prometheus are
expected to run locally and are **not** deployed by this profile.

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
| `StatefulSet/<release>-lantern`       | 2 lantern pods (default), `RollingUpdate` strategy.    |
| `Service/<release>-lantern`           | `ClusterIP` — client gRPC + scrapeable `/metrics`.     |
| `Service/<release>-lantern-headless`  | `clusterIP: None` — peer discovery DNS.                |
| `PodDisruptionBudget/<release>-lantern` | `minAvailable: 1` keeps one replica up during a drain. |
| `ServiceAccount/<release>-lantern`    | Workload identity (token-only by default).             |
| `ServiceMonitor/<release>-lantern`    | Optional, when `metrics.serviceMonitor.enabled=true` (Prometheus Operator). |
| `PodMonitoring/<release>-lantern`     | Optional, when `metrics.podMonitoring.enabled=true` (GKE Managed Prometheus). |

The pump reads `LANTERN_PEER_DISCOVERY=dns` and resolves the headless
`Service` FQDN to obtain peer IPs. `LocalIPSet()` in the server filters
the pod's own IP so the supervisor never dials itself. The headless Service
publishes not-ready addresses so peers can discover one another during a cold
start; the separate client Service continues to exclude unready pods.

## Single-instance fallback

For dev / single-instance topologies set `replicaCount=1`,
`replication.discovery.mode=static`, and `replication.peers=[]`. DNS discovery
selects HA readiness mode and therefore intentionally waits for a peer. PDB can
be disabled via `podDisruptionBudget.enabled=false`.

## Tuning

| Value                                       | Default                | Notes                                              |
| ------------------------------------------- | ---------------------- | -------------------------------------------------- |
| `replicaCount`                              | `2`                    | Minimal HA default (full-replica store).           |
| `image.repository`                          | `ghcr.io/anaregdesign/lantern` | Override for private mirrors.              |
| `image.tag`                                 | `.Chart.AppVersion`    | Pin to a specific release tag in production.       |
| `enableServiceLinks`                        | `false`                | Disable unused Kubernetes ServiceLink env injection; enable only for legacy ServiceLink discovery. |
| `service.port`                              | `6380`                 | gRPC.                                              |
| `metrics.port`                              | `9090`                 | `/metrics`, `/healthz`, `/readyz`.                 |
| `replication.discovery.mode`                | `dns`                  | `static` falls back to `LANTERN_PEERS`.            |
| `replication.discovery.dnsName`             | headless FQDN          | Auto-templated. Override only for cross-ns peers.  |
| `replication.discovery.defaultPort`         | `"6380"`               | Appended to each resolved IP.                      |
| `replication.discovery.intervalMs`          | `10000`                | `0` = resolve once at startup.                     |
| `replication.maxLag`                        | `10000`                | Per-(peer,origin) lag cap before readiness flips.  |
| `antiEntropy.intervalMs`                    | `30000`                | Background reconciliation cadence.                 |
| `podDisruptionBudget.minAvailable`          | `1`                    | Keep one replica up while the other drains (correct for 2 replicas). |
| `backup.enabled`                            | `true`                 | Snapshot durability (#770/#779): per-pod dump PVC + restore-on-start baseline (peers overlay it via HLC). Needs a default StorageClass or `backup.persistence.storageClass`. |
| `backup.interval`                           | `5m`                   | Dump cadence; keep `cache.defaultTtlSeconds` above it.             |
| `backup.persistence.size`                   | `1Gi`                  | Per-pod PVC size for dumps.                        |
| `backup.persistence.existingClaim`          | `""`                   | Set to a pre-provisioned RWX claim for a shared dump volume. |
| `resources.requests` / `.limits`            | `250m` CPU / `512Mi`   | requests == limits (Autopilot Guaranteed QoS). 250m is the Autopilot min; 512Mi the server floor. |
| `probes.startup`                            | 60s initial delay, 5s period, 36 failures | Gives restore-on-start about four minutes before restart; liveness/readiness stay disabled until it succeeds. |
| `metrics.serviceMonitor.enabled`            | `false`                | Requires the prometheus-operator CRD.              |
| `metrics.podMonitoring.enabled`             | `false`                | GKE Managed Service for Prometheus (GMP). Requires the `monitoring.googleapis.com/v1` PodMonitoring CRD (default on GKE). |
| `metrics.podMonitoring.interval`            | `60s`                  | GMP scrape interval.                               |
| `admin.enabled`                             | `false`                | Render the `lantern-admin` SPA Deployment + Service. |
| `admin.image.repository`                    | `ghcr.io/anaregdesign/lantern-admin` | Admin SPA image (Caddy serving the built bundle).     |
| `admin.image.tag`                           | `.Chart.AppVersion`    | Pin to an `admin/vX.Y.Z` tag in production.        |
| `admin.service.port`                        | `8080`                 | Service ClusterIP port for the SPA.                |
| `admin.ingress.enabled`                     | `false`                | Render an Ingress for the admin Service.           |
| `admin.ingress.host`                        | `""` (required when enabled) | Host name the Ingress rule matches.        |

See [`values.yaml`](values.yaml) for the full set including probes,
resources, security context, anti-affinity, and `extraEnv`.

## GMP cost guard

[Managed Service for Prometheus bills primarily by ingested
samples](https://cloud.google.com/stackdriver/docs/managed-prometheus/cost-controls).
The default `PodMonitoring.metricRelabeling` therefore keeps the production
signals used for HA, readiness, backups, capacity/resource health,
search-index health, validation, subscriptions, and gRPC RED monitoring. It
drops the high-cardinality Illuminate and detailed search/scan/batch families
from GMP only; Lantern's local `/metrics` endpoint remains complete for
short-lived diagnosis or a self-managed Prometheus.

The July 2026 two-pod production sample exposed 14,604 total series. At a
60-second interval that is an upper bound of 630,892,800 samples per 30-day
month. The default allowlist retained 425 series on the busier pod, reducing
the two-pod upper bound to approximately 36.7 million samples per month (about
94% fewer). Histogram billing can be lower because GMP counts only populated
buckets, so treat these figures as conservative planning bounds. Re-measure
after adding labels or metric families.

To ingest the full surface deliberately, override the list:

```shell
helm upgrade --install lantern deploy/helm/lantern \
  --set metrics.podMonitoring.enabled=true \
  --set-json 'metrics.podMonitoring.metricRelabeling=[]'
```

Increasing `metrics.podMonitoring.interval` reduces sample cost linearly but
also delays short HA signals. The maintained production profile keeps `60s`
and controls cost by cardinality instead.

## Admin UI (`admin.enabled=true`)

The browser-facing admin SPA (`lantern-admin`) ships as part of this
chart but is **disabled by default** to preserve the existing install
footprint.

```shell
helm upgrade --install lantern deploy/helm/lantern \
  --set admin.enabled=true \
  --set extraEnv[0].name=LANTERN_CORS_ALLOWED_ORIGINS \
  --set extraEnv[0].value=http://localhost:8080
kubectl port-forward svc/lantern-admin 8080:8080
# Open http://localhost:8080/ — the Gateway button (top-right) points
# at the lantern service; default is http://localhost:6380, override
# at runtime.
```

The admin container is a **pure Caddy SPA host** — the browser calls
the lantern listener (Connect-Web) directly, so the lantern server
**must** allow the admin origin via `LANTERN_CORS_ALLOWED_ORIGINS`.
Set it on the server StatefulSet via `extraEnv` (above) to include
every origin the SPA may be served from (`http://localhost:8080`
during port-forward dev, `https://<ingress-host>` once an Ingress is
configured, …). Multiple origins are comma-separated.

### Ingress

```shell
helm upgrade --install lantern deploy/helm/lantern \
  --set admin.enabled=true \
  --set admin.ingress.enabled=true \
  --set admin.ingress.host=admin.example.com \
  --set admin.ingress.className=nginx \
  --set extraEnv[0].name=LANTERN_CORS_ALLOWED_ORIGINS \
  --set extraEnv[0].value=https://admin.example.com
```

`admin.ingress.host` is required when `admin.ingress.enabled=true`;
the template fails fast (`helm: …admin.ingress.host to be set`) if it
is omitted. For TLS, set `admin.ingress.tls` to the standard
networking.k8s.io/v1 Ingress TLS slice. For multi-host setups, fork
`templates/admin-ingress.yaml` — v1 of the chart deliberately only
covers the single-host happy path.

### No auth

v1 ships **no** auth in front of the admin (same posture as the
container image's `admin/README.md`). Run it only on trusted networks
(`admin.ingress.enabled=false` + `kubectl port-forward`) or front it
with your own ingress-level auth proxy.

## MCP server

`lantern-mcp` serves the Model Context Protocol over **Streamable HTTP**,
so the chart renders a standard **Deployment + ClusterIP Service** for it
(mirroring the admin pattern), gated on `mcp.enabled` (default `false`):

```shell
helm upgrade --install lantern deploy/helm/lantern \
  --set mcp.enabled=true \
  --set mcp.image.tag=v0.4.0
```

Agent pods in the same cluster reach it at:

```
http://<release>-mcp.<namespace>.svc:6390/mcp
```

The container listens on `0.0.0.0:6390` (set automatically via
`LANTERN_MCP_HTTP_ADDR`) and dials the in-cluster lantern Service by
default. A `GET /healthz` backs the liveness / readiness probes.

The MCP config lives under `.Values.mcp`:

| Value | Default | Purpose |
| --- | --- | --- |
| `mcp.enabled` | `false` | Render the Deployment + Service. |
| `mcp.replicaCount` | `1` | MCP server replicas. |
| `mcp.image.repository` | `ghcr.io/anaregdesign/lantern-mcp` | Override for private mirrors. |
| `mcp.image.tag` | `.Chart.AppVersion` | Pin to a specific `mcp/vX.Y.Z`. |
| `mcp.service.type` | `ClusterIP` | Keep cluster-internal — the endpoint is unauthenticated. |
| `mcp.service.port` | `6390` | Service + container port (endpoint `/mcp`). |
| `mcp.lanternAddr` | _(empty → in-cluster Service FQDN)_ | Override only for cross-namespace / cross-cluster setups. |
| `mcp.pingTimeout` | `5s` | Startup health-check timeout. |
| `mcp.ttl.<bucket>` | _(unset)_ | Per-bucket TTL override; rendered as `LANTERN_MCP_TTL_<UPPER>` env. |
| `mcp.resources` | small | requests/limits for the container. |
| `mcp.extraEnv` | `[]` | Raw env list appended to the templated ones. |

The endpoint is **unauthenticated** — keep `mcp.service.type=ClusterIP`
and reach it from agent pods in the same cluster, or front it with your
own ingress-level auth proxy. For local probing use
`kubectl port-forward svc/<release>-mcp 6390:6390` and connect to
`http://localhost:6390/mcp`. The agent-runtime client configs in
[`mcp/examples/`](../../../mcp/examples/) show the host-side wiring.


## Smoke test

```shell
kubectl -n default get pods -l app.kubernetes.io/name=lantern
kubectl -n default get endpoints <release>-lantern-headless
kubectl -n default port-forward svc/<release>-lantern 6380:6380
# In another terminal:
lantern-cli --address localhost:6380 put vertex key1 value1
lantern-cli --address localhost:6380 get vertex key1
```

Then scale and observe convergence:

```shell
kubectl -n default scale statefulset <release>-lantern --replicas=5
# Within one discovery tick (default 10s) each pod resolves all 5,
# filters self, and opens replication streams to the other 4.
```

## Verifying peer discovery

```shell
# Each pod logs a "replication pump: peer transition" line (transition=connect,
# peer=<addr>) for each of the other N-1 peers as streams open.
kubectl -n default logs <release>-lantern-0 | grep "peer transition"

# Prometheus: lantern_peer_connected{peer="..."} gauge series
# should show one per resolved peer, transitioning 0→1 on stream open.
```

## Cross-references

- RFC: [docs/replication.md](../../../docs/replication.md)
- Discovery spec: [docs/replication.md §9.1](../../../docs/replication.md#91-peer-discovery-190)
- Docker Compose alternative: [`deploy/compose/`](../../compose/)
- HA runbook: [docs/ha-runbook.md](../../../docs/ha-runbook.md)
