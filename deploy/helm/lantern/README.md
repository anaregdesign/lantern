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

Private peers (including OFF) and OIDC require `nativeTime.configMap`. Supply its
public `ntp.conf` key with exactly `server time.asia.apple.com`; the chart mounts
it read-only at `/etc/ntp.conf`. This declares the existing admitted source, not
arbitrary NTP support or an OS time-service change. Linux amd64/arm64 and Darwin
arm64 qualified private OFF each own one clock; OIDC borrows its existing owner.
Missing/invalid input refuses on those targets. Other existing OFF/native-TLS
platforms retain the guarded scalar path and its rollback limitation; OIDC's
platform scope is unchanged. Standalone OFF adds no owner or source dependency.


## OIDC/RBAC

Set `auth.mode=oidc`, a verified public TLS Secret, and `auth.configMap` with
`LANTERN_OIDC_BROWSER_ORIGIN` plus any required OIDC fetch/secret bindings.
`auth.existingSecret` holds independent original provisioning inputs. List every
required basename in `auth.files`, including `auth.nodeFile` (default `node.json`).
The source Secret key for each file is `<pod-name>.<basename>`: for example,
`lantern-0.node.json`. The trusted init step copies only that pod's files into
owner-only regular files under `/run/lantern-security`. Reference those exact
paths from the node document; persistent journal and custody paths belong under
`runtimeState.dir`. An originless voter omits its origin signing file.

The node/genesis documents bind the original voter set, separate origin keys,
workload identities, resource bounds and native time profile. Provision a fixed
set of 1 through 31 voters, with majority `floor(N/2)+1`; even counts are valid.
Odd counts usually provide the same failure tolerance with one fewer voter.
The chart's data replica count cannot add/remove control voters or provision
identities. Every authenticated Server must have an original voter identity.

Use `auth.storeMode=fresh` once for an empty state family and `resume` for intact
recovery with its independent custody/minimum floors. Never initialize over
retained state, relabel an old fixed-writer journal, or reuse one pod's keys for
another. Existing `auth.nodeRole`, `auth.writerFiles`, profile selectors and old
writer/bootstrap environment settings are rejected. No migration is provided.
OFF remains the default and needs no authentication material; private data
replication still requires its independent signed membership and mTLS material.

## Admin and diagnostics

`admin.enabled=true` mounts a writable Caddy configuration directory. OFF derives
a fixed public upstream; OIDC requires `admin.server.upstream` to name the pinned
HTTPS authority origin. Set `admin.server.caSecret` (`ca.pem`) for a private CA. Browser
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
