# OIDC/RBAC and private peer operations

This is the #1599/#1608/#1609 operational contract. The fixed-writer behavior
below describes merged source `93d537892f3025d4a1666dcab36c5748d5cfb9f6`.
The #1672 management implementation and #1608 leaderless target are identified
separately; the latter remains outside this fixed-writer delivery. Source and local
conformance do not complete final provider, production-clock or device
qualification in #1610. Lantern is the database; no PostgreSQL, external policy
Store, coordinator or additional runtime service is required. The approved
Dart platform SQLite adapter remains a separate client option.

## Public mode and bootstrap

No authentication environment variables means OFF. Configure OIDC explicitly
with the complete [env contract](env.md). Partial, unknown or mixed auth config
fails startup; no error falls back to OFF. Retired deployment-wide static bearer
settings are rejected, including empty values. Nonempty `LANTERN_PEERS` and
legacy peer discovery/credentials cannot enable HA or accompany signed membership;
configure the separate private workload plane below.

OIDC requires administrator Issuer and subject IDs in environment configuration.
Those bindings are environment-owned; changing the bootstrap configuration is
an explicit revision-controlled operator action. Admin manages other registered
Issuers, users and Role assignments via current authenticated Server APIs.
Identity is the verified Issuer/subject pair, not email, display name or a client
header. Machines use dedicated credentials bound only to Roles. The approved
actor policy allows authorized management reference/status reads, but no
management mutation. Trusted provenance and durable human qualification are
described in [ADR 0012](decisions/0012-oidc-prefix-rbac.md#management-operation-and-actor-policy);
existing mixed Bearer profiles need actual issuance qualification before activation.
For Google, first review the [provider setup and qualification prerequisites](google-oidc-setup.md),
including the exact callback and private secret binding. Ordinary Google login
and approved ordinary management do not require recent `auth_time`. Google
Security bundle, extra claims and their app publication/verification are
optional and outside baseline gates.

Provision fresh native sys: state only for a new generation. Use durable restart
for existing state. Policy changes require expected revision, immutable change
ID and original-status reconciliation after an uncertain response. Separate
committed policy from cluster-enforced policy: a write acknowledgment alone is
not evidence that every replica stopped serving the old revision.

## Security state version boundary

The ordinary-login/recent-auth split (#1658) uses security image version 2,
signed revision prefix `LNSEC03` and native journal binding v2. Missing
`auth_time` is persisted as unknown; old signed times are retained exactly.
Every current reader, replica and restart/restore path must preserve that
meaning. At `93d53789`, Apply, Store management and non-mutating ValidateIssuer
also require signed authentication within five minutes. #1672 removes that
blanket requirement for ordinary management; the state format alone does not
prove that the policy change or its high-impact protection is implemented.

Image version 1, `LNSEC02` revisions and native binding v1 are incompatible.
There is no automatic migration, implicit reset or mixed-cohort rolling
upgrade. A v2 process rejects the old durable binding before replay or tip/floor
advancement; old readers reject v2 images/revisions. Preserve existing bytes
and stop serving an unproven cohort. An operator moving an experimental old
cohort must fence it and explicitly choose a certified new generation/bootstrap
or independently reviewed migration; this runbook does not perform that action.
Graph namespace format, receipt-WAL V9 and receipt archive V4 are unchanged.
Graph backups cannot substitute for certified security state.

## Management operation and actor policy

An authenticated end user with current explicit `security.manage` authority may
perform ordinary management with missing or older signed `auth_time` under
#1672 after trusted human qualification. This includes creating a mutable scoped data Role and assigning it to
the exact verified bootstrap identity while preserving the environment-owned
`security_admin` assignment. Data grants alone do not require additional
authentication. Bootstrap continues to have no implicit data rights, and
missing `auth_time` alone does not require extra operator provisioning.

Changes to accepted Issuer/credential trust and effective expansion of
`security.manage` require reauthentication for each operation. Removing a
Deny, deleting a Role or removing an assignment may expand authority; assess
the complete resulting policy. A general recent-session flag is insufficient
for this operation. The [operation matrix and proof contract](decisions/0012-oidc-prefix-rbac.md#management-operation-and-actor-policy)
binds actor, original ID, unchanged v1 intent and full policy cut to a signed
post-review event. The purpose callback preserves the ordinary session, CSRF,
authentication evidence, expiry and policy revision. Generic session step-up
retains its five-minute rule and cannot substitute for this proof. ValidateIssuer
uses separate conservative qualified-human probe admission with no age gate;
machine reference/status authorization does not settle probe eligibility.

Valid authentication, exact identity, token/session expiry, current Roles,
revocation/suspension, CSRF/exact origin, expected revision/CAS, current
session/policy admission, environment-owned locks and administrator invariants
remain required. Signed evidence still rejects malformed, future or
contradictory times. Ordinary login never turns unknown/old evidence into a
new authentication event. Keep original Change-ID reconciliation and conflict
review under #1669/#1670; no automatic replay or new grant follows an uncertain
response. Ordinary authority can remain usable for the credential's actual
lifetime, subject to these independent checks; removing the five-minute gate
does not extend that lifetime.


When the first Apply returns a matching typed precommit rejection, reload the
current policy, correct the draft, review a new Change-ID and apply explicitly.
The refusal carries no committed effect, revision or audit entry. Keep the
original ID and use status lookup after an ambiguous dispatch or a generic
conflict, including one seen after route remount; a later refusal cannot settle
that earlier call. A status outside retained history does not prove noncommit.
For the separate operation-authorization refusal, reauthenticate the unchanged
reviewed tuple rather than editing it or allocating a replacement ID.

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

In the fixed-writer baseline, application data remains leaderless. Security
revisions have one fixed writer;
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

OFF can serve the static Admin separately and call the public Server directly.
For example, an Admin at `http://localhost:8080` can select
`http://localhost:6380` as its gateway when the Server explicitly allows
`LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080`. This is an OFF example;
no IdP or browser session is involved.

For OIDC, serve Admin and its browser/auth/RPC routes under the same configured
HTTPS public origin. Select that exact HTTPS origin as the Admin gateway; the
current `http://localhost:6380` connection default does not configure a proxied
OIDC deployment. Match `LANTERN_OIDC_BROWSER_ORIGIN` and the exact registered
Issuer-specific callback to that origin. Pin `/auth/*`,
`/browser/*` and security/control APIs to the fixed writer. Verify TLS to the
Server; for private CAs mount `LANTERN_ADMIN_SERVER_CA_FILE`. A plaintext
Server hop is allowed only behind its explicitly trusted exact gateway IP and
validated HTTPS/public Host headers; never trust arbitrary forwarded headers.
No auth material goes into the SPA bundle, browser local storage or Vite env.

The optional Admin Caddy proxy uses `LANTERN_ADMIN_SERVER_UPSTREAM`, for example
`https://server.internal.example:6380`, chosen by the operator. Its certificate
must match that upstream hostname, and the Server must receive the configured
public Host/scheme through the trusted proxy boundary. Configure exact proxy
addresses with `LANTERN_OIDC_TRUSTED_PROXY_IPS` when required. A browser-entered
gateway URL never chooses Caddy's upstream. An HTTPS frontend does not certify
an unverified upstream hop, a wrong callback or an untrusted forwarded header.

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

## Trusted HTTPS fixture preparation

The earlier actual Server/IdP/Admin fixture used short-lived certificates,
expired and was stopped. Subsequent bounded normal-TLS acceptance completed:
[#1670's four rendered recovery cases](https://github.com/anaregdesign/lantern/issues/1670#issuecomment-6008965699)
passed 4/4, and
[#1606's bounded UI supplement](https://github.com/anaregdesign/lantern/issues/1606#issuecomment-6060077406)
reached 8/8 at their recorded source revisions. Temporary trust, keys and owned
fixture processes were cleaned up. These historical results do not qualify a
later final source, real provider, physical device, HA or production deployment.
A new actual UI run still needs fresh valid trusted HTTPS and matching Issuer,
callback and browser origin. API results and browser contract fixtures alone do
not qualify that combined flow.

### Historical, unexecuted DNS-01 proposal

The older local-only proposal used
`https://lantern-test.replary.com:17443` and
`https://idp-test.replary.com:17444`. A DNS-01 certificate and local host
resolution could serve these without exposing the Server publicly. The proposal
was not executed: no DNS changes, ACME issuance or production setup were performed
for it. Any future DNS records, ACME issuance, certificate installation, host
mapping and runtime restart each need their applicable individual approval;
these names are a proposal, not an installed setup.
Preserve existing website/mail records and keep certificate keys private.

If this proposal is adopted, choose renewal before repeated fixture work.
[Manual Certbot issuance](https://eff-certbot.readthedocs.io/en/stable/using.html#manual)
needs manual renewal unless authentication hooks are configured.
[DNS-01](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge) can
delegate only ACME challenge records to an API-capable DNS zone; that is a
separate operator choice, not a requirement to migrate the whole domain.
Recheck certificate validity, exact origins, verified upstream/private TLS,
CSRF and source hashes before a separately authorized actual UI/provider run.

## Practical Roles and data visibility

[ADR 0012](decisions/0012-oidc-prefix-rbac.md) defines read-only, application
writer, identity/value CDC, backup exporter, operations observer and security
administrator scenarios. Permissions always come through Roles. Literal prefix
Deny wins. CDC value and export actions are explicit; neither is implied by
ordinary read. A machine with explicit export/read Roles can export its visible,
referentially closed data subset without an interactive login. Security changes
follow the approved operation/actor matrix above; machine export permission
does not grant management mutation.

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
convergence are proven. Protected Add/Put/Create require live endpoints and never
create, update or revive them; OFF retains legacy Add/Put endpoint creation.
Edge read requires tail VertexRead and head VertexRead. Every Edge modification
requires tail VertexRead and head VertexWrite, with matching Deny winning across
Roles and default deny. Pair selectors and independent Edge grants are rejected.
VertexDelete remains an independent permission.

## Leaderless S5 — remaining contracts and qualification

#1608 owns the normative shared-sys protocol. The selected G1 Profile B safety
target requires isolated/stale nodes to stop authorization-required processing
within the proven freshness bound. The numerical bound, protocol and clock/
suspension proof remain to be specified; elapsed Go monotonic time alone is
not proof across suspension. The fixed-writer timings above do not select the
leaderless target's timings.

#1609 S5 remains incomplete until #1608 S2–S4 and their consumers qualify:

- Homogeneous mode/cohort/schema/profile and approved workload capabilities
  must admit members before full transfer or serving. Distinguish replication,
  security mutation/session issuance and any selected certification identity.
- One trusted HTTPS public origin needs eligible-node management routing and
  affinity only for the initiating login attempt/process. The current fixed
  writer configuration does not implement these routes.
- Each origin needs its own private key and only the IdP secret handles its
  login capability requires. Shared sys state carries public references, never
  private keys, client secrets, tokens or PKCE verifiers.
- Specify and qualify readiness, partitions, removal/fencing/rejoin, key and
  membership rotation, capacity failure and native restore against the selected
  profile. No failure path becomes OFF. G3 administrator/recovery tooling and
  the operation/actor policy require their own proof.
- Review a versioned stopped/fenced migration preserving original journals,
  import provenance and security/data-origin continuity. Reject old peers and
  readers before transfer or durable-floor advancement. No mixed-cohort rolling
  activation, silent reset or unsafe downgrade; new cohort numbers do not prove
  current grants. Certified checkpoint/tail, stale-backup refusal and session
  invalidation remain independent of receipt durability.
- Bind native/Compose topology and failure results to #1610's final source
  matrix. Real-provider/human, physical-device and publication evidence remain
  separate. Helm/Kubernetes #1636 is deferred outside these gates. Dart
  publication remains last, with the parent before offline.

See the [replication RFC](replication.md), [HA runbook](ha-runbook.md),
[Compose profile](../deploy/compose/README.md) and
[preserved, deferred Helm chart](../deploy/helm/lantern/README.md).
