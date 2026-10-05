# Native security state and Role evaluation

This is the internal Server delivery for #1601, #1611 and the state/lease
primitives of #1608. It builds on the namespace foundation (#1617) and generic
Core query ranges (#1623). It does not enable production OIDC serving; the
runtime readiness guard remains until transport and application boundaries are
certified.

## One complete policy revision

The Server owns Issuer, Principal, Role, RoleAssignment, Session and named
MachineCredential records. No Principal has direct permission grants. Identity
is the exact Issuer/subject pair; neither email nor an asserted group links an
account or grants a Role. Machine credentials retain a domain-separated digest
of a random token, never its plaintext, and use explicitly assigned Roles.

Each immutable signed revision contains the complete bounded security image,
generation, sequence, previous digest, original writer identity and a bounded
change replay history. Management uses expected-revision comparison and a
client change ID. Retried identical retained changes return the original commit
proof; an unknown ID is indeterminate rather than proof of non-commit. Changes
require current security-management permission and recent authentication.
Bootstrap Issuer/subjects and machine assignments are operator owned; ordinary
management cannot replace them. Audit records retain bounded redacted identities
and commit outcomes, with capacity reserved for restrictive changes.

`GetSecurityChangeStatus` exposes only retained commit proof: the original
change ID, generation, revision, digest and enforcement. The unchanged signed
history does not retain request-aligned item outcomes. Apply owns item results
and replay acknowledgement; Admin preserves those only when its original Apply
response was retained. Response-loss recovery can prove the commit while item
results remain unavailable. The current fixed writer produces the reviewed CAS
revision plus one; this scalar is not leaderless ordering or proof of currently
effective grants. Unknown and retired IDs remain indeterminate.

Ordinary browser sessions preserve absent authentication time as unknown and
retain older signed evidence. Neither issuance, replacement, replication nor
recovery refreshes it. Explicit step-up and important management changes still
require a signed event within five minutes. The current image v2 / `LNSEC03` /
native binding v2 cohort rejects incompatible old state before admission or
durable-floor advancement; [operator recovery](oidc-operations.md#security-state-version-boundary)
requires a fenced, explicit decision rather than implicit migration/reset.

The native system lane uses existing mutation-log durability, ownership locks,
classified segments and an atomic selector. A complete revision is persisted
before publication. Rotation checkpoints retain replay evidence and recover
exactly the selected acknowledged cut. Indeterminate write/publication failure
poisons serving instead of accepting an unproven revision. No SQL database is
introduced and graph writes do not gain a security fsync dependency.

Core SystemMetadata is a bounded opaque image isolated from graph TTL,
eviction, search and topology. Its owner can stage a newer checkpoint against
the exact previous digest. Signature, history, freshness and writer authority
remain Server responsibilities; this primitive exposes no public sys CRUD.

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

Admission captures one verified identity, immutable revision, credential expiry
and local authority fence. Graph hot paths perform local checks without a remote
security lookup per key, edge, receipt or event. Policy-bound cursor/cache
bindings include Principal, generation, revision and digest.

The pinned writer signs challenge-bound leases for its current committed cut.
A receiver has one process-local challenge and one lease; response latency is
charged from challenge start. A new boot cannot reuse a persisted lease.
Expiry, policy mismatch, replay, backwards wall time or uncertain state fails
closed. Restrictive changes distinguish durable commit from cluster enforcement;
the writer accounts for old holders until their conservative expiry.

A replica checkpoint requires the original complete signed revision and a fresh
challenge proof for that exact cut. It cannot promote a replica to writer or
install stale bootstrap state. The mTLS workload/membership transport, renewal
workers, qualified clock assumptions and production readiness composition remain
#1608 work. These local primitives alone do not establish cluster freshness.

## Validation boundaries

Paired tests cover Deny precedence, immutable scope reuse/caps, management CAS
and replay, bootstrap locks, credential rotation, suspension/session invalidation,
signed history, fresh-process recovery, journal faults, rotation, replica
checkpoint proof, lease latency/replay/restart and admission expiry. Paired
benchmarks cover local admission, Role evaluation and scope reuse. Real-wire
authorization/CDC and final OFF/OIDC/HA performance/provider evidence are separate
deliveries, not completion claims for this internal layer.
