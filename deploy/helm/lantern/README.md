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
| `admin.enabled`                             | `false`                | Render the `lantern-admin` SPA Deployment + Service. |
| `admin.image.repository`                    | `ghcr.io/anaregdesign/lantern-admin` | Admin SPA image (Caddy serving the built bundle).     |
| `admin.image.tag`                           | `.Chart.AppVersion`    | Pin to an `admin/vX.Y.Z` tag in production.        |
| `admin.service.port`                        | `8080`                 | Service ClusterIP port for the SPA.                |
| `admin.ingress.enabled`                     | `false`                | Render an Ingress for the admin Service.           |
| `admin.ingress.host`                        | `""` (required when enabled) | Host name the Ingress rule matches.        |

See [`values.yaml`](values.yaml) for the full set including probes,
resources, security context, anti-affinity, and `extraEnv`.

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

## MCP sidecar

The chart **does not render a Pod / Deployment for `lantern-mcp`**.
MCP is a stdio protocol (the binary mounts `&mcp.StdioTransport{}`
in `mcp/cmd/main.go`), so a free-standing Service shape doesn't fit:
there's no socket to expose and no obvious client to attach to. The
canonical deployment shape is a **sidecar inside the agent runtime's
pod** (Claude Desktop / VS Code Server / a custom worker).

To make that easy, the chart exports a named template,
`lantern.mcpSidecar`, that emits a container spec your own
chart / manifest can splice in:

```yaml
# my-agent/templates/pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-agent
spec:
  containers:
    - name: agent
      image: my-agent:latest
      stdin: true
      tty: false
    {{- include "lantern.mcpSidecar" . | nindent 4 }}
```

The sidecar reads its config from `.Values.mcp`:

| Value | Default | Purpose |
| --- | --- | --- |
| `mcp.image.repository` | `ghcr.io/anaregdesign/lantern-mcp` | Override for private mirrors. |
| `mcp.image.tag` | `.Chart.AppVersion` | Pin to a specific `mcp/vX.Y.Z`. |
| `mcp.lanternAddr` | _(empty → in-cluster Service FQDN)_ | Override only for cross-namespace / cross-cluster setups. |
| `mcp.pingTimeout` | `5s` | Startup health-check timeout. |
| `mcp.ttl.<bucket>` | _(unset)_ | Per-bucket TTL override; rendered as `LANTERN_MCP_TTL_<UPPER>` env. |
| `mcp.resources` | small | requests/limits for the sidecar container. |
| `mcp.extraEnv` | `[]` | Raw env list appended to the templated ones. |

Real production traffic happens over stdio inside the pod; for
manual probes use `kubectl exec -it <pod> -c lantern-mcp` which
hands you the same stdio channel the agent uses.

If you actually want a free-standing `lantern-mcp` Deployment (e.g.
for `kubectl attach` exploration), the agent-runtime configs in
[`mcp/examples/`](../../../mcp/examples/) show the pattern — fork
them into your own chart rather than complicating this one.

If an HTTP/SSE transport lands in the upstream `go-sdk`, the chart
will gain a proper `mcp-deployment.yaml` + `mcp-service.yaml`; tracked
as a follow-up.

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
