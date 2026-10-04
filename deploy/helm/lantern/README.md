# Lantern Helm chart

The default is one OFF Server with graph backups, an internal public Service,
and loopback diagnostics. Authentication has no zero-config dependency on an
IdP or security Store. Multiple replicas require explicit private peer material.

```sh
helm lint deploy/helm/lantern
helm template local deploy/helm/lantern
helm install lantern deploy/helm/lantern --namespace lantern --create-namespace
```

Publication of an image/chart does not deploy it. Provision and validate the
operator material before installation. Generated examples contain no secrets.

## Private peer plane

Set `peerPlane.enabled=true` and supply the signed membership ConfigMap,
operator public-key Secret, deployment ID, workload URI prefix and identity
Secret. The latter has `<pod-name>.crt`, `<pod-name>.key` and `ca.pem`; every
pod owns a distinct private key. A trusted non-root initContainer copies only
its Pod's key/certificate and public trust material into a memory-backed
EmptyDir. Raw cohort Secret projections are never mounted into the Server. The ConfigMap has `manifest.json`, renewed
atomically before its expiry (at most ten minutes). DNS/headless Services locate
only signed private origins; they do not authorize membership.

The peer Service uses its own port (6381), includes bootstrapping pods and
never exposes the public APIs. The client Service remains readiness gated.
Choose `peerPlane.mode=fresh` only for first enrollment and `resume` afterward.
Per-pod `runtime-state` PVCs retain anti-rollback enrollment and sys: state.
Never use an emptyDir or copied state file as proof of continuity.

## OIDC/RBAC

Set `auth.mode=oidc`, `auth.configMap`, `auth.existingSecret` and
`publicTLS.existingSecret`. The ConfigMap supplies the complete
[Server contract](../../../docs/env.md). Provision a separate minimal source
Secret for each writer/replica release. `auth.files` explicitly selects common
files (default `writer.pub`, `cdc-keys.json`); `auth.writerFiles` selects files
copied only for a writer (default `writer.key`; add `machines.json` when machine
bootstrap is configured). Map ConfigMap file settings and Issuer secret handles
to those selected basenames under `/run/lantern-security`. A replica's source
Secret must exclude writer signing keys and machine bootstrap credentials.

The initContainer dereferences Kubernetes projections into regular mode-0600
files owned by the Server UID. It uses the same Server image and securityContext;
a non-root UID and fsGroup are required. Public TLS files are
`<pod-name>.crt`/`<pod-name>.key` under `/run/lantern-public`, independently of
private peer identity under `/run/lantern-peers`. Missing selected files fail
before Server startup. Never place the operator private signing key in a Pod.

Copied key/secret rotation requires an explicit Pod restart after provisioning
and validating replacement material. Signed membership stays on its live
ConfigMap projection and must be renewed independently before expiry; it is
never frozen by the init copy. Keep restart/fencing and security continuity
requirements intact. Kubernetes documents [initContainer volume sharing](https://kubernetes.io/docs/concepts/workloads/pods/init-containers/)
and [memory-backed EmptyDir and Secret volumes](https://kubernetes.io/docs/concepts/storage/volumes/).

Use `auth.nodeRole=writer` with exactly one pod for the fixed security writer.
Deploy a separate replica release with `auth.nodeRole=replica` and private peer
membership in the same domain. The chart does not infer the writer from pod
ordinal or elect one. `auth.storeMode=fresh` initializes new state;
`restart` validates existing sys: continuity. Required bootstrap administrator
Issuer and subjects are environment-owned; Admin can manage additional Issuers,
users and Role assignments through Server APIs.

Public OFF/OIDC mode, generation, namespace version and trust must be homogeneous
throughout approved membership. Changing them requires a fenced migration, not
a mixed anonymous/protected rolling update. Replication of sys: data alone does
not prove current policy: readiness also requires workload and security leases.

## Admin and diagnostics

`admin.enabled=true` mounts a writable Caddy configuration directory. OFF derives
a fixed public upstream; OIDC requires `admin.server.upstream` to name the pinned
HTTPS writer. Set `admin.server.caSecret` (`ca.pem`) for a private CA. Browser
sessions use same-origin routes, HttpOnly cookies and CSRF; no tokens go in Vite
configuration. An enabled OIDC Ingress requires TLS.

`admin.prometheus.upstream` is optional. Each request first calls the fixed
Server `/auth/operations` route; OFF permits it explicitly, OIDC requires a
current `operations.read` Role. API/proxy errors never become the SPA shell.
Only GET diagnostics are proxied, and cookies/Bearer headers are stripped before
reaching Prometheus.

Protected diagnostics bind to loopback. Probes execute local health/readiness
checks so they do not require a public diagnostics port. For OFF-only collection,
explicitly set `metrics.expose=true` and a reachable `metrics.address` before
enabling ServiceMonitor/PodMonitoring. OIDC collection requires an authenticated
operator sidecar, outside the chart's default public Service. Keep its readiness
and scrape permissions scoped separately.

## Runtime and recovery

The chart retains cache/capacity/backup settings, drain delay, startup budget,
resource limits and `runtime.goMemoryLimit`. Graph backups and sys:/membership
PVCs are separate. Receipt-WAL configuration is an additional certified runtime
choice; graph snapshots never certify receipt or security continuity.

The optional MCP deployment is a separate application. Protected deployments
must supply an approved machine Role, credential and verified public transport;
an OFF machine example does not authorize private replication.

See [OIDC operations](../../../docs/oidc-operations.md),
[HA runbook](../../../docs/ha-runbook.md) and
[replication RFC](../../../docs/replication.md).
