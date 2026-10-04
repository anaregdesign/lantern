# OIDC/RBAC and private peer operations

This is the in-flight #1599/#1608/#1609 operational contract. Source and local
conformance do not complete final provider, production-clock or device
qualification in #1610. Lantern is the database; no PostgreSQL, external policy
Store, coordinator or additional runtime service is required. The approved
Dart platform SQLite adapter remains a separate client option.

## Public mode and bootstrap

No authentication environment variables means OFF. Configure OIDC explicitly
with the complete [env contract](env.md). Partial, unknown or mixed auth config
fails startup; no error falls back to OFF. Retired deployment-wide static bearer
settings and public-peer discovery settings are rejected, including empty values.

OIDC requires administrator Issuer and subject IDs in environment configuration.
Those bindings are environment-owned; changing the bootstrap configuration is
an explicit revision-controlled operator action. Admin manages other registered
Issuers, users and Role assignments via current authenticated Server APIs.
Identity is the verified Issuer/subject pair, not email, display name or a client
header. Machines use dedicated credentials bound only to Roles and cannot
claim recent interactive authentication.

Provision fresh native sys: state only for a new generation. Use durable restart
for existing state. Policy changes require expected revision, immutable change
ID and original-status reconciliation after an uncertain response. Separate
committed policy from cluster-enforced policy: a write acknowledgment alone is
not evidence that every replica stopped serving the old revision.

## Workload membership and freshness

HA, including public OFF, has a distinct private TLS 1.3/mTLS listener. Every
member has its own workload URI identity and SPKI-pinned certificate. The
operator-signed membership binds exact path-free HTTPS private origins,
deployment, public auth mode, namespace format, security generation and writer
public key. DNS supplies addresses only for approved origins. Never share a
private key, accept a browser/Bearer credential as membership or publish this
listener through a public gateway.

The independently persisted membership revision prevents rollback. Renew the
signed manifest atomically before its expiry, with at most a ten-minute signed
lifetime. Account for Kubernetes projection/distribution delays in the renewal
budget. The operator signing private key is never mounted in Lantern. Use
`fresh` only for first enrollment, then `resume`; copied or ambiguous enrollment
state does not prove continuity.

Application data remains leaderless. Security revisions have one fixed writer;
replicas acquire bounded signed serving leases over the private plane. Initial
writer fencing takes 35 seconds. Public admission lasts at most 28 seconds;
replica/writer checks may shorten it. Idle or blocked streams cannot retain
unbounded authority. Membership expiry, unknown policy, lost renewal or stale
generation stops protected serving. Replication of sys: bytes alone is
insufficient evidence of current policy.

Writer loss stops login and policy changes, then protected serving after lease
expiry. Do not promote another writer automatically or configure a secondary
as OFF. Replacement requires externally fencing the old writer, choosing the
current certified native state and an explicit generation/restore decision.
Graph-only backups or public data exports cannot restore authentication state.
Receipt-WAL continuity and security continuity have separate certification gates.

## Browser and diagnostics boundary

Serve Admin under its configured HTTPS public origin. Pin `/auth/*`,
`/browser/*` and security/control APIs to the fixed writer. Verify TLS to the
Server; for private CAs mount `LANTERN_ADMIN_SERVER_CA_FILE`. A plaintext
Server hop is allowed only behind its explicitly trusted exact gateway IP and
validated HTTPS/public Host headers; never trust arbitrary forwarded headers.
No auth material goes into the SPA bundle, browser local storage or Vite env.

Admin's entrypoint accepts fixed `scheme://hostname:port` upstreams and fails
closed on incomplete or injected proxy configuration. Missing Server and
Prometheus routes return API errors, never the SPA shell. Prometheus requests
perform a Server `/auth/operations` check first, preserving the current session
and public Host. OFF explicitly permits diagnostics; OIDC requires a current
`operations.read` Role. After admission, strip cookies and Authorization from
the Prometheus request. Query strings are not forwarded to the authorization
endpoint. Only GET diagnostics are proxied. This follows the documented Caddy
[forward_auth](https://caddyserver.com/docs/caddyfile/directives/forward_auth)
and [reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)
contracts.

Protected `/metrics`, health/readiness and profiling listeners stay on loopback;
profiling is disabled by default. Kubernetes probes execute local checks.
Production collection needs an operator-authenticated local scraper/sidecar.
Readiness includes current workload/security authority, even when data lag is
acceptable. Raw logs/evidence must exclude credentials, codes, cookies and
personal data; retain private fixture material outside Git/artifact uploads.

## Practical Roles and data visibility

[ADR 0012](decisions/0012-oidc-prefix-rbac.md) defines read-only, application
writer, identity/value CDC, backup exporter, operations observer and security
administrator scenarios. Permissions always come through Roles. Literal prefix
Deny wins. CDC value and export actions are explicit; neither is implied by
ordinary read. A machine with explicit export/read Roles can export its visible,
referentially closed data subset without an interactive login. Security changes
still require recent interactive authentication.

Logical public keys map to physical `data:` keys. `sys:` metadata never appears
in business scans, counts, Graph results, public backup or CDC. Ranking uses
shared corpus DF/N/document-length statistics; hidden data in the same corpus
may affect visible scores. Search limits candidates before top-k; Graph never
passes through hidden nodes/edges. Exact counts, aggregates, paging and CDC
remain authorized. No per-Role ranking-statistics cache is required.

Public CDC is `WatchChanges` with an opaque scope/policy/request/corpus-bound
cursor. Save it only after applying every invalidation in the frame. A gap,
policy change or unsafe resume requires rebootstrap/revalidation. No client
manufactures per-origin offsets from an opaque cursor. Private Subscribe and
Snapshot retain internal mutation/receipt/system synchronization and are not
public CDC endpoints. Policy/session changes partition client cache state.

The new existing-endpoint-only Edge Create family (#1626) must preserve local
atomic liveness/absence and original receipts. It stays disabled in HA until
concurrent creation versus Add/Put/Delete and delayed endpoint/tombstone/TTL
convergence are proven. Existing Add/Put behavior remains available. Directed
pair grants cannot combine halves across rules or Roles or imply Delete.

See the [replication RFC](replication.md), [HA runbook](ha-runbook.md),
[Compose profile](../deploy/compose/README.md) and
[Helm chart](../deploy/helm/lantern/README.md).
