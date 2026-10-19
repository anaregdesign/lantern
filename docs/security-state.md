# Security state, Role evaluation and current admission

The Server owns the complete bounded security image and its authorization
policy. OFF remains unauthenticated. OIDC uses the sole independently
provisioned quorum authority described by the
[authority contract](security-current-authority.md). Local component evidence
does not establish configured-source, provider, physical-device or deployment
qualification. The fixed-writer Store, signed-revision journal, replica policy
leases and their compatibility selectors were retired by #1820.

## Immutable policy and original operations

Issuer, Principal, Role, RoleAssignment, Session and named MachineCredential
records remain Server-owned. No Principal has direct permission grants. OIDC
identity is the exact verified Issuer/subject pair; email, asserted groups and
client-selected names cannot enroll a human or grant a Role. Machines store
only domain-separated credential digests and receive explicit Role assignments.
Their original bounded token expiry and current suspension/Role checks remain.

Prepare binds canonical management v1 business intent, original Change-ID,
actor and the complete current authority cut. Apply retains that original ID
and result through durable consensus/materialization. Retries never substitute
a new ID or infer non-commit from an unknown status. A lost response requires
status reconciliation under currently valid disclosure authority; an APPLIED
original result is distinct from current readiness and from local cookie
clearing. Scalar revision fields are wire metadata, not independent authority.

Ordinary qualified-human management accepts missing or older signed
`auth_time`; malformed, future or contradictory evidence still fails. Issuer
trust changes and effective `security.manage` expansion require approval of the
exact final reviewed operation. This includes Deny removal, Role deletion or
unassignment and activation. Machines may perform granted reference/status
reads, but cannot mutate management or approve a human authentication event.

Purpose approval binds the actor, original ID, canonical intent, full cut and
current Issuer configuration. Its verified signed event must satisfy the
qualified whole-second boundary after review. `iat`, callback time, consent,
account selection and a generic recent-session flag cannot prove that event.
A purpose callback branches before ordinary session issuance and preserves the
old usable session on success, refusal or abandonment. Pending process-owned
approvals expire within the existing login transaction bounds and do not
survive restart; retained original commits do.

Operator-owned Issuers, administrator assignments and machine credentials
preserve their original locks. Management retains referential integrity and
at least one active qualified human with effective management authority.
A current OIDC Principal kind or signed authentication time alone does not
establish the Issuer's noncollision/nonimpersonation contract. Exact durable
human enrollment and its current Issuer configuration remain required.

The original M/P/B journals, materialized state, custody floors and exact
provisioning identities certify durable continuity together. Indeterminate
persistence or publication fails closed. Never relabel an old fixed-writer
journal, import its signed history, reset failed state automatically or reuse
another node's private files. Fresh and intact resume are explicit operator
choices; deferred backup/clone/restore paths remain closed. See
[operator recovery](oidc-operations.md#security-state-version-boundary).

## Role-only scopes

Matching Deny wins across all active assigned Roles; otherwise a matching Allow
is required. Prefixes are literal strings without wildcard, case or Unicode
normalization. Empty-prefix access is an explicit all-keys choice. Edge access
requires both endpoints. Actions are independent: reads do not imply Query,
CDC, Export, mutation or security management.

The compiled policy returns immutable disjoint key ranges for the intersection
of requested actions. A bounded cache belongs to one immutable Role policy and
coalesces equivalent Role sets, never ranking statistics. Old queries may retain
their captured ranges safely. Server adapters translate ranges to Core
constraints; Core does not interpret Roles, OIDC or policy rules. Role templates
and explain output use the same effective Allow/Deny evaluation.

## Serving authority

Only the actual current owner constructs an Admission. It captures verified
credential provenance, immutable policy and the full current authority cut.
Pure Role evaluation accepts a compiled policy view; it cannot manufacture
serving admission, native time, original-H custody or a public output grant.

Current quorum renewal, original-H reservation, native UTC/elapsed bounds,
revocation and immutable output ownership remain separate checks. Loss of a
required proof stops protected processing under the existing finite bounds.
Data availability, a replicated image, readiness metadata and a successful
workflow return are not substitutes for those proofs. Workload membership is
independently signed and authenticated with TLS 1.3/mTLS in both public modes.

Current data adapters bind exact resources and original receipt/disclosure
rules to one immutable output grant. Session issuance requires Apply of the
original session operation, a fresh request admission for the same Code event,
and issued-session output binding before exposing secrets. Revocation preserves
original result reconciliation separately from cookie deletion. Scoped cursors,
caches, streams and exports retain their policy/request/corpus bindings.

## Validation boundaries

Paired tests cover Deny precedence, immutable policy scopes, original operation
conflicts/replay, operator locks, human/machine classification, credential and
session revocation, journal faults, original-H custody, quorum/time expiry,
output ownership and intact restart. Root real-wire cases exercise production
credential, management, browser, data, CDC and receipt adapters with independent
original provisioning. Their coordinated controlled-source F and configured-source
Q executions remain distinct; a skipped test or parser-only fixture is not a
qualification result. Final SDK, provider, device, HA and performance evidence
must name its actual frozen source and executed boundaries.
