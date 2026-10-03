# ADR 0012: Default-off OIDC and Role-only prefix RBAC

- Status: Accepted design; implementation and qualification tracked separately
- Driving issue: [#1599](https://github.com/anaregdesign/lantern/issues/1599)
- Contract issue: [#1600](https://github.com/anaregdesign/lantern/issues/1600)

The initial implementation supplies inactive policy/state foundations and a
fail-closed mode preflight. It does not enable OIDC: `LANTERN_AUTH_MODE=oidc`
currently rejects startup until the complete serving boundary is installed.
The following sections define the target contract, not completed qualification.

The staged native Store reserves a private nonexpiring GraphCache image and
reuses FileWAL framing, process ownership, sync and lower-bound tip proofs.
It verifies full signed history before installing the recovered image. Its
hard journal cap fails closed; checkpoint/rotation, audit, public namespace
mapping and serving leases remain prerequisites for production activation.

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
credential. During staged implementation, existing static-token behavior is
still the existing runtime; an unavailable new mode must be rejected rather
than advertised as working. Health remains structurally auth-exempt. Schema
reflection and global diagnostics have explicit capabilities.

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
| Illuminate / TopVerticesByDegree | `query` plus readable vertices/edges; operate on the authorized induced graph before scoring |
| SearchVertices | `query` plus `vertex.read`; membership and all scoring statistics use the authorized corpus |
| BackupSnapshot | `export` plus readable vertices/edges; include only the referentially closed authorized data subset |
| Scoped CDC | Explicit `cdc.identity`; values additionally require `cdc.value` and ordinary data reads; endpoints must both qualify |
| GetReceiptCapability | Authenticated, current authority; only supported capabilities applicable to the actor |
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

### Practical Role templates

Templates are explicit Roles with literal prefixes, not additional privilege
mechanisms. Operators may copy them for a namespace and add Deny exceptions.

| Role | Scenario / permissions |
| --- | --- |
| `namespace_reader` | Application reads, scans, Search and traversal in one prefix; vertex/edge read and query; no CDC/export/global diagnostics |
| `namespace_editor` | Reader plus vertex write, edge Add/Put; Delete denied; lifetime reduction denied |
| `namespace_maintainer` | Editor plus vertex/edge Delete and collection Delete in its prefix |
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

Query isolation happens before scoring. BM25 document frequency, corpus size,
field length, phrase evidence and fuzzy vocabulary use the visible corpus;
degree, graph document frequency, PPR and community conductance use the visible
induced graph. Adding hidden or system records must not change visible scores,
membership or order. TTL expiry invalidates any cached statistics even without
a new write. A global fast path is valid only after proving full coverage of
every required action with no effective Deny, and still excludes `sys:`.

Use prefix-range candidate pushdown, shared postings and request-local
memoization rather than copying/reindexing the whole graph per request.
Cache keys include identity, scope, query, policy revision and generation.
Unchanged Roles can share compiled state; unrelated session churn must not
recompile all data policies. Cursors are confidential and integrity protected
(AEAD or opaque Server handles), not merely signed/base64 physical keys.
They are bound to the actor/scope/query/revision/generation; policy changes
invalidate them. Retained Search pagination keeps its stable snapshot across
ordinary graph churn and remains node-local until portable state is designed.

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
Until scoped receipt provenance is qualified, scoped receipt use is unavailable;
only an explicit whole-data-domain receipt Role may use the qualified existing
protocol. Possibly sent operation IDs never change during namespace migration.

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
changes. Fixed session expiry, revocation and per-stream authority checks remain
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
Measure batches, scoped statistics, hidden-data ratios, subscribers, TTL churn,
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
