# ADR 0012: Default-off OIDC and Role-only prefix RBAC

- Status: Accepted design; implementation and qualification tracked separately
- Driving issue: [#1599](https://github.com/anaregdesign/lantern/issues/1599)
- Contract issue: [#1600](https://github.com/anaregdesign/lantern/issues/1600)

The current implementation composes native signed sys state, short-lived
policy authority, Role admission, logical data boundaries, public Security/
Changes/browser APIs and a separate signed-membership workload plane in the
production Wire graph. Source, CI, final exact-source acceptance and external
provider/clock evidence remain separate exit buckets in #1599/#1610. Local
conformance does not complete deployment or provider qualification.

The native Store reserves private nonexpiring GraphCache state and reuses
FileWAL framing, ownership, sync, lower-bound tip proofs and bounded checkpoint
rotation. Recovery verifies signed history before installing an image and
never restores a serving lease. A fresh writer observes the full restart fence
before public serving; replicas require a challenge-bound current writer proof.

## Context

Lantern is the database. Security state belongs in Lantern's own storage,
mutation log, replication and recovery primitives. There is no PostgreSQL,
external authorization service, or new consensus dependency. The approved
SQLite adapter in the Dart SDK is independent of this decision.

The existing deployment bearer authenticates one deployment-wide authority.
It cannot represent users, prefix restrictions, revocation, or authenticated
peers independently. Asynchronous graph replication alone cannot prove that
a replica has current authorization state. This ADR defines the replacement
contract before enabling that replacement on a listener.

## Modes and startup

| Configuration | Server / Admin behavior |
| --- | --- |
| No authentication configuration | `off`; anonymous data operations; security management unavailable |
| `LANTERN_AUTH_MODE=off`, no OIDC or machine credentials | Same explicit OFF behavior |
| `LANTERN_AUTH_MODE=oidc`, complete valid configuration | OIDC authentication and RBAC; Admin requires login |
| Empty, unknown, partial or conflicting authentication settings | Startup fails; never fall back to OFF |
| Invalid, stale, recovering or unavailable security authority | Protected operations fail closed; peer repair remains available |

OIDC requires `LANTERN_OIDC_ADMIN_ISSUER` (an exact HTTPS Issuer),
`LANTERN_OIDC_ADMIN_SUBJECTS` (a nonempty JSON array of exact `sub` strings),
an API audience, a browser client ID and exact redirect URI, and native
durable security storage. Presence of an OIDC setting without an explicit
mode is an error. Empty configured values count as configuration, not absence.
Unknown subjects are never inferred from email, display name or IdP groups.

Bootstrap creates env-owned assignments to the protected `security_admin`
Role. Reconciliation is an atomic writer operation with an operator-declared
bootstrap configuration revision; a replica or an old configuration must not
recreate a removed administrator. Subsequent administrators use the same
Role-assignment model. Bootstrap does not grant access to data.

Legacy `LANTERN_AUTH_TOKENS` needs an explicit migration to named machine
Principals and Role assignments. It must not silently become a security-admin
credential. The retired static-token and reflection-exemption settings are rejected even
when explicitly empty. Only absent authentication configuration selects OFF;
no legacy bearer is automatically promoted to a machine or administrator. Health remains structurally auth-exempt. Schema
reflection and global diagnostics have explicit capabilities.

The writer may load `LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE`, an absolute
regular private file (mode `0600` or `0400`, no symlinks). Its strict JSON array
contains `name`, `role_ids`, and `credentials` with `token`, `created_at`, and
`expires_at`. Role IDs refer to `LANTERN_SECURITY_BOOTSTRAP_ROLES`; permissions
cannot be attached to a machine directly. At most 64 machines with four
credentials each are admitted. Tokens use `lnt_m1_` followed by canonical
unpadded base64url of 32 cryptographically random bytes; lifespan is positive
and at most 90 days. Rotate/remove credentials by advancing the operator
bootstrap revision; account suspension and current Role changes apply locally.
Only domain-separated SHA-256 digests enter signed sys state and its native
journal/replica recovery. Raw tokens are never management responses, graph
records or audit fields. No last-seen write or IdP lookup runs for machine
requests, and a machine credential does not prove interactive recent auth or
create a browser session. This public credential route cannot admit a peer.

The capability endpoint returns a sanitized mode, readiness and supported
features. Admin must not interpret a 401, network failure or malformed response
as evidence of OFF. In OFF it can use data features; it cannot add Issuers,
change Roles, assign users or issue authenticated sessions.

## Physical storage and public identity

Storage format `namespaced-v1` has two disjoint physical domains:

- `sys:`: typed security records and other system metadata.
- `data:`: every public logical key, including keys that themselves start
  with `sys:` or `data:`.

The public Server boundary maps `logical` to `"data:" + logical` exactly
once, in every mode. For example, `users:user1`, `sys:users:user1` and
`data:users:user1` are three ordinary logical keys, physically stored as
`data:users:user1`, `data:sys:users:user1` and `data:data:users:user1`.
There is no escape syntax or already-prefixed exception. Role prefixes and
all SDK, CLI and Admin APIs use logical keys. Empty public prefix selects
only `data:`. Key values are never case-folded or Unicode-normalized.

Conversion covers vertices, both edge endpoints, contributions, seeds,
prefixes, batches, missing/results, graph maps, public exports, CDC and
receipt results. Internal apply, WAL and peer Snapshot use physical keys;
they do not pass through the public mapper. Unknown storage versions and
mixed-version peers fail before transfer. Existing unnamespaced recovery
requires an explicit offline migration, including receipt evidence; it is
not guessed from a key's spelling.

Cross-domain edges are forbidden. Generic graph mutations cannot target
`sys:`, including replicated graph mutations. System records are nonexpiring
and reserved from generic Delete, TTL GC and capacity admission. Data
saturation must not prevent security revocation. Public Search analyzes
logical keys; system records never enter the business index, vocabulary,
corpus statistics, graph degree, traversal or public counts. Public
`BackupSnapshot` contains logical data records only; private recovery includes
both domains and security proof.

## Security metadata and durability

The security model is `Principal -> RoleAssignment -> Role -> PermissionRule`.
Principals contain identity and lifecycle state, never permission rules.
An OIDC Principal's key is the exact verified `(iss, sub)` pair. A machine
Principal is explicitly registered and also receives only Role assignments.
Unknown Principal, disabled Issuer, suspended Principal or unknown assigned
Role denies access. A verified JWT alone grants nothing.

A native system transaction carries the complete affected security image,
expected revision, unique change ID, audit record, generation, writer identity,
contiguous revision and previous/current image digests. The original writer
signs the bounded canonical envelope. Transport mTLS authenticates the relay,
not the original transaction. Generic HLC/LWW merges must never independently
merge grants, memberships, Issuers or session revocations.

The writer serializes transactions, validates referential integrity and the
last usable administrator, synchronizes the native security WAL, installs
the complete image in `sys:` in the same GraphCache, and atomically publishes
one immutable compiled revision. A CAS conflict changes nothing. A repeated
change ID with the same intent returns the original status; a different intent
is rejected. Singular management mutations delegate to plural canonical
operations, whose responses remain request-index-aligned.

System persistence is mandatory for OIDC even when application data uses the
graph-only runtime. Reuse the native FileWAL framing, integrity checks, lease,
sync and recovery primitives through a separate typed system durability lane.
It has its own sequence, checkpoint, bounded compaction and certification.
Do not filter application frames out of an existing receipt WAL, reset its
sequence under an old origin, or turn every data RPC into an fsync. Application
receipt-WAL certification remains the owned graph/Store/origin/Log/HLC bundle
from ADR 0010. Security-writer identity is independent of graph origin identity.

Session issuance and revocation are durable control changes. Sessions use a
fixed bounded expiry; data requests do not synchronously update `last_seen`
or sliding idle expiry. Metadata bytes, records, audit retention, sessions,
revisions and checkpoint size have explicit caps. Revocation reserves remain
available when general admission is exhausted. Corrupt, incomplete, forked or
unknown state stays unavailable until certified recovery.

Local fsync proves local restart continuity, not survival of the sole volume's
destruction. A stale backup cannot prove recent revocations. Restore enters
management-only recovery, requires reconciliation of grants and revocations,
fences the old writer, installs a new generation and invalidates old sessions
and leases. A new generation alone does not repair stale grants.

## Permission rules and evaluation

A rule contains an explicit `allow` or `deny`, a known action and a resource
kind. A data rule has a literal logical prefix; `""` explicitly means all
logical keys. A global rule has no prefix. `*` has no special meaning in a
prefix. There is no inheritance, user override, wildcard action or IdP claim
mapping. Policy schema versions and limits are checked before publication.

For each action/resource, any matching Deny across any assigned Role wins.
Otherwise at least one matching Allow is required. More specific Allows
cannot override a Deny. Unknown actions, resource kinds or Roles fail closed.
Compile action-specific prefix ranges once per immutable revision; capture one
coherent snapshot per RPC or exact batch before graph locks. Evaluation does
not perform network access or persistence under graph locks.

### RPC / action / projection contract

The primitive data actions are `vertex.read`, `vertex.write`, `vertex.delete`,
`edge.read`, `edge.add`, `edge.write`, `edge.delete`, `query`, `cdc.identity`,
`cdc.value`, `export` and `receipt.read`. They are independently granted.
`vertex.read` includes a key's existence, value and TTL; `edge.read` includes
existence, effective weight, TTL and contribution identity. Global actions are
`operations.read`, `schema.read` and `security.manage`. Internal
`cluster.replicate` is a protected machine capability outside user Role CRUD.

| RPC family | Required actions and information boundary |
| --- | --- |
| GetVertex / GetVertices | `vertex.read` for every exact key, including missing results |
| PutVertex / PutVertices | `vertex.write` and `vertex.read`; conditional state and outcomes are observable; effective lifetime reduction additionally requires `vertex.delete` |
| DeleteVertex / DeleteVertices | `vertex.delete` and `vertex.read`; incident-edge cleanup is aggregate lifecycle maintenance |
| GetEdge / GetEdges | `edge.read` and `vertex.read` on both endpoints |
| AddEdge / AddEdges | `edge.add`, `edge.read`, `vertex.read` and endpoint-creation `vertex.write` on both endpoints |
| PutEdge / PutEdges | `edge.write`, `edge.read`, `vertex.read` and endpoint-creation `vertex.write` on both endpoints; lifetime reduction additionally requires `edge.delete` |
| DeleteEdge(s) / DeleteEdgeContribution(s) | `edge.delete`, `edge.read` and `vertex.read` on both endpoints; contribution Delete remains exact |
| ScanVertices / ScanVertexKeys / CountVerticesByPrefix | Only the `vertex.read` subset, before limits, counts and cursor generation |
| ScanEdges | Only the subset with `edge.read` and `vertex.read` on both endpoints |
| DeleteVerticesByPrefix | Delete only the `vertex.delete` plus `vertex.read` subset; dry-run/count/limit use the same subset |
| DeleteEdgesByPrefix | Delete only the `edge.delete` plus readable-edge/endpoints subset |
| Illuminate / TopVerticesByDegree | `query` plus readable vertices/edges; restrict paths and actual degree to the authorized induced graph; TF-IDF/BM25 use shared corpus statistics |
| SearchVertices | `query` plus `vertex.read`; restrict candidates before top-k; reuse this index's shared ranking statistics |
| BackupSnapshot | `export` plus readable vertices/edges; include only the referentially closed authorized data subset |
| Scoped CDC | Explicit `cdc.identity`; values additionally require `cdc.value` and ordinary data reads; endpoints must both qualify |
| GetReceiptCapability | Current authority plus `receipt.read` and `vertex.read` within an applicable logical scope; supported capabilities only |
| GetReceiptStatus(es) / receipt replay | `receipt.read` and current rights for every proven original resource; absence needs caller intent proof or fails closed |
| GetServerStatus / GetReplicationStatus / metrics | `operations.read`; never inferred from data read |
| Issuer / Principal / Role / assignment / session / audit management | `security.manage` and recent authentication; expected revision required on writes |
| Peer Subscribe / Snapshot / PeerStatus | Separate admitted peer identity and `cluster.replicate`; no human token or prefix filtering |
| Health | Auth-exempt, content-free liveness/readiness |
| Reflection | `schema.read` in OIDC; explicit OFF behavior |
| Any unclassified new RPC | Deny until the action matrix is extended |

An exact batch with any denied identity is rejected before lookup or mutation,
including a batch mixing visible and denied keys. Responses must not reveal
existence, missing keys, conditions, weight, TTL or receipt contents of denied
resources. Collection APIs instead operate on an authorized subset, subtracting
all Deny ranges before pagination, ranking, limits, counts and dry-run.

A Vertex owns incident-edge lifetime. An authorized Vertex Delete or expiry
removes incident edges even if an edge Delete rule denies explicit edge
mutation. Shortening a lifetime or a born-expired Put is an effective Delete
and must pass its Delete action; a separate lifecycle Allow cannot bypass a
Delete Deny. Already committed expiry and admitted replication are protected
system maintenance, not new actions by the original user's current Roles.
If an operation cannot prove its effects safely, reject it before mutation.

### Directed existing-endpoint connection creation (#1626)

A separate plural-canonical `CreateEdges` / singular `CreateEdge` family uses
an independent `edge.create` action. Existing Add accumulation, Put replacement
and endpoint creation retain their current contracts. Creation authority never
implies Add, Put, update or Delete. Both endpoint Vertices must be readable and
live at the same atomic application-time cut as Edge absence. The operation
never creates/resurrects a Vertex or changes its value/TTL. No omitted option
may broaden this existing-endpoints-only default.

A directed pair selector matches one rule's tail prefix AND head prefix AND
action. All three must match that same rule; no half-rule/half-Role combination
or reverse-direction inference is permitted. Any applicable Deny wins. Delete
requires an independent explicit grant. A caller-supplied owner/prefix never
establishes identity: initial ownprefix assignments use Server-resolved verified
identity and explicit literal Roles. General dynamic templates need a separate
design and are not activated implicitly.

The Rule resource oneof is literal prefix, directed prefix pair, or global.
Pairs select Edge Read/Create/Add/Write/Delete, CDC Identity/Value, Export and
Receipt Read only; they never grant a Vertex or Query action. `edge.create`
requires a pair. Existing prefix rules retain their endpoint-union semantics;
matching prefix Deny on either endpoint and matching complete pair Deny both
defeat an Edge Allow. Core receives detached bounded endpoint ranges and
two-dimensional range filters, applied before scan limits/top-k and on every
traversed Edge. Pair-only IDENTITY CDC does not require Vertex/Edge Read; VALUE
CDC additionally requires both endpoint reads and the full Edge Read selector.
Receipt absence still requires a whole-domain proof, never a pair-only grant.
Admin edits each direction explicitly and asks Server to explain one complete
action selector; operation-specific additional actions are checked separately.

Standalone outcomes are `CREATED_AND_LIVE`, `EDGE_EXISTS`, `ENDPOINT_NOT_LIVE`
and `EXPIRED`, one per request position. Source weights must be finite and
nonzero so successful creation denotes a live Edge. Any unexpired contribution
counts as an existing Edge even if its current aggregate is zero; collision
never removes or rewrites contributions. Born-expired input is a no-op before
endpoint/absence checks. A successful earlier duplicate makes later positions
`EDGE_EXISTS`. Storage evaluates all conditions at one application-time cut.

Authorized conditional results expose only bounded creation/no-change outcomes,
not existing Edge weights, TTLs, owner fields or another receipt. Duplicate
positions, collision outcomes, TTL/Delete races and response-loss reconciliation
must be request-index-aligned and preserved as original receipt evidence.
Replicated committed effects must not fabricate endpoints, resurrect deleted
Vertices, overwrite an already existing Edge on replay, or reinterpret creation
as Add/Put. Missing remote endpoints require a convergence design that retains
the original causal intent rather than silently dropping it or creating Nodes.

Leaderless data replication cannot prove cluster-wide absence from one local
atomic check. Before activation, #1626 must freeze concurrent-create versus
legacy Add/Put/Delete arbitration, delayed endpoint delivery and tombstone/TTL
behavior. A locally successful create is not a cluster-wide uniqueness claim.
The new family stays disabled in HA until the durable convergence guarantee is
designed and verified (#1626). This restriction applies only to the new operation;
existing Add/Put families keep their contracts. It does not waive cluster-wide
create-if-absent guarantees or authorize replica-local absence as a substitute.
Activation also requires matching SDK outcomes, the Admin pair editor and
real-wire/receipt/restart tests. Independent OIDC/RBAC deliveries continue while
this gate remains closed.

### Practical Role templates

Templates are explicit Roles with literal prefixes, not additional privilege
mechanisms. Operators may copy them for a namespace and add Deny exceptions.

| Role | Scenario / permissions |
| --- | --- |
| `namespace_reader` | Application reads, scans, Search and traversal in one prefix; vertex/edge read and query; no CDC/export/global diagnostics |
| `namespace_editor` | Reader plus vertex write, edge Add/Put; Delete denied; lifetime reduction denied |
| `namespace_maintainer` | Editor plus vertex/edge Delete and collection Delete in its prefix |
| `connection_creator` | Both endpoint Vertex reads plus directed Create and receipt access; no Edge read, Add/Put/Delete or endpoint write |
| `connection_deleter` | Both endpoint Vertex reads plus directed Edge read/Delete and receipt access; independent of Create |
| `cdc_identity_consumer` | Explicit identity invalidations for allowed prefixes; no values, weights or raw cluster progress |
| `cdc_value_consumer` | Identity consumer plus value CDC and normal reads for the same resources |
| `backup_exporter` | Explicit export plus required reads; no mutations or raw peer Snapshot |
| `operations_observer` | Global sanitized operational status/metrics; no implicit business reads |
| `security_admin` | Global recent-auth security management; no implicit data read/write/export |
| `cluster_replica` | Protected internal full replication, assigned by versioned peer trust; cannot be assigned to users through Admin |

Examples include an analytics reader excluding `customers:private:`, a service
editor on `orders:` with protected Delete, a maintainer on `cache:`, an offline
identity-only CDC consumer, a migration exporter and a security administrator
who cannot inspect application records.

## Queries, cursors, CDC and receipts

Authorization scope and ranking corpus are separate. Search candidates are
restricted before top-k selection; traversal never visits hidden vertices or
edges. Actual counts, degree/weighted-degree aggregates, prefix-delete victims,
exported topology, paging, and CDC remain scoped. Graph TF-IDF/BM25 base weights
reuse the existing graph-wide DF, tail count, and structural out-degree totals.
PPR transition normalization and community cuts still use only traversable
edges, so different scopes can produce different destinations and final scores.
Resource budgets bound physical posting/adjacency work, including rejected
candidates. Public authorized traversal failures expose the exhaustion reason
without physical scan counters; these counters are not public data aggregates.

Search reuses the existing index's field/class DF, N, document lengths, and
length totals. Private application records in the same corpus may affect visible
scores and order. The former requirement that denied-only data changes leave
visible scores/ranking unchanged is withdrawn. Do not build, cache, invalidate,
persist, or replicate per-Principal/Role ranking statistics. Search candidates
combine posting bitmaps and admitted matching IDs, without enumerating every
visible non-match. Query-time TTL cleanup maintains shared live search statistics;
graph structural statistics retain the existing bucket/GC semantics. Neither
kind of ranking statistic is an actual public count or permission check.

One existing GraphCache/search-index instance remains one corpus. Lantern has no
separate tenant/corpus selector today; do not infer one from an Issuer, Role or
key prefix, or share statistics across independent graph/index instances. Future
explicit tenant/corpus boundaries must retain their own statistics. Native
`sys:` metadata is outside business indexes and never affects data scores,
counts or paths; the physical `data:` prefix is excluded from document text.

Core receives immutable generic range constraints and owns corpus statistics;
Server interprets OIDC identities, Roles and policy. No policy callbacks, Store
locks, network calls, or credentials enter Core. Reuse shared postings and
prefix-range pushdown rather than copying/reindexing a graph per request.
Caches of protected results, pages and cursors bind identity, scope, query,
policy revision and generation; this is distinct from shared ranking statistics.
Unchanged Roles can share compiled state; unrelated session churn must not
recompile all data policies. Cursors are confidential and integrity protected
(AEAD or opaque Server handles), not merely signed/base64 physical keys.
They are bound to the actor/scope/query/revision/generation; policy changes
invalidate them. Retained Search pagination keeps its stable snapshot across
ordinary graph churn and remains node-local until portable state is designed.

The staged switch's [local component comparison](../../testbed/bench/evidence/issue-1612/shared-ranking/README.md)
records first requests, fresh/reused scopes, mixed updates/deletes, allocations
and retained-memory diagnostics. It does not complete production OIDC or final
exact-source acceptance.

Public scoped CDC is separate from raw peer Subscribe/Snapshot, while reusing
the log/projector machinery. Identity projection carries no values, effective
weights, receipt payloads or hidden sequence/HLC progress. Opaque cursors encode
only authorized progress; Role or scope changes, including expansion, require
rebootstrap. Gaps, duplicates, chunk boundaries, cancellation, failover and
bounded backpressure have explicit outcomes. Ordinary TTL expiry still needs
client expiry handling; the mutation stream does not invent an expiry event.
No network, storage lookup or expensive policy walk runs under `Log.subsMu`.

Receipt identity remains the original logical canonical intent, independent
of physical mapping. Preserve original effective `float32` result bytes,
including historically accepted NaN/Infinity. Proven resource provenance must
survive WAL, replication, private Snapshot, retirement and restore. An operation
ID is not authority; unknown IDs cannot be queried broadly to infer absence.
The staged Server records exact original logical Vertex/Edge identities in
bounded native receipt rows, checks them against canonical mutation envelopes,
and preserves them through active/retired Snapshot and backup recovery. Core
owns only opaque comparable identities, byte bounds and original effect bytes.
Status and replay require `receipt.read`, ordinary reads and the original
mutation's actions for every original resource, including both Edge endpoints.
Put/Add endpoint-creation rights still apply. The origin's application-time
lifecycle-reduction bit preserves a Put's Delete requirement after Graph churn;
an ordinary live Put does not acquire that requirement. These checks use the
captured local policy cut, with the admission fenced before publication.
Mixed-scope status batches fail atomically with a generic permission error.
Unknown/expired IDs without resource evidence fail closed for scoped callers,
without returning absence or a result. Unproven legacy rows require explicit
whole-domain original actions, including conservative Delete for legacy Put;
whole-domain absence requires explicit whole-domain receipt/data reads.
No client-supplied resource is accepted as status evidence, and public results
omit resource/effect metadata. Production activation and final acceptance remain
gated by #1613/#1610. Possibly sent operation IDs never change during namespace
migration; scoped offline clients must retain an unknown ID after denial and
cannot assume automatic status-first retry is qualified.

## OIDC and browser session boundary

Use registered Issuers only. Verify exact Issuer, audience, permitted algorithm,
signature, expiration, not-before, required claims and token type. The API accepts
the RFC 9068 access-token profile (`at+jwt`), not a browser ID token. Pin each
Issuer's API audience and client policy; untrusted token metadata cannot select
arbitrary Discovery/JWKS destinations. Discovery/JWKS have bounded responses,
timeouts, cache lifetimes and unknown-key refreshes; redirect, DNS rebinding and
private-address defenses apply to each connection. A private IdP needs explicit
operator network policy. Secret handles bind to exact Issuer/client/endpoint;
Issuer management cannot redirect operator credentials.

The Server is the browser relying party: Authorization Code plus PKCE, state,
nonce, exact redirects and single-use bounded login transactions. A fixed
same-origin gateway routes callbacks to the pinned security writer. Exchange
and ID-token validation produce an opaque Secure/HttpOnly cookie; Admin does
not persist IdP tokens in localStorage. CSRF and exact-origin checks protect
cookie-authenticated mutations. Recent authentication is required for security
changes. Role-scoped data export uses explicit export/read grants and current
admission; machine exporters do not assert interactive recent authentication. Fixed session expiry, revocation and per-stream authority checks remain
independent of JWT expiry. Passwords, MFA and account enrollment stay with the
IdP; no password store, email linking, implicit group grants, SCIM or opaque
token introspection is introduced.

Admin displays effective Server permissions. It assigns users to Roles and
edits Role rules; it has no direct-user-permission UI. Issuer registration
grants no data access. CAS conflicts require a deliberate reload/review; no
optimistic grant or blind overwrite. Every query, graph layout, detail pane,
suggestion, cursor and metric cache is identity/scope/revision/generation-bound.
Logout, Role changes, cross-tab events, resume and late callbacks invalidate
old state. Already delivered data cannot be recalled.

## HA authority, leases and recovery

Application data remains leaderless and asynchronously replicated. Initially
one operator-pinned internal security writer owns control changes. There is
no automatic election, external coordinator or home-built consensus protocol.
Replicas consume signed complete security transactions/checkpoints through
the existing pipeline; they never LWW-merge independent security records.

A protected replica serves only with one coherent locally installed security
revision and a bounded renewable serving lease. A lease binds a fresh challenge,
node, generation, writer incarnation, revision and digest. Renewal is serialized
with writer publication. The validity deadline starts before the request is
sent and subtracts elapsed network time; a delayed response cannot extend it.
Activate a lease only after its exact state is installed. Renewal acknowledges
the writer's committed cut at issuance, not an indefinitely moving target.

Control results distinguish durable `committed`, cluster `pending` and
`globally_enforced`. Restrictive changes stop old lease renewal and invalidate
known holders. Globally enforced requires every outstanding holder's proof or
conservative expiry; connected acknowledgements alone are insufficient. The
enforcement bound includes clock-rate error, restart, suspension/VM pause and
response publication. Restart never restores a live lease. Already admitted
writes may finish; long queries and streams recheck before publishing further
results. No lease renewal is performed per key or under graph locks.

| Failure / transition | Required behavior |
| --- | --- |
| Graph replication lag | Existing data consistency contract; security authority must independently qualify |
| Security transaction incomplete, forked or out of order | No protected serving until a contiguous certified image is installed |
| Writer unreachable / partition | No new management/login; replicas stop protected serving when leases expire |
| IdP unavailable | No new login; locally verified sessions/tokens may serve only within their own validity and current security lease |
| Restrictive policy pending | Report pending and the enforcement deadline; do not claim global revocation from local durability |
| Manual writer replacement | Fence old process/network/credentials, certify current state and outstanding leases, install a new writer incarnation |
| Stale restore / sole-volume loss | Management-only reconciliation; no automatic promotion or grant continuity claim |
| Unknown session on lagging node | Deny; no arbitrary node issues/accepts an unobserved session |
| Mixed OFF/OIDC, namespace version or trust generation | Reject before bootstrap/full graph transfer; drain incompatible peers |

The staged private implementation uses TLS 1.3 with normal chain/hostname
verification plus one exact operator-approved SPIFFE URI SAN and SPKI digest.
It uses certificate files directly; no SPIFFE controller or runtime dependency
is introduced. A bounded, canonical Ed25519-signed membership manifest pins
workload IDs, fixed HTTPS origins, deployment, namespace format, homogeneous
public mode, CA digest and OIDC security generation/writer key. Versions never
renew expiry in place. A synchronized native checkpoint under the existing
FileWAL path lease retains the version floor through restart. Missing/damaged
state fails closed; a complete trusted-volume rollback still requires operator
fencing and cannot be detected merely by signing historical bytes.

Every private request checks current membership and an exact domain digest,
including reused inbound TLS connections. Original admission ends within
30 seconds and at certificate/membership expiry; removal cancels active streams
and bounds blocked writes. Public Bearer/cookie credentials, redirects, proxies
and plaintext fallback are refused on outbound peer requests. The initial client
uses one fresh TLS 1.3 handshake per bounded private RPC rather than pooling
certificate authority across requests. The handshake cost belongs to peer
renewal/catch-up, not each data RPC, and needs final #1610 measurement.

`LanternSecurityPeerService.RenewPolicyLease` exists only on this private mux.
Its receiver ID must match the authenticated workload. The writer serializes
challenge-bound signing with policy publication and returns the original complete
signed checkpoint only when the receiver's known digest differs. A known digest
is a transfer optimization, never serving proof. The replica atomically persists
and installs that exact cut before activating its process-local lease. Routine
renewal runs every five seconds, with a five-second request deadline; no network
call or system WAL write occurs in unchanged-policy data admission. Losing local
workload membership also invalidates protected public serving even if its last
policy lease has time remaining.

Peer authentication is separate from public mode: dedicated mTLS and
operator-managed versioned trust/membership, optionally an existing SPIFFE
identity. The protected replica Role cannot be assigned to a human. A security
domain cannot mix public OFF and OIDC replicas. Admitted peers replicate the
complete graph, sys state and receipt history, including mutations by users
whose Roles have since been revoked; public prefix filters never restrict peer
repair. Login transactions are writer-local and single-use; ordinary sessions
may serve on caught-up leased replicas. Graph failover does not imply seamless
control-plane or callback failover.

## Validation and rollout

Dependent Issues follow the native dependency order in #1599. A listener must
not enable a partially implemented OIDC/RBAC mode. Each external change has
same-PR real Connect happy and rejection/edge coverage; generated consumers
are regenerated, and relevant hot paths have benchmark scenarios.

Compare existing OFF, namespace-only OFF and OIDC/RBAC under matched visible
work, with unsaturated latency percentiles and separate capacity measurements.
Measure batches, shared statistics and constrained candidates, hidden-data ratios,
first/reused scopes, mixed updates/deletes, allocations and retained memory,
subscribers, TTL churn,
policy compilation, caches, system WAL/compaction, lease expiry and revocation.
Producer throughput alone does not prove CDC consumer latency. No performance
percentage is claimed without measurements. Avoid per-data fsync/network calls
and whole-graph copies on ordinary authorization paths.

Track merged source/CI, exact-final-source acceptance, publication and provider/
physical evidence separately. Run automated tests while implementing. Physical
device/provider qualification is the final phase, as requested; preliminary
branch or simulator success cannot close that phase.

## References

- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html)
- [RFC 9068: JWT access-token profile](https://www.rfc-editor.org/rfc/rfc9068)
- [RFC 9700: OAuth security best current practice](https://www.rfc-editor.org/rfc/rfc9700)
- [Replication RFC](../replication.md)
- [ADR 0010: bounded mutation receipts](0010-bounded-mutation-receipts.md)


### Standalone Create accepted effects and SDK reconciliation (#1626)

`CreateEdges` is canonical; `CreateEdge` forwards one item. Receipt-bearing
Create is one atomic logical batch of at most 10,000 items, never SDK-chunked.
The original resource, action and aligned outcome are sealed before WAL
publication. A confirmed replay returns the original outcomes without executing
Create again; later Edge/Vertex deletion or TTL expiry cannot be reversed by
that replay. Recovery applies only originally accepted positions, suppresses
expired effects/missing endpoints/newer causal floors and rejects a live
collision. Rejected original positions are never reevaluated after restore.

Create needs the complete directed `edge.create` pair and both `vertex.read`
grants. It does not require or imply `edge.read`, `vertex.write`, `edge.write`,
`edge.add` or deletion. Receipt status/replay additionally requires the matching
`receipt.read` selector and the current original Create/read authorization.
Standalone capability discovery advertises Create only to a captured admission
with a nonempty Create/read candidate scope; this is a family preflight, not
permission for any particular pair or a promise that a later retry is allowed.
HA capability discovery omits Create and HA ingress rejects it before effects.

Go, Node and Dart expose normal and receipt-bearing plural/singular facades;
Rust exposes normal plural/singular facades and typed accepted-effect CDC.
Normal Create is not automatically retried after response loss. Persist receipt
context and absolute expiration inputs before dispatch, or use the Dart/Node
operation-issuance TTL anchor. Malformed/unspecified/misaligned outcomes fail
closed; receipt-bearing uncertain results require original-ID reconciliation.

The private WAL union is now version 8 (`LRWU\x08`), with a typed standalone
Create effect discriminator and frozen Mutation descriptor fingerprint
`11a0ed2e1d6c418b461ae2c27aceda465289b95e53519145cddbba7f2780fa73`.
Previous union versions fail closed and require the documented offline migration;
raw graph decoding cannot downgrade a Create accepted effect. Public identity
CDC projects only `CREATED_AND_LIVE` Edge identities, with no rejected inputs,
endpoint auto-creation, source weights or receipt payloads. The typed full effect
is local WAL/full-CDC evidence and is rejected by peer ApplyMutation until the
HA arbitration gate above is complete.
