# ADR 0012: Default-off OIDC and Role-only prefix RBAC

- Status: Accepted design; implementation and qualification tracked separately
- Driving issue: [#1599](https://github.com/anaregdesign/lantern/issues/1599)
- Contract issue: [#1600](https://github.com/anaregdesign/lantern/issues/1600)

The 2026-10-05 owner-approved #1672 policy removes the blanket five-minute
authentication gate from ordinary management. Trust changes and effective
Security/Settings `write` expansion require per-operation reauthentication; machines
may perform authorized reference/status reads, but no management mutation.
The implementation is described below; final exact-source and provider/browser
qualification remain separate. The historical fixed-writer source
`93d537892f3025d4a1666dcab36c5748d5cfb9f6` used the blanket age gate.
#1608 owns the leaderless design; its selected G1 Profile B freshness target
and remaining proof/implementation are separate from that source history.

The #2076/#2085 permission model uses Allow-only `read`/`write` rules on
typed resources. #2084 replaces the former fixed Head predicate with one
cluster-wide `rr`/`ww`/`rw`/`wr` Edge policy, defaulting to `rr` for fresh state.
The 2026-10-04 Head-managed decision and its measurements remain historical
evidence; they do not qualify the new policy. Kubernetes/Helm is
deferred from this task; Docker/Compose/native and Server HA/security remain.

#1820 retires the pre-production fixed-writer/replica-lease implementation.
Public configuration is OFF or OIDC. OIDC uses original fixed-membership
quorum authority, separate origin signing custody, native interval time,
full-cut review/original-result reconciliation and bounded immutable output.
The [authority contract](../security-current-authority.md) owns its exact
encoding and recovery rules. Historical fixed-writer measurements do not
qualify this source. Provider, configured-source and physical-device evidence
remain separate exits in #1610. HA Edge Create stays closed pending its own
distributed absence/arbitration and endpoint/tombstone proof.

The Head source and response/SDK implementation were merged in
[#1661](https://github.com/anaregdesign/lantern/pull/1661) under
[#1626](https://github.com/anaregdesign/lantern/issues/1626). Final exact-source,
provider, device and HA qualification remain separate. HA Edge Create stays
disabled pending distributed absence/arbitration and endpoint/tombstone proof.

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

OIDC requires one human `LANTERN_SECURITY_CONFIG_JSON`, supplied to provisioning,
explicit first initialization and every normal start. It selects the exact HTTPS
public origin, current Issuers/secrets, private `material_root`, separate durable
`persistence_root`, target platform, fixed named topology and initial administrator.
Material retains identity keys/historical trust and seed identity only; it is not
a current configuration source. No generated-config reference, legacy file-based
startup recipe or automatic missing-state fallback remains.

Native certified compare-and-swap reconciles supplied configured Issuer intent
before public readiness. A node with different desired Issuer intent stays
unavailable. Later Issuer additions create no Principals, Roles or assignments;
managed policy and historical genesis remain intact. Initial administrator input
seeds fresh state only and asserts the original identity on resume; it never
undoes suspension or regrants permissions. No public desired revision is needed.
Unknown subjects are never inferred from email, display name or IdP groups.
See the [configuration guide](../../deploy/examples/auth/README.md#environment-json-startup).

Environment-owned `security_admin` assignments grant no data rights. Fresh
provisioning separately creates `data_admin` with all-logical-data read/write and
assigns it to the bootstrap identity as a removable, non-environment-owned
membership. It also creates `personal_user` while keeping registration OFF.
The full image preserves their locks, referential integrity and the last usable human
administrator. A named machine uses canonical `lnt_m1_` credentials with a
bounded positive lifetime at most 90 days. Only domain-separated token digests
enter durable state; plaintext tokens stay private. Current Role loss and
account suspension still deny requests. Machines may perform granted reads
and status reconciliation, but cannot mutate security policy, prove a human
reauthentication event or acquire workload membership.

Retired deployment-wide bearer/reflection-exemption settings cannot promote
an old token to machine or administrator authority. Only absent auth settings
select default OFF. Health remains structurally auth-exempt while readiness
still checks the actual serving owner. Schema and diagnostics need explicit
capabilities in OIDC.

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

A reviewed control operation binds its actor, original full ID, immutable
intent and full semantic cut. A genuine consumed credential and any required
post-review Code event become an original H owned by its origin's durable
serial reservation. Fixed-membership strict-majority consensus chooses the
exact original value; deterministic Apply installs the complete image and
retained original outcome. Generic HLC/LWW merging never combines security
records independently. Singular management mutations use the plural canonical
operation and retain request-index-aligned original results.

OIDC has a distinct typed durability lane even with graph-only application
data. Original M/P/B families, lower-bound proofs and custody have separate
identities, floors and ownership. Resume replays intact original state and
never restores volatile serving authority. Unknown, corrupt, partial or copied
state fails closed. Fresh initialization refuses retained or interrupted
families without rewriting them. No old signed revision/session import or
fixed-writer journal migration is provided.

Session issuance and revocation are durable control changes with fixed expiry;
data access does not update last_seen or sliding idle expiry. Capacity and
revocation reserves remain bounded. Receipt-WAL continuity, graph backups and
security custody qualify separate invariants. A stale backup or new generation
alone cannot establish current grants/revocations or recover a lost origin key.

## Permission rules and evaluation

A rule contains `permission: read|write` and one typed resource:
`data`, `security`, `settings`, `operations` or `schema`. There is no active
Effect/Deny or independently grantable operation name. Data rules contain
exactly one literal logical prefix or a trusted immutable user-UID prefix
selector; `""` explicitly selects all logical keys and `*` is literal.
Management resources have no prefix or personal selector. Operations/schema
support `read` only because they expose no write operation.

Matching positive rules from every assigned Role form a union. No matching
rule means refusal. Unknown resources, permissions, obsolete rule fields and
invalid resource/selector combinations fail before publication. Compile scopes
once per immutable image and capture a coherent view before graph locks; no
network or persistence lookup enters a graph lock. Internal operation names
remain exact original receipt/admission identities and map exhaustively to the
two grant permissions; they are never additional Role grants.

### RPC / permission / projection contract

| RPC family | Required permission and information boundary |
| --- | --- |
| GetVertex(s), ScanVertex(s)/keys, Count, Search, Illuminate, TopVerticesByDegree | Data `read`; filter before ranking, paths, actual counts and pagination |
| PutVertex(s), DeleteVertex(s) | Data `write` on every key, including lifetime reduction/deletion; detailed outcomes additionally need `read` on every key |
| DeleteVerticesByPrefix | Select bounded victims using Data `write`; dry-run and actual count are undisclosed unless the entire selected policy range is readable |
| GetEdge(s), ScanEdges | Data `read` on both endpoints, including missing results |
| Every Edge modification, contribution deletion and Edge TTL change | One global endpoint mode: `rr` = tail read/head read, `ww` = write/write, `rw` = read/write, `wr` = write/read; both endpoint reads separately govern detailed responses |
| DeleteEdgesByPrefix | The same selected endpoint mode before lookup and under the final view; dry-run/counts need both endpoint policy ranges readable |
| BackupSnapshot and scoped CDC (identity/value) | Data `read`; the explicit requested projection limits output, with no independent export/CDC grant |
| GetReceiptCapability | Current authority plus any nonempty Data `read` or `write` scope; static capability grants no resource/status disclosure |
| Receipt-bearing mutation/replay | Current original mutation authority; no extra Data `read` prerequisite; detailed effects need ordinary read on all original resources |
| GetReceiptStatus(es) | Separate Data `read` ingress plus current original-resource authority; unknown IDs need whole-domain read or fail closed |
| Security references, users, Roles, assignments, audit, issuer/Edge/registration policy reads | Security `read`; authorized machines may perform reference/status reads |
| Security/issuer/Role/assignment/session policy writes | Security `write`, qualified end-user, exact native CAS/original operation rules |
| Log and operational policy reads/previews | Settings `read` |
| Log and operational policy writes | Settings `write`, qualified end-user; mixed security/settings batches require both writes |
| Issuer trust or effective Security/Settings write expansion | Operation-bound signed post-review approval, exact actor/original ID/intent/full cut |
| GetServerStatus / GetReplicationStatus / metrics | Operations `read`; no implicit data grant |
| Reflection | Schema `read` in OIDC; explicit OFF behavior |
| Peer Subscribe / Snapshot / PeerStatus | Separate admitted peer identity; never a user Role grant |
| Health | Auth-exempt, content-free liveness/readiness |
| Any unclassified new RPC | Refuse until the central mapping is extended |

Write-only callers receive one complete-call `MutationAcceptance` with
`HANDLED_EFFECT_UNDISCLOSED`; every outcome, existence flag, weight, count and
unknown response field is empty. This depends only on the admitted policy and
public request. Conditional conflicts/private receipt dispositions cannot
reveal state through a separate success/error branch. Invalid input, authority,
transport and durability failures retain their normal error semantics. Original
receipt bytes remain private and unchanged. A mixed batch is wholly redacted
if any selected item is unreadable.

### Model version and retained state

Image model 3, its deterministic execution/model fingerprint and the new wire
rule schema are explicit compatibility boundaries. Previous Effect/action/Deny
policies, old SDK wire rules, configuration and signed native histories are not
accepted by this release. Startup fails closed; there is no automatic conversion,
permission expansion, reset or deletion of retained state. Backward compatibility
and a migration implementation are outside #2076's required scope.

### Management operation and actor policy

The following separates verified baseline behavior from the #1672 implementation.
Final exact-source and actual provider/browser qualification remain separate
acceptance exits. Every row retains valid authentication, exact verified identity,
credential/session expiry, current explicit Roles, CSRF/exact origin where
applicable, session/policy admission freshness, request validation,
environment-owned locks and administrator invariants. Writes retain expected
revision/CAS and immutable original Change-ID recovery (#1669/#1670).
Authentication-time freshness is independent of policy freshness.

| Historical operation / consequence | Fixed-writer `93d53789` | Historical #1672 implementation |
| --- | --- | --- |
| Management reference/read/status, including audit | Current `security.manage`; no five-minute gate | Same authority; authorized machines may read/reference/status, without mutation rights |
| Non-mutating ValidateIssuer network probe | Current `security.manage` plus five-minute signed evidence | Qualified end-user authority without an age gate; separate conservative probe admission excludes ambiguous/machine callers, whose probe qualification remains unresolved |
| Ordinary end-user Role creation/data grants/exact self-assignment and other changes without trust change or control-authority expansion | Apply and Store enforce five-minute signed evidence | Valid end-user authentication and current explicit authority; missing/older signed `auth_time` alone is allowed |
| Change accepted Issuer/credential trust or expand effective `security.manage` | Blanket five-minute signed evidence | Separate purpose proof binds actor, original ID, canonical v1 intent and full policy cut to a signed post-review event |
| Machine management mutation | No explicit human-only policy; a named machine's unknown authentication time fails the blanket gate | Explicitly prohibited, even if a machine has a Role containing `security.manage` |

Classify the full proposed policy by effective consequences. Any newly
effective Security or Settings `write` grant is control-authority expansion. A data permission grant alone is
ordinary; do not add a reauthentication requirement just because it expands
data access. Bootstrap can create a mutable scoped data Role and assign it to
its exact verified identity after #1672, preserving its environment-owned
`security_admin` assignment. Its separate fresh `data_admin` membership supplies
explicit data read/write and can be removed without ordinary restart restoring it. Missing
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
- Recheck current authority, credential/proof expiry and the exact reviewed
  cut before durable consensus entry. The immutable original ID, canonical
  intent and original consensus result permit one effect; exact retained
  retries return their original result before demanding a new proof. Outstanding purpose
  capabilities are process-bound and restart invalidates them.
- Preserve the old usable session after failed step-up. Recovery of an already
  committed same-ID operation needs no further reauthentication and never
  executes a new mutation. Current recovery/disclosure checks and original
  commit evidence still apply.

Login/session orchestration lives in Server `internal/authn`; browser/Connect
transport stays in `service`, and provider retains construction, certification
and lifecycle ownership. See [the extraction design](../login-session-workflows.md)
for concrete APIs and the unchanged opaque-evidence/output ordering.

Ordinary browser login and purpose navigation wait for the native qualified
lower endpoint to reach the newly saved transaction's creation upper endpoint
before exposing its authorization URL (#2094). `internal/authn` retains the
original expiry/cancellation and rechecks the exact Issuer/cut before binding
output; callback consumption keeps its whole-interval and single-use guards.

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
resources. Collection APIs instead operate on the positive authorized subset
before pagination, ranking, limits, counts and dry-run.

A Vertex owns incident-edge lifetime. Data `write` permits Vertex replacement,
TTL/lifetime reduction and deletion on its selected key. Incident-edge cleanup
is lifecycle maintenance. Already committed expiry and replication do not
reevaluate the original user's current Roles.

### Global-policy existing-endpoint connections (#2084)

Create/Add/Put/Delete/contribution and Edge TTL operations use the same captured
global endpoint mode. Fresh mode `rr` allows a reader to edit connections among
readable existing Vertices; it grants no Vertex write. Switching mode is a
native Security-write CAS operation and invalidates old policy-bound output.
Endpoint reads/writes may come from different Roles. There are no Role-specific
pair selectors or independently grantable Edge operations. Reverse orientation
uses the same selected tail/head order.

Protected Edge operations never create/resurrect Vertices or change endpoint
values/TTLs. Both endpoints must be live under the final atomic application
cut; the blind response hides a private liveness refusal. OFF retains its
legacy implicit endpoint creation contract. Generic Core receives immutable
endpoint filters, without learning the Role model or mode.

Standalone outcomes are `CREATED_AND_LIVE`, `EDGE_EXISTS`, `ENDPOINT_NOT_LIVE`
and `EXPIRED`, one per request position. Source weights must be finite and
nonzero so successful creation denotes a live Edge. Any unexpired contribution
counts as an existing Edge even if its current aggregate is zero; collision
never removes or rewrites contributions. Born-expired input is a no-op before
endpoint/absence checks. A successful earlier duplicate makes later positions
`EDGE_EXISTS`. Storage evaluates all conditions at one application-time cut.

Internal conditional results are bounded creation/no-change outcomes.
A caller missing either endpoint read cannot receive prior existence/collision details,
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
Activation also requires matching SDK outcomes, mode-aware Admin explanations and
real-wire/receipt/restart tests. Independent OIDC/RBAC deliveries continue while
this gate remains closed.

### Practical Role templates

Templates are explicit Roles, not extra privilege mechanisms. Operators select
positive namespaces and review effective union across assignments.

| Role | Scenario / permissions |
| --- | --- |
| `data_reader`, `app_reader` | Data `read` on an explicit prefix; includes query/CDC/export and Edge edits when global mode permits |
| `app_writer`, `data_maintainer` | Data `read` and `write` on an explicit prefix |
| `write_only` | Data `write`; mutation handling without effect disclosure |
| `ops_reader` | Operations `read`, with no implicit business data access |
| `settings_admin` | Settings `read`/`write`, with no security/data grant |
| `security_admin` | Security and Settings `read`/`write`; no data/operations/schema grant |
| `personal_user` | Data `read`/`write` using the trusted immutable assigned user UID |

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
Receipt-bearing mutations and confirmed original replay require the current
original mutation's authority. Vertex receipts map Put/Delete/lifecycle reduction
to Data `write`; Edge receipts use the selected global endpoint policy. Read
is checked separately for detailed effects. Status ingress retains Data `read`;
whole-domain absence needs whole-domain read. Any unreadable item redacts the
complete response; no result, existence, intent digest or effect count survives.
Stored original bytes are never replaced by the detached public projection.

Historical endpoint-creation effects still require their own explicit Vertex
write authority; a connection edit under `rr` cannot authorize endpoint changes.
The retained lifecycle-reduction bit and internal operation identity remain
original proof even though both Vertex write/delete map to the same grant.
Checks use one captured local cut, fenced before publication. Unauthorized
mixed-scope batches fail before effects; an authorized blind batch cannot
execute and subsequently fail only because its result was not readable.
Unknown/expired IDs without resource evidence fail closed for scoped callers.
No client-supplied resource is accepted as status evidence, and public results
omit resource/effect metadata. Production activation and final acceptance remain
gated by #1613/#1610. Possibly sent operation IDs never change during namespace
migration; scoped offline clients must retain an unknown ID after denial and
cannot assume automatic status-first retry is qualified.

## Private current-authority implementation

The separately constructed [current-authority/origin owner](../security-current-authority.md)
(#1722) extends the fixed-membership S1/S2/S3 components. It combines serialized
challenge-start renewal, conditional native UTC/elapsed intervals, actual
credential and one-use full-S1 purpose verification, durable serial/original H,
and bounded immutable output authorization. HLC/data semantics remain unchanged.
Approved A permits an exact preexpiry output
unit to finish arbitrarily late; it promises no physical-send deadline. The new
private APPLIED wait concerns cessation of new stale authorization and does not
reinterpret `globally_enforced`. Public S4 activation and final #1610 target,
provider and OS qualification remain separate; source-level/native focused
evidence is not deployment certification.

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
nonce, exact redirects and single-use bounded login transactions. A pinned
same-origin gateway routes each attempt to its eligible authority origin. Exchange
and ID-token validation produce an opaque Secure/HttpOnly cookie; Admin does
not persist IdP tokens in localStorage. CSRF and exact-origin checks protect
cookie-authenticated mutations. Ordinary login accepts provider SSO and missing
`auth_time` as unknown, retaining an older signed time without upgrading it.
Future or contradictory evidence is rejected; `iat`, callback time, consent and
account selection never supply authentication time. A Server-owned transaction
saves ordinary versus step-up intent independently from session replacement.
Operation approval requests fresh provider
authentication and essential signed
`auth_time`. Broad session step-up is retired. Ordinary qualified-human management has no
age gate; high-impact changes use a separate purpose callback and signed
post-review event, preserving the ordinary session and its fixed expiry.
Canonical state and recovery preserve unknown/old signed authentication facts.
Image and native protocol versions remain actual encoding contracts; the graph
namespace and receipt formats retain their independent versions.
Google Security bundle, extra Google claims and the associated app publication/
verification are optional outside baseline implementation, acceptance and
release gates. Ordinary Google login and actual provider-neutral ordinary
management remain separately required evidence. Role-scoped data export uses
Data `read` grants and current admission; machine exporters do not
assert interactive recent authentication. Fixed session expiry, revocation and
per-stream authority checks remain
independent of JWT expiry. Passwords, MFA and account enrollment stay with the
IdP; no password store, email linking, implicit group grants, SCIM or opaque
token introspection is introduced.

### Selected native provider/session extension (#1750)

The [native login design checkpoint](../native-login-sessions.md) fixes the
Server contract before consumer implementation. It selects a dedicated Connect
boundary, trusted Google/Apple client profiles and a distinct opaque native
end-user Bearer session under the existing current-v2 authority. Initial sessions
have fixed eight-hour expiry followed by explicit re-login, without refresh.
Google's plugin initialization nonce limitation is handled as an explicit
ID-token bearer-input policy; Apple requires an exact raw transaction nonce,
one Server-owned code exchange with client_secret_post, and a separate validated
Android Services-ID callback. Original signed authentication time is preserved.

This extension was selected before implementation at main `4a4fa11`; the current
candidate implements it. The document defines the evidence/profile fingerprint change,
Apply/current-output sequence, app-owned account isolation and bounded tests.
Browser PKCE/CSRF, RFC9068 human qualification, machine separation and existing
operation-bound approval remain independent. Source/CI, final-source functional
acceptance, four real-provider/platform exits and publication stay separate.

Admin displays effective Server permissions. It assigns users to Roles and
edits Role rules; it has no direct-user-permission UI. Issuer registration
grants no data access. CAS conflicts require a deliberate reload/review; no
optimistic grant or blind overwrite. Every query, graph layout, detail pane,
suggestion, cursor and metric cache is identity/scope/revision/generation-bound.
Logout, Role changes, cross-tab events, resume and late callbacks invalidate
old state. Already delivered data cannot be recalled.

## Fixed-membership authority and private replication

The original provisioned control voter set contains 1–31 members, including
even counts, and uses strict majority `floor(N/2)+1` for consensus and renewal.
Data-ring replication has a separate signed membership. A non-proposer voter
still votes and can read/apply chosen values; it cannot create/recover an
unchosen original H without its original origin custody. No automatic shrink,
live expansion, replacement writer or fallback to OFF is implemented.

Every protected request and output unit checks its current installed cut,
credential/session, workload eligibility and finite native renewal/time bound.
A partition or lost majority blocks new commits and renewals. Already expired
challenges, delayed signatures and process restart cannot extend the original
authority window. APPLIED proves an original committed result; STOPPED has its
separate conservative observation meaning. Approved A permits an exactly
authorized unit to complete late without claiming a physical-send deadline.

Private data replication retains TLS 1.3/mTLS, normal chain/hostname checking,
exact workload URI/SPKI and homogeneous signed domain membership in both OFF
and OIDC. The OIDC domain binds the full authority profile and generation;
the signed wire's retired writer-key slot must be zero. Per-request membership
and expiry checks remain active on reused connections and streams. Public
user credentials never grant membership. The old RenewPolicyLease service and
fixed-writer checkpoint transfer are removed; private consensus/original-H/
renewal routes belong to the actual quorum owner and never the public mux.

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
policy compilation, caches, system durability, quorum/time expiry and revocation.
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

Create uses the global endpoint mode, with both endpoints live at application
time. It grants no endpoint mutation. Receipt replay uses current original-
resource authority and status has separate read admission; a caller without
both endpoint reads receives only a typed
blind acknowledgement, never an original result that reveals ungranted Edge
state. The public return contract above must be implemented before new-policy
qualification; stored authoritative results remain private and unchanged.
Standalone capability discovery advertises Create only to a captured admission
with a nonempty global-mode candidate scope; this is a family preflight, not
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

The maintained native fixture exposes bounded stdin-only `-private-input` creation for Rust test inputs. Credentials/configuration never enter argv or a broadly created temporary file. Public startup failures contain only fixed categories and bounded typed readiness diagnostics: node/port, elapsed wait, probe category/HTTP status, child exit code, and a fixed classification of a bounded child log. The Rust fixture retains this diagnostic before removing private temporary state; arbitrary child log/exception contents stay private. Native Windows CI executes positive/negative DACL tests and the existing authenticated Rust wire cases; cross-compilation alone does not qualify Windows.

Native listener fixtures also retain passive readiness observations (#1791).
The authority reason comes from the same executed check and clock sample;
worker evaluation/completion/publication sequences distinguish pending work
from a completed refusal. Health records its actual static or runtime branch.
A static rejection leaves runtime explicitly unevaluated and only correlates
with the separately observed worker state. No diagnostic capabilities RPC or
extra authority check occurs. The existing 60-second default startup budget, 35-second
native authority time, custody and all serving predicates remain mandatory.
Only this fixture composition installs the observer before serving. It writes
fixed enum/numeric records directly to the existing private child log, outside
authority/gate locks and independently of the configured logging level, at most
once per 5 seconds and 64 records of at most 1024 bytes. The fixture's 64 KiB reader
and Rust's 4 KiB startup record retain explicit missing, first-observed, invalid,
incomplete, truncated, exhausted and stale states. Observation age measures time
since the fixture first read that sequence; worker age uses the observer's
monotonic clock, never the authority clock. These are separate observations,
not an atomic security/health cut or proof of a historical timeout's cause.

The Rust authenticated fixture uses `authfixture -allocated-nodes` instead of releasing reserved ports before startup. Each supervised production child reserves its actual wildcard public TCP listener and, for a cohort, its IPv4-loopback private listener at port zero. A nonce-bound, bounded local pipe exchange returns only allocation metadata so the supervisor can generate the exact HTTPS origins and signed membership. The child retains the same Go listeners and poller throughout; no descriptor import, cross-process socket duplication, second bind, or fallback bind occurs. Configuration then passes through the ordinary providers: public runtime/receipt/frame certification and private exact-runtime/replication/policy/workload checks precede adoption. Adoption acknowledgment precedes serving and does not replace verified-TLS Health/capabilities readiness. The supervisor publishes fixture metadata only after that existing readiness gate and reaps the entire cohort on failure. Its stdin pipe is also the child's lifetime signal, including on Windows. Without the explicit local launch argument, ordinary server startup still binds through the existing providers. Native platform lifecycle/conflicting-bind tests accompany the existing real authenticated Rust receipt and HA paths; the local launch capability supplies no current-authority or durability proof.
