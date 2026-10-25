# OIDC/RBAC and private peer operations

Public authentication has two modes: OFF and OIDC. OIDC uses the sole
[quorum authority](security-current-authority.md#public-oidc-composition), with
independent original provisioning and intact recovery. The fixed-writer runtime,
replica policy lease, profile selectors and environment bootstrap were retired
in #1820. No backward compatibility or state conversion is supplied.
Source and local conformance do not complete real-provider, configured-source
or physical-device qualification. Lantern needs no external policy database.

The [native Google/Apple session contract](native-login-sessions.md) for #1750
records exact native client profiles, Server-private Apple secret handling,
eight-hour fixed sessions, provider-disconnect limitations and Android callback
ownership. Its current-source implementation and deterministic fixtures do not
complete the separate real-provider/device qualification exits.

## Native client profiles

The [private developer templates](../deploy/examples/auth/README.md) separate
Server environment, reviewed Issuer inputs, mobile builds and OAuth/platform
registration. Keep actual validation selections and provider files outside Git.

Only OIDC can serve native login. Provision the existing certified
authority and use its committed Issuer `native_clients` collection. Native-only
Issuers leave browser `client_id`, `api_audience`, `redirect_uri`, `secret_ref`
empty and human-subject API qualification false. Enable RS256. Profiles must be
sorted by ID; Google presenters must also be sorted, with no duplicates.

Example profile records inside their respective Issuers (public identifiers are
illustrative; substitute actual registered IDs):

```json
{"id":"google_mobile","kind":"google_id_token","client_id":"BACKEND_WEB_CLIENT_ID","service_origin":"https://lantern.example.com","presenters":["ANDROID_CLIENT_ID","IOS_CLIENT_ID"]}
{"id":"apple_android","kind":"apple_android_web","client_id":"REGISTERED_SERVICES_ID","service_origin":"https://lantern.example.com","redirect_uri":"https://lantern.example.com/auth/native/apple/callback/apple_android","android_package":"com.example.application","secret_ref":"apple_android"}
{"id":"apple_ios","kind":"apple_native","client_id":"REGISTERED_BUNDLE_ID","service_origin":"https://lantern.example.com","secret_ref":"apple_ios"}
```

Google's exact Issuer is `https://accounts.google.com`; the bare spelling is
deliberately rejected. Its single audience must be the backend Web client ID.
Every present `azp` must match the explicit allowlist; absent `azp` with that
single audience is allowed and does not attest an installed app. Apple's exact
Issuer is `https://appleid.apple.com`; native Bundle ID and Android Services ID
are separate audience profiles. No email or caller-provided account mapping is
used: ordinary login requires an existing `(iss, sub)` Principal and explicit
Role assignments. Explicit self-registration is a separate, default-disabled
operator policy described below.

Supply each Apple native profile's `client_secret` in the same human
`LANTERN_SECURITY_CONFIG_JSON`, with its exact issuer/client and approved
`token_origin` (`https://appleid.apple.com`). It contains Apple's signed ES256
client-secret JWT, not a `.p8` signing key; the bounded no-newline secret and
exact destination checks remain. The operator creates/rotates that JWT and
supplies it again on startup. The exchange uses `client_secret_post` at the
pinned `/auth/token`, once, with no HTTP retry. Invalid/expired secrets fail at
Apple; Lantern does not sign or refresh provider credentials. Secret bytes are
not persisted into identity material or replicated Issuer records.

Management Issuer responses expose `has_secret_binding`, never secret handles
or file contents. Omit each Apple profile's `secret_ref` to preserve an existing
binding only when ID, kind and client ID are unchanged. New or changed bindings
must be supplied explicitly. Adding/widening trust still requires the existing
operation-bound reauthentication; native login alone cannot satisfy it.

The configured `service_origin` is a logical client/session binding, not a
separate Host-based tenant boundary. Keep the existing TLS/trusted-proxy ingress.
Route Complete using the `Lantern-Native-Affinity` header returned by Begin and
sent by the Dart facade. This bounded canonical `v2.member.process` value is only
a routing hint inside the configured endpoint; the owner validates the complete
transaction and proof. Clients that omit it require a fixed backend. Do not
redirect or retry Complete across backend owners. Route Apple's form callback
by the affinity prefix in `state`. The callback verifies the full
transaction and returns only to the configured Android package. A restart loses
pending transactions; existing committed sessions retain their fixed expiry.

This change updates the credential contract fingerprint of current provisioning.
Do not mix old and new peer profiles or treat a profile mismatch as permission
to reset journals. Apply the existing coordinated provisioning/generation and
custody procedure; there is no automatic state conversion or deployment here.
The [Flutter setup](../sdks/dart/example/README.md#native-google-and-apple-login)
lists required platform registration. Four real-provider/platform exits and
package publication remain separate from merged source and functional-validation/configured-source-integration evidence.

## Native user registration policy

The [registration contract](native-user-registration.md) adds an explicit signup
operation to the OIDC authority. An absent policy disables signup; startup and ordinary
login do not assign UIDs or create Roles. Before enabling it, review existing
`users:` data and broad grants for collisions, choose the exact permitted native
profiles, and install the reviewed `personal_user` Role. Its seven initial Allow
rules use the typed `PrincipalUIDPrefix` with `literal_before: "users:"` and
`literal_after: ":"`. Existing Denies still restrict access. Never substitute an
empty literal prefix or Global resource for a selector an editor does not support.
The current Admin editor refuses unsupported UID-bound Role editing.

An authorized operator reads `GetRegistrationPolicy` and changes it through the
existing Prepare/Apply security workflow using `put_registration_policy`.
Version 1 selects Role `personal_user` and at most 32 exact issuer/profile pairs;
the list is sorted by issuer, then client profile ID. Opening or expanding it
requires operation-bound reauthentication. Closing or reducing it is restrictive
and does not bump Issuer revisions or revoke existing sessions. Suspension,
deletion, assignment withdrawal, Denies and explicit session revocation retain
their existing meanings. Actual enablement is a separate deployment decision.

Clients use Begin/Prepare/Apply registration and save the exact review before
Apply. Recovery authenticates the same identity under a distinct purpose and
inspects only the original status; an unknown outcome is not permission to retry.
UID/enrollment/initial assignment commit together. Session issuance and personal
data writes are separate operations. A lost session output requires ordinary
login. Registration does not provide fresh `auth_time` or extend the fixed session
lifetime. The installed UID and logical `users:<uid>:` prefix are returned in
registration/login/restored-session metadata; legacy UID-less users have neither.
The physical `data:` namespace is never part of the client key.

The registration proof and UID selector contract changes current provisioning's
admission fingerprint. Use the coordinated generation/custody procedure for a
homogeneous cohort; do not reinterpret old journals, silently mix profiles or
reset retained state. The implementation and isolated fixtures do not enable
signup, migrate existing data or qualify real provider/device behavior.

## Public mode and bootstrap

Select `LANTERN_AUTH_MODE=oidc` and supply the same complete human-authored
`LANTERN_SECURITY_CONFIG_JSON` to `provision-security`, `init-security` and
every normal start/restart. It contains public origin/TLS termination, explicit
target platform, `material_root`, separate `persistence_root`, fixed nodes,
selected `node`, exact initial administrator and current provider profiles and
secrets. For one node the unique named entry selects it automatically; two or
more entries require `node`. See the
[configuration guide](../deploy/examples/auth/README.md#environment-json-startup)
and [Compose example](../deploy/compose/README.md#oidc).

Run `lantern provision-security --operator-directory ...` once. It creates
independent identity keys and fixed historical trust under `material_root`,
without opening native state, trusted time or listeners. On each actual target,
`init-security` creates fresh native state once under `persistence_root` and
completes orderly CLEAN shutdown. Normal startup resumes that intact state and
reads current settings from the supplied JSON again. Identity material is not a
saved configuration; missing JSON, missing/partial material or mismatched state
never triggers fallback, trust regeneration, migration or reset. OFF accepts no
security JSON, including an explicitly empty value.

Current configured Issuers are declarative. Add a later Issuer to `issuers`,
keep all intended configuration-owned Issuers in the list, and supply the updated
configuration on restart. Native certified compare-and-swap reconciles that
intent before public readiness. A node whose supplied desired Issuer set differs
from the certified set remains publicly unavailable. This does not create
Principals, Roles or assignments, appropriate management-owned Issuers, restore
suspended users or regrant withdrawn permissions. Existing authorization and
last-administrator invariants still apply. Initial administrator identity is a
fresh-state seed and an exact original-identity assertion on resume, never a
restart reconciliation instruction. No manual desired revision is needed.

Standalone uses one fixed voter; the topology may contain one to 31 fixed
voters. This is not dynamic membership or live standalone expansion. Both
control and data memberships require continuous short-lived renewal through the
external operator, including for one node. Operator/CA signing keys stay outside
all serving mounts. `renew-security` prepares same-key certificates and the next
finite window from operator/identity material; it reads no saved user setup or
provider secrets. Its [maintenance sequence](live-membership-renewal.md#renew-generated-workload-certificates)
preserves original paths/state and uses CLEAN stop, reviewed installation,
explicit publisher advance/resume and node restart.

Browser and external API profiles are independent. Browser-only needs no
`api_audience`; absent API configuration rejects external API JWTs. API-only
needs no browser client/redirect. Fresh provisioning requires the initial
administrator's enabled Issuer to provide a browser profile for login and operation-purpose reauthentication.
Google's preset pins canonical issuer, RS256 and token origin. Native profile,
secret destination, endpoint/SSRF and exact public-origin/proxy checks remain.
Current provider secrets, public TLS, IdP roots/private origins and proxy settings
come only from the supplied configuration; none is recovered from identity files.

No legacy file-backed OIDC environment recipes or generated runtime-config
reference are supported. External material uses the same typed configuration and
identity-material format. Generic OFF private-peer controls retain their
independent workload plane; no authentication environment means standalone OFF.

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

Initialize native state only for a new original cohort. Resume uses supplied
configuration, fixed identity material, intact journals and independent
custody/minimum floors. It cannot recover serving authority from persisted bytes alone. Prepare returns a server
ID bound to the full profile, semantic cut, actor and exact intent; retain it for
Apply and status reconciliation. APPLIED proves the original committed result;
WAITING and STOPPED separately describe cessation of stale authorization.

## Security state version boundary

Security image version 2, protocol version 2, namespaced-v1 data and native
journal/capsule encodings remain versioned contracts. Retiring the old runtime
does not rename their bytes, synthesize new IDs or reinterpret an old result.
Old fixed-writer signed revisions, sessions and journals cannot be imported.
Graph backups and receipt-WAL V9 / archive V4 retain their separate contracts
and cannot substitute for authority state or custody.

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
binds actor, original full ID, immutable intent and full policy cut to a signed
post-review event. The purpose callback preserves the ordinary session, CSRF,
authentication evidence, expiry and policy cut. Broad session step-up is retired;
only the reviewed operation can request this proof. ValidateIssuer
uses separate conservative qualified-human probe admission with no age gate;
machine reference/status authorization does not settle probe eligibility.

Valid authentication, exact identity, token/session expiry, current Roles,
revocation/suspension, CSRF/exact origin, expected full-cut/CAS, current
session/policy admission, environment-owned locks and administrator invariants
remain required. Signed evidence still rejects malformed, future or
contradictory times. Ordinary login never turns unknown/old evidence into a
new authentication event. Keep original Change-ID reconciliation and conflict
review under #1669/#1670; no automatic replay or new grant follows an uncertain
response. Ordinary authority can remain usable for the credential's actual
lifetime, subject to these independent checks; removing the five-minute gate
does not extend that lifetime.


After an ambiguous Apply, retain the exact original ID/review and reconcile its
original status. An unknown or evicted result does not prove nonexecution.
Review a corrected command only after resolving the earlier operation's state.
Per-operation authentication preserves the unchanged reviewed tuple.

## Workload membership and freshness

HA, including public OFF, has a distinct private TLS 1.3/mTLS listener. Every
member has its own workload URI identity and SPKI-pinned certificate. The
operator-signed membership binds exact path-free HTTPS private origins,
deployment, public auth mode, namespace format, security generation and full authority
profile binding (the retired writer-key slot must be zero). DNS supplies addresses only for approved origins. Never share a
private key, accept a browser/Bearer credential as membership or publish this
listener through a public gateway.

The independently persisted membership revision prevents rollback. Renew the
signed manifest atomically before its expiry, with at most a ten-minute signed
lifetime. Account for Kubernetes projection/distribution delays in the renewal
budget. The operator signing private key is never mounted in Lantern. Use
`fresh` only for first enrollment, then `resume`; copied or ambiguous enrollment
state does not prove continuity.

Control consensus and renewal use the original fixed set of 1–31 voters and
strict majority `floor(N/2)+1`, including even counts. Data ring replicas are a
separate topology. Losing a majority prevents new commits and renewals; existing
serving authority ends at its original finite native-time bound. No automatic
voter shrink, live expansion, writer promotion, deadline extension or fallback
to OFF is available. A distinct origin key owns each original H/serial; its
custody cannot be recreated from a runtime label or old journal.

Every output unit is authorized by the actual current owner. Under approved A,
an exactly authorized unit may finish late; the contract supplies no physical
send deadline. New stale units remain closed after revocation/expiry. See the
[output contract](security-current-authority.md#bounded-output-under-approved-a).
Graph-only backups cannot restore authentication continuity, and receipt-WAL
continuity has its own certification requirements.

## Browser and diagnostics boundary

OFF can serve the static Admin separately and call the public Server directly.
For example, an Admin at `http://localhost:8080` can select
`http://localhost:6380` as its gateway when the Server explicitly allows
`LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080`. This is an OFF example;
no IdP or browser session is involved.

For OIDC, serve Admin and its browser/auth/RPC routes under the same configured
HTTPS public origin. Select that exact HTTPS origin as the Admin gateway; the
current `http://localhost:6380` connection default does not configure a proxied
OIDC deployment. Match the supplied JSON `public_origin` and the exact registered
Issuer-specific callback to that origin. Pin `/auth/*`,
`/browser/*` and security/control APIs to a pinned eligible authority origin. Verify TLS to the
Server; for private CAs mount `LANTERN_ADMIN_SERVER_CA_FILE`. A plaintext
Server hop is allowed only behind its explicitly trusted exact gateway IP and
validated HTTPS/public Host headers; never trust arbitrary forwarded headers.
No auth material goes into the SPA bundle, browser local storage or Vite env.

The optional Admin Caddy proxy uses `LANTERN_ADMIN_SERVER_UPSTREAM`, for example
`https://server.internal.example:6380`, chosen by the operator. Its certificate
must match that upstream hostname, and the Server must receive the configured
public Host/scheme through the trusted proxy boundary. Configure exact proxy
addresses with the supplied JSON `trusted_proxy_ips` when required. A browser-entered
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

The older local-only proposal required separately chosen HTTPS Admin/Server and
IdP origins. Placeholder counterparts are `https://replace-admin-host.invalid`
and `https://replace-idp-host.invalid`; keep actual selections private. A DNS-01
certificate and local host resolution could serve operator-owned real names
without exposing the Server publicly. The proposal
was not executed: no DNS changes, ACME issuance or production setup were performed
for it. Any future DNS records, ACME issuance, certificate installation, host
mapping and runtime restart each need their applicable individual approval;
the proposal is not an installed setup, and `.invalid` names are not certificate targets.
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

#1608 owns the normative shared-sys protocol. Its fixed-membership current
authority and public API/client composition are merged in
[#1723](https://github.com/anaregdesign/lantern/pull/1723),
[#1724](https://github.com/anaregdesign/lantern/pull/1724) and
[#1726](https://github.com/anaregdesign/lantern/pull/1726). The explicit
OIDC authority uses current quorum renewal and native UTC/elapsed intervals
under the selected source/path/rate assumptions and fail-closed bounds in
[the current-authority contract](security-current-authority.md). Elapsed Go
monotonic time alone is not proof across suspension.

#1609 S5 remains incomplete for operational cutover and final #1610 acceptance;
S2–S4 implementation delivery is separate from those exits.

### Delivered orderly restart and operator requirements

Orderly current-authority floor custody was merged in
[#1728](https://github.com/anaregdesign/lantern/pull/1728). Follow the
[current lifecycle contract](security-current-authority.md#orderly-floor-custody-and-process-restart)
and [maintainer custody lifecycle qualification](../testbed/current-custody/README.md):

- Keep original provisioning/keys read-only, private durable M/P/B journals,
  and private durable custody in separate mounts. Preserve the original
  documents, identity, keys and fixed paths; reject symlink/hardlink aliases.
- A normal stop must finish readiness drain, public/private worker and producer
  joins, all journal/resource closes, final floor persistence and CLEAN
  publication last. Check process success and `server stopped cleanly`, plus
  the matching CLEAN binding/cycle and exact floor hash; a stopped container
  or surviving volume alone is insufficient.
- Intact process/container restart requires the same complete current security
  JSON and normal resume of retained native and application state. New native time, valid membership/TLS,
  current quorum/catch-up and independent data readiness are still required.
  Durable sessions retain normal expiry/current-policy checks; callback,
  purpose and output permits do not survive the process.
- On refusal, retain original files and logs. Do not delete custody sidecars or
  leases, switch to `fresh`, reset state, rewrite provisioning, or derive new
  floors from suspect journals. RUNNING, mixed/missing or mismatched custody,
  damaged/known-old families remain fail-closed. A shutdown error is a failed stop;
  uncertain final CLEAN publication follows the linked lifecycle's matching-pair
  validation. No failure path becomes OFF; CLEAN is not proof of present
  authority or data durability.

### Remaining deployment and acceptance exits

- Admit homogeneous mode/cohort/schema/native profile and approved workload
  capabilities before transfer or serving. Distinguish replication, security
  mutation/session issuance and any selected certification identity.
  Keep per-origin private keys and
  only the IdP secret handles each login capability requires. Shared sys state
  carries public references, never private keys, client secrets, tokens or PKCE
  verifiers.
- Production eligible-node management routing under one trusted HTTPS public
  origin, with affinity only for the initiating login attempt/process, remains
  unqualified. The existing fixed-upstream proxy is not that topology.
- Actual legacy cutover needs a reviewed stopped/fenced migration. Retain
  original status/data/receipt history and import provenance; choose a new
  generation/cohort with independent keys/genesis, invalidate old sessions and
  cursors, and install compatible clients. Reject old peers/readers before
  transfer or floor advancement; no mixed-cohort rolling activation, silent
  reset or downgrade. New cohort numbers alone do not prove current grants.
- Bind ordinary process/container restart, communication recovery, readiness
  and failure evidence to #1610's final source matrix under the existing native
  kernel/storage premises. Physical sleep/live resume is outside the current
  scope; backup/VM snapshot/clone return is deferred and unscheduled. Dynamic
  membership, rotation, retirement/compaction, lost-state rejoin and automatic
  migration are outside this fixed-membership, intact-storage unit. These are
  not prerequisites for its normal CLEAN/resume lifecycle; no physical
  reboot/power-loss or abnormal recovery qualification follows.
- Real-provider/Google login, human/physical-device evidence and SDK publication
  remain separate and unqualified by these source or restart results.
  G3 administrator/recovery tooling and operation/actor-policy proof remain
  separately owned.
  Helm/Kubernetes #1636 is deferred outside these gates. Dart publication remains
  last, with the parent before offline.

See the [replication RFC](replication.md), [HA runbook](ha-runbook.md),
[Compose profile](../deploy/compose/README.md) and
[preserved, deferred Helm chart](../deploy/helm/lantern/README.md).
