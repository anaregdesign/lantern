# ADR 0012: Default-off OIDC and Role-only prefix RBAC

- Status: Accepted design; implementation and qualification tracked separately
- Driving issue: [#1599](https://github.com/anaregdesign/lantern/issues/1599)
- Contract issue: [#1600](https://github.com/anaregdesign/lantern/issues/1600)

The 2026-10-05 owner-approved #1672 policy removes the blanket five-minute
authentication gate from ordinary management. Trust changes and effective
`security.manage` expansion require per-operation reauthentication; machines
may perform authorized reference/status reads, but no management mutation.
The implementation is described below; final exact-source and provider/browser
qualification remain separate. The historical fixed-writer source
`93d537892f3025d4a1666dcab36c5748d5cfb9f6` used the blanket age gate.
#1608 owns the leaderless design; its selected G1 Profile B freshness target
and remaining proof/implementation are separate from that source history.

The 2026-10-04 Head-managed Edge decision in #1626 supersedes configurable
directed prefix-pair Roles. The prior candidate and its measurements remain
historical evidence; they do not qualify the new policy. Kubernetes/Helm is
deferred from this task; Docker/Compose/native and Server HA/security remain.

The preserved runtime candidate composes native signed sys state, short-lived
policy authority, Role admission, logical data boundaries, public Security/
Changes/browser APIs and a separate signed-membership workload plane in the
production Wire graph. Source, CI, final exact-source acceptance and external
provider/clock evidence remain separate exit buckets in #1599/#1610. The Head
source and response/SDK implementation were merged in
[#1661](https://github.com/anaregdesign/lantern/pull/1661) under
[#1626](https://github.com/anaregdesign/lantern/issues/1626). Final exact-source,
provider, device and HA qualification remain separate. HA Edge Create stays
disabled pending distributed absence/arbitration and endpoint/tombstone proof.
Prior local conformance does not complete deployment or provider qualification.

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
regular private file (Unix owner-only mode such as `0600`/`0400`, or the Windows owner/DACL contract below; no symlinks). Its strict JSON array
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

The independently grantable data actions are `vertex.read`, `vertex.write`,
`vertex.delete`, `query`, `cdc.identity`, `cdc.value`, `export` and
`receipt.read`. Edge operation names identify derived checks, not independent
Role grants. Edge visibility requires `vertex.read` on both endpoints.
Every Edge modification requires tail `vertex.read` AND head `vertex.write`.
`vertex.read` includes a key's existence, value and TTL; `edge.read` includes
existence, effective weight, TTL and contribution identity. Global actions are
`operations.read`, `schema.read` and `security.manage`. Internal
`cluster.replicate` is a protected machine capability outside user Role CRUD.

| RPC family | Required actions and information boundary |
| --- | --- |
| GetVertex / GetVertices | `vertex.read` for every exact key, including missing results |
| PutVertex / PutVertices | `vertex.write` and `vertex.read`; conditional state and outcomes are observable; effective lifetime reduction additionally requires `vertex.delete` |
| DeleteVertex / DeleteVertices | `vertex.delete` and `vertex.read`; incident-edge cleanup is aggregate lifecycle maintenance |
| GetEdge / GetEdges | `vertex.read` on both endpoints, including missing results |
| CreateEdge(s), AddEdge(s), PutEdge(s), DeleteEdge(s), DeleteEdgeContribution(s) | Tail `vertex.read` AND head `vertex.write`; existing-state responses require a separate disclosure check; contribution Delete stays exact |
| ScanVertices / ScanVertexKeys / CountVerticesByPrefix | Only the `vertex.read` subset, before limits, counts and cursor generation |
| ScanEdges | Only the subset with `vertex.read` on both endpoints |
| DeleteVerticesByPrefix | Delete only the `vertex.delete` plus `vertex.read` subset; dry-run/count/limit use the same subset |
| DeleteEdgesByPrefix | Head-derived modification subset; dry-run, victim identities and actual counts additionally need both endpoint reads |
| Illuminate / TopVerticesByDegree | `query` plus readable vertices/edges; restrict paths and actual degree to the authorized induced graph; TF-IDF/BM25 use shared corpus statistics |
| SearchVertices | `query` plus `vertex.read`; restrict candidates before top-k; reuse this index's shared ranking statistics |
| BackupSnapshot | `export` plus readable vertices/edges; include only the referentially closed authorized data subset |
| Scoped CDC | Explicit `cdc.identity`; Edge identities require both endpoint reads; values additionally require `cdc.value` and ordinary data reads |
| GetReceiptCapability | Current authority plus `receipt.read` and `vertex.read` within an applicable logical scope; supported capabilities only |
| GetReceiptStatus(es) / receipt replay | `receipt.read` and current rights for every proven original resource; absence needs caller intent proof or fails closed |
| GetServerStatus / GetReplicationStatus / metrics | `operations.read`; never inferred from data read |
| Ordinary Principal / Role / assignment / session / audit management | Current `security.manage` and qualified end-user; no fixed authentication age; expected revision required on writes |
| Issuer trust / effective `security.manage` expansion | Exact final reviewed operation approval with a signed post-review event; actor/ID/v1 intent/full cut bound |
| Peer Subscribe / Snapshot / PeerStatus | Separate admitted peer identity and `cluster.replicate`; no human token or prefix filtering |
| Health | Auth-exempt, content-free liveness/readiness |
| Reflection | `schema.read` in OIDC; explicit OFF behavior |
| Any unclassified new RPC | Deny until the action matrix is extended |

### Management operation and actor policy

The following separates verified baseline behavior from the #1672 implementation.
Final exact-source and actual provider/browser qualification remain separate
acceptance exits. Every row retains valid authentication, exact verified identity,
credential/session expiry, current explicit Roles, CSRF/exact origin where
applicable, session/policy admission freshness, request validation,
environment-owned locks and administrator invariants. Writes retain expected
revision/CAS and immutable original Change-ID recovery (#1669/#1670).
Authentication-time freshness is independent of policy freshness.

| Operation / effective consequence | Fixed-writer `93d53789` | #1672 implementation |
| --- | --- | --- |
| Management reference/read/status, including audit | Current `security.manage`; no five-minute gate | Same authority; authorized machines may read/reference/status, without mutation rights |
| Non-mutating ValidateIssuer network probe | Current `security.manage` plus five-minute signed evidence | Qualified end-user authority without an age gate; separate conservative probe admission excludes ambiguous/machine callers, whose probe qualification remains unresolved |
| Ordinary end-user Role creation/data grants/exact self-assignment and other changes without trust change or control-authority expansion | Apply and Store enforce five-minute signed evidence | Valid end-user authentication and current explicit authority; missing/older signed `auth_time` alone is allowed |
| Change accepted Issuer/credential trust or expand effective `security.manage` | Blanket five-minute signed evidence | Separate purpose proof binds actor, original ID, canonical v1 intent and full policy cut to a signed post-review event |
| Machine management mutation | No explicit human-only policy; a named machine's unknown authentication time fails the blanket gate | Explicitly prohibited, even if a machine has a Role containing `security.manage` |

Classify the full proposed policy by effective consequences. Removing a Deny,
deleting a Role or deleting an assignment can expand `security.manage`, including
latent authority in another assigned Role. A data permission grant alone is
ordinary; do not add a reauthentication requirement just because it expands
data access. Bootstrap can create a mutable scoped data Role and assign it to
its exact verified identity after #1672, preserving its environment-owned
`security_admin` assignment and receiving no implicit data rights. Missing
`auth_time` alone no longer requires extra initial operator provisioning.

Identity kind is not credential provenance. Both a verified browser Code/PKCE/
nonce exchange and an RFC 9068 Bearer can map to an OIDC `(iss, sub)` Principal;
that pair or `KindOIDCPrincipal` alone cannot establish whether the Bearer
represents an end user or a machine. Legitimate end-user Bearer management
eligibility is preserved; no browser-only restriction is selected. The trusted
classification separates browser Code, RFC 9068 Bearer and native machine
provenance from end-user, machine and unresolved actor classes. Bearer human
eligibility requires exact durable human enrollment plus an operator-qualified
Issuer contract preventing client-subject collision and impersonation. Existing
mixed profiles require actual issuance qualification before activation; the
default does not infer trust from an OIDC kind or signed authentication time.
Issuer profile configuration that changes accepted credential trust itself
belongs to the high-impact set.

The operation-purpose proof contract is:

- Bind Server-owned purpose to canonical reviewed intent, expected state and
  original Change-ID; bind state/nonce to the same verified Issuer and subject.
- Verify a qualified signed authentication event for that operation. A generic
  recent-session flag, `iat`, callback time, consent or account selection is not
  a proof. Navigation starts no earlier than the next whole NumericDate second
  after final review, and the signed event must meet that boundary. Purpose
  state uses the existing bounded ten-minute login lifetime.
- Under the Store lock, recheck current authority, credential/proof expiry,
  serving fence and CAS immediately before durable entry. Immutable original
  ID plus fixed-writer CAS permits one effect; exact retained retries return
  their original result before demanding a new proof. Outstanding purpose
  capabilities are process-bound and restart invalidates them.
- Preserve the old usable session after failed step-up. Recovery of an already
  committed same-ID operation needs no further reauthentication and never
  executes a new mutation. Current recovery/disclosure checks and original
  commit evidence still apply.

The purpose callback branches before ordinary session issuance or cookie writes;
generic session step-up cannot authorize an operation. A typed, fully bound
pre-persistence authorization refusal permits same-ID proof reacquisition only
for a first previously unambiguous Apply; earlier uncertainty stays status-only.
Malformed, future or contradictory signed times and invalid session
creation evidence remain rejected independently of the removed ordinary gate.
Ordinary management remains exposed for the actual valid credential/session
lifetime; it receives no implicit extension or automatic machine grant.

An exact batch with any denied identity is rejected before lookup or mutation,
including a batch mixing visible and denied keys. Responses must not reveal
existence, missing keys, conditions, weight, TTL or receipt contents of denied
resources. Collection APIs instead operate on an authorized subset, subtracting
all Deny ranges before pagination, ranking, limits, counts and dry-run.

A Vertex owns incident-edge lifetime. An authorized Vertex Delete or expiry
removes incident edges as lifecycle maintenance. Head write does not authorize
Vertex deletion or shortening a Vertex lifetime: Vertex lifetime reduction or
born-expired Put still requires `vertex.delete` and cannot bypass its Deny.
Edge lifetime reduction/deletion uses the same Head-derived modification
predicate as other Edge changes. Already committed expiry and replication are protected
system maintenance, not new actions by the original user's current Roles.
If an operation cannot prove its effects safely, reject it before mutation.

### Head-managed existing-endpoint connections (#1626)

A separate plural-canonical `CreateEdges` / singular `CreateEdge` family uses
tail read and head write, like Add/Put/Delete/contribution and Edge TTL changes.
Both endpoint Vertices must exist and be live at the same atomic application-time
cut as Edge absence. Neither endpoint read/write nor its existence is inferred
from a caller's owner assertion. The operation
never creates/resurrects a Vertex or changes its value/TTL. No omitted option
may broaden this existing-endpoints-only default.

Role selectors are literal prefixes or explicit global capabilities. Generalized
directed pair configuration and independent Edge grants are withdrawn. Tail
read and head write may come from different assigned Roles; each constituent
check independently applies Deny precedence. Reversing an Edge changes which
endpoint needs write. Tail read Deny or head write Deny defeats every mutation.
Head write never grants VertexDelete or endpoint value/TTL updates. Obsolete
pair/Edge-grant configuration must fail closed, not be converted into broader
Vertex authority. No permanent alternative policy mode is introduced.

Server derives generic endpoint/path constraints for Core. Graph exploration
requires both endpoint reads and its independent Query capability. CDC,
export and receipt capabilities stay independent and compose with base Edge
visibility/current original-resource authorization. Admin edits Vertex grants
and asks Server to explain the derived Edge check and its constituent rules.
Client prefix/identity assertions grant nothing. Dynamic owner templates are
outside this initial design.

Legacy OFF Add/Put accumulation, replacement and endpoint auto-creation remain
unchanged. Protected Edge writes cannot infer tail Vertex creation/update from
head write. The OIDC mutation matrix must certify existing/live endpoints at
application time for a new connection and must not resurrect endpoints or
change their values/TTL through an Edge operation. These constraints have no
OIDC/Role meaning inside Core.

Standalone outcomes are `CREATED_AND_LIVE`, `EDGE_EXISTS`, `ENDPOINT_NOT_LIVE`
and `EXPIRED`, one per request position. Source weights must be finite and
nonzero so successful creation denotes a live Edge. Any unexpired contribution
counts as an existing Edge even if its current aggregate is zero; collision
never removes or rewrites contributions. Born-expired input is a no-op before
endpoint/absence checks. A successful earlier duplicate makes later positions
`EDGE_EXISTS`. Storage evaluates all conditions at one application-time cut.

Internal conditional results are bounded creation/no-change outcomes.
A head write-only caller cannot receive prior existence/collision details,
weights, TTLs, contributions or original receipt results. The user selected
typed blind acceptance: policy-authorized input is handled, but the response
does not assert mutation success, liveness, creation, deletion or collision.
SDKs must represent undisclosed effects explicitly and never fabricate a
weight, existence flag, effect count or detailed outcome from a placeholder.
Authentication, policy rejection and input-format errors remain distinguishable
before resource lookup/effects. Batch positions, receipt replay/status,
idempotency conflicts and current permission changes must obey the same
disclosure rule. No mutation may be applied and then reported as an ordinary
permission failure. OFF legacy results remain unchanged. Duplicate
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
Activation also requires matching SDK outcomes, Head-aware Admin explanations and
real-wire/receipt/restart tests. Independent OIDC/RBAC deliveries continue while
this gate remains closed.

### Practical Role templates

Templates are explicit Roles with literal prefixes, not additional privilege
mechanisms. Operators may copy them for a namespace and add Deny exceptions.

| Role | Scenario / permissions |
| --- | --- |
| `namespace_reader` | Vertex read and Query in explicit prefixes; readable Edges follow both endpoint reads; no mutation/CDC/export/global diagnostics |
| `namespace_editor` | Reader plus Vertex write; manages incoming Edges whose tails are readable, including Edge Delete/TTL; no VertexDelete |
| `namespace_maintainer` | Editor plus explicit VertexDelete and collection Vertex lifecycle operations |
| `head_relationship_manager` | Tail-prefix VertexRead and head-prefix VertexWrite through explicit Roles; same base authority for every Edge mutation; head read is separately granted |
| `cdc_identity_consumer` | Explicit identity invalidations; Edge events also require both endpoint reads; identity frames omit values/weights/raw cluster progress |
| `cdc_value_consumer` | Identity consumer plus value CDC and normal reads for the same resources |
| `backup_exporter` | Explicit export plus required reads; no mutations or raw peer Snapshot |
| `operations_observer` | Global sanitized operational status/metrics; no implicit business reads |
| `security_admin` | Global security management; high-impact operation proof; no implicit data read/write/export |
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
Status and replay require `receipt.read` and the current original mutation's
authority for every proven original resource. Vertex receipts retain their
ordinary Vertex read/write/delete requirements. Edge authority is tail read
and head write; both endpoint reads additionally determine disclosure of the
original result. A policy-authorized head write-only caller gets typed blind
acceptance without the original result, existence, intent digest or effect
count. A batch with any undisclosed Edge result gets a whole-batch blind
acknowledgement, without revealing detailed results for its other positions.
Stored original bytes are never replaced by the redacted response.

Legacy endpoint-creation effects require their own explicit Vertex authority;
Head ownership cannot authorize them. The origin's application-time
lifecycle-reduction bit preserves a Vertex Put's Delete requirement after
Graph churn; an ordinary live Vertex Put does not acquire that requirement.
Every Edge TTL/deletion effect uses the same Head mutation predicate. These
checks use the captured local policy cut, fenced before publication.
Unauthorized mixed-scope batches fail atomically with a generic policy error;
an authorized blind batch must not execute and then fail on disclosure alone.
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
cookie-authenticated mutations. Ordinary login accepts provider SSO and missing
`auth_time` as unknown, retaining an older signed time without upgrading it.
Future or contradictory evidence is rejected; `iat`, callback time, consent and
account selection never supply authentication time. A Server-owned transaction
saves ordinary versus step-up intent independently from session replacement.
Explicit session step-up and separate operation approval request fresh provider
authentication and essential signed
`auth_time`. Generic session step-up requires a signed event
within five minutes. This generic session step-up does not authorize a reviewed
management operation. Under #1672 ordinary qualified-human management has no age
gate; high-impact changes use a separate purpose callback and signed post-review
event, preserving the ordinary session. A failed step-up retains the old session.
Canonical state,
replication and recovery preserve unknown/old evidence. This interpretation uses
security image v2, `LNSEC03` and native binding v2; old cohorts fail closed before
admission or durable-floor advancement, without automatic migration or mixed
rolling acceptance. The graph namespace and receipt formats do not change.
Google Security bundle, extra Google claims and the associated app publication/
verification are optional outside baseline implementation, acceptance and
release gates. Ordinary Google login and actual provider-neutral ordinary
management remain separately required evidence. Role-scoped data export uses
explicit export/read grants and current admission; machine exporters do not
assert interactive recent authentication. Fixed session expiry, revocation and
per-stream authority checks remain
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

## Fixed-writer HA baseline: authority, leases and recovery

This section records the merged `93d53789` implementation, including its
timings. #1608's leaderless security target is not implemented by these leases.
The selected G1 Profile B target requires isolated/stale nodes to stop
authorization-required processing within a proven bound. Its numerical bound,
protocol and clock/suspension proof remain pending; Go monotonic elapsed time
alone does not certify suspension. #1609's [remaining S5 contracts](../oidc-operations.md#leaderless-s5--remaining-contracts-and-qualification)
follow #1608 S2–S4, including eligible-node/per-attempt routing, per-origin key/
secret custody and reviewed versioned migration. Preserve fixed-writer history
without claiming target acceptance.

Application data remains leaderless and asynchronously replicated. In this
baseline one operator-pinned internal security writer owns control changes. There is
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
publication. An authorized confirmed replay returns the original outcomes only
with current disclosure rights; otherwise it returns blind acceptance without
executing Create again. Later Edge/Vertex deletion or TTL expiry cannot be reversed by
that replay. Recovery applies only originally accepted positions, suppresses
expired effects/missing endpoints/newer causal floors and rejects a live
collision. Rejected original positions are never reevaluated after restore.

Create needs tail `vertex.read` and head `vertex.write`, with both endpoints
live at application time. It grants no endpoint mutation or VertexDelete.
Receipt status/replay additionally requires explicit `receipt.read` and current
original-resource authority; a head write-only caller receives only a typed
blind acknowledgement, never an original result that reveals ungranted Edge
state. The public return contract above must be implemented before new-policy
qualification; stored authoritative results remain private and unchanged.
Standalone capability discovery advertises Create only to a captured admission
with a nonempty Head-derived candidate scope; this is a family preflight, not
permission for a particular Edge or a promise that a later retry is allowed.
HA capability discovery omits Create and HA ingress rejects it before effects.

Go, Node and Dart expose normal and receipt-bearing plural/singular facades;
Rust exposes normal plural/singular facades and typed accepted-effect CDC.
Normal Create is not automatically retried after response loss. Persist receipt
context and absolute expiration inputs before dispatch, or use the Dart/Node
operation-issuance TTL anchor. Malformed/unspecified/misaligned outcomes fail
closed; receipt-bearing uncertain results require original-ID reconciliation.

The private WAL union is now version 9 (`LRWU\x09`), with a typed standalone
Create effect discriminator and frozen Mutation descriptor fingerprint
`92a299d04670341c8a9d73113dc712e7dabd0a56b7ba840b0d769fc10e3d384a`.
Previous union versions fail closed and require the documented offline migration;
raw graph decoding cannot downgrade a Create accepted effect. Public identity
CDC projects only `CREATED_AND_LIVE` Edge identities, with no rejected inputs,
endpoint auto-creation, source weights or receipt payloads. The typed full effect
is local WAL/full-CDC evidence and is rejected by peer ApplyMutation until the
HA arbitration gate above is complete.


### Immutable endpoint effects and retained private history

Protected public Add/Put requires both existing live endpoints under the final
Core application lock. An entire batch fails atomically if either endpoint is
not live. A caller without complete EdgeRead receives only typed handled/effect-
undisclosed acceptance for that private liveness disposition. Malformed input,
authority, transport and WAL failures retain their ordinary failure contract.
Auth OFF keeps legacy implicit endpoint creation at public origin.

The origin records `MutationOp.no_endpoint_creation` for protected Edge Add/Put
and receipt Add. Every peer, relay, accepted-effect WAL record and restart applies
that immutable Edge-only effect; it never inserts, revives, or extends a Vertex.
Peers do not recheck origin liveness against asynchronously replicated state.
Receipt Add binds this effect into its immutable intent digest. A misplaced flag
fails before graph/WAL/origin progress. HA Create remains unavailable.

Production private graphs retain pending accepted Edge sources through GC and
Snapshot until their own expiration or causal removal. Physical Edge capacity
accounts for these buckets at local admission; replica union preserves accepted
remote effects and can exceed that local cap. Nil-TTL sources may remain until
causal removal. This does not introduce a separate pending queue or promise a
hard memory bound.
Private `SnapshotEdge.no_endpoint_creation` permits missing explicit endpoints
and restores only Edge sources. Public reads, counts, traversal and graph export
continue to require live endpoints. Private archive V4 pins the reachable
Snapshot descriptor to
`a8c8af1e98800f47365f1b25711019315f07bef319490243401e9b1e4ed6d662`.
V3 archives and V8 WAL unions require explicit offline migration and fail closed;
there is no silent reinterpretation of existing persisted bytes.

### Consumer presentation of undisclosed handling (#1626)

Primitive SDK facades return a dedicated complete-call `MutationAcceptance`
signal; typed reply adapters expose `knownEffect` or `acceptedUndisclosed`.
Only the direct acknowledgement becomes a handled reply. Wrapping, partial
batches and genuine failures retain their error/reconciliation semantics.
Neither branch may infer an effect from a zero weight, false existence flag,
empty outcome vector or count.

Admin Edge forms clear stale values and show a handled/effect-undisclosed state
without reading the Edge back or claiming it is absent. Admin CLI and Go CLI
emit only an explicit acceptance object in that case. Streaming bulk and
Restore consume the complete input and preserve any later failure; they redact
final effect counts if any chunk was undisclosed. An acknowledgement does not
invite automatic retry.

The offline candidate persists `acceptedUndisclosed` as a terminal operation
state in codec version 3, removes the outbox entry and invalidates confirmed
cache under existing ownership/generation/authentication fences. It never
fabricates confirmation, a receipt original or a reusable cache image.
Paired-source Dart checks and hosted-archive/publication/device exits remain
separate; publication is deferred until the end of the implementation work.

### Native private-file contract (#1650)

Private operator keys, bound OIDC secrets, peer checkpoints and native security/receipt WAL metadata use generic Core file-security primitives. Unix retains owner-only mode validation. Windows validates the opened handle's owner and DACL: the owner must be the current Server account; the DACL must be protected and contain one explicit recognized owner-only file grant with read access. Null/empty, broad, inherited, unprotected, unknown-mask/ACE and reparse configurations fail closed. A Windows read-only attribute or `chmod(0600)` is not privacy evidence.

Private creation installs the owner and protected DACL in `CreateFile` before any content is written. Checkpoints/manifests use private temporary files and preserve file-sync, replacement and native directory-flush errors; failed directory flushes never become successful durability acknowledgements. Restart validates native metadata before replay. Provision Windows secrets for the actual Server account rather than assuming an inherited Administrators/Users ACL is accepted. This is file privacy and local persistence admission, not a claim of survival after sole-volume loss or every storage stack's power failure.

The maintained native fixture exposes bounded stdin-only `-private-input` creation for Rust test inputs. Credentials/configuration never enter argv or a broadly created temporary file. Public startup failures contain only fixed categories; arbitrary child log/exception contents stay private. Native Windows CI executes positive/negative DACL tests and the existing authenticated Rust wire cases; cross-compilation alone does not qualify Windows.
