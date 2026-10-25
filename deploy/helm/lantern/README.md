# Lantern Helm chart

The default is one OFF Server with graph backups, an internal public Service,
and loopback diagnostics. Authentication has no zero-config dependency on an
IdP or security Store. Multiple replicas require OFF peer material or the fixed OIDC identity topology.

```sh
helm lint deploy/helm/lantern
helm template local deploy/helm/lantern
helm install lantern deploy/helm/lantern --namespace lantern --create-namespace
```

Publication of an image/chart does not deploy it. Real Kubernetes acceptance
remains **0/1, held** in [#1636](https://github.com/anaregdesign/lantern/issues/1636).
This source migration does not resume cluster work or qualify renewal delivery.
Provision and validate inputs separately; examples contain no real secrets.

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

Set `auth.mode=oidc`, `auth.configSecret` and `auth.existingSecret`. The former
contains the same complete human JSON used for provisioning, explicit first
initialization and every start, under key `<pod-name>.json`. The latter contains
only fixed per-workload identity material. Current provider credentials, public
TLS/proxy settings and desired Issuers belong in the supplied JSON, never in a
saved generated runtime configuration. OIDC rejects `publicTLS.existingSecret`
and generic OFF `peerPlane.enabled`/credential inputs; its workload identity is
already bound to the retained material.

The trusted non-root init step selects only its Downward-API Pod name. It copies
that Pod's user JSON to owner-private ephemeral `/run/lantern-input/config.json`
and identity material to `/run/lantern-security/nodes/<pod-name>/`. The Server
mounts only those private copies read-only; raw cohort Secret projections are
visible only to the trusted setup container. Every private directory is `0700`,
regular file `0600`, with the same effective non-root owner. This input copy is
an operator-supplied **Pod snapshot**, not a server-derived configuration or
fallback. Container-only restart rereads that required snapshot. A changed
`auth.configSecret` requires deliberate **Pod replacement** to supply a new
snapshot; no live Secret/config reload is promised.

`auth.files` must explicitly allow exactly these eight relative identity paths:

```yaml
auth:
  mode: oidc
  configSecret: current-human-config
  existingSecret: fixed-identity-material
  controlPort: 16380
  files:
    - identity.json
    - COMPLETE
    - workload.pem
    - workload-key.pem
    - roots.pem
    - data-operator.pem
    - membership/control.json
    - membership/data.json
```

Each identity Secret key is `<pod-name>.<relative-path-with-slashes-replaced-by-dots>`;
for example `lantern-0.identity.json` and
`lantern-0.membership.control.json`. Unknown paths, duplicates, traversal and
operator signing keys are refused. There is no old `auth.storeMode`, `nodeFile`
or `configMap` input; even empty retired settings are rejected.

The supplied JSON must select its exact Pod name as `node`, declare
`material_root: /run/lantern-security`, and use `persistence_root` equal to
`runtimeState.dir`. Preserve those original target paths during provisioning
and installation. Per-pod PVCs retain native state under the derived node path;
no init-container action creates or resets native M/P/B/custody state. Perform
explicit `init-security` on each new target using the same JSON before normal
serving. The normal entrypoint only reads its required input and executes
`/app/lantern` to resume intact state. Missing/partial input/material/state fails
closed without regeneration or a saved-config fallback.

OIDC permits one to 31 fixed replicas without generic `peerPlane.enabled`.
`peerPlane.port` is the data listener (default `6381`); `auth.controlPort` is the
control listener (default `16380`). Both must be distinct from the public port
and each other and match the JSON's fixed origins/listeners. The headless
Service exposes both private ports for bootstrapping; the client Service stays
public/readiness gated. Scaling the StatefulSet does not add voters or generate
new identities. Current configured Issuer intent must be certified before
public readiness; a differing desired set leaves that node unavailable and
never grants Principals, Roles or assignments.

### Finite membership delivery boundary

The trusted setup step copies the two signed memberships into private regular
files. Those copies retain finite expiry. Updating a Secret does not update
the copied files, and a container restart does not rerun Pod initialization.
This chart does **not** provide automatic Secret-to-private-file membership
renewal delivery. Existing source checks preserve isolation and fail-closed
expiry; they do not establish a continuously renewed Kubernetes deployment.
The separate external publisher's qualified directory delivery cannot be
claimed as Kubernetes distribution proof. Keep that delivery/real-cluster
acceptance gap with held [#1636](https://github.com/anaregdesign/lantern/issues/1636);
no sidecar, new cluster operation or acceptance claim is added here.

Outside that held platform exit, fixed identity/trust, native time/quorum,
CLEAN restart custody and the [same-identity certificate maintenance](../../../docs/live-membership-renewal.md#renew-generated-workload-certificates)
contract remain unchanged. Operator/CA signers and the publisher journal stay
outside every serving mount. Never remove state, weaken expiry or repeatedly
reinitialize a Pod to bypass missing renewal delivery.

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
