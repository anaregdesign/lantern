# S1 pure security reducer and Apply ledger (#1608)

This module is an **unwired S1 contract and deterministic implementation**.
No Store, route, authentication verifier, purpose consumer, WAL, private
replication, runtime selector or serving lease calls it. The existing fixed
writer and public v1 Change IDs remain unchanged.

The owner selected authorized-handoff recovery on 2026-10-05 at 08:57:51 UTC:
the exact immutable H whose authorization was consumed before its deadline
may first complete after expiry if original reviewed semantics still match.
Expiry, tab closure and transport timeout do not cancel H. This implementation
has no ambient clock, purpose consumption, age cap or cancellation command.

## Canonical identity and semantic observation

`FullChangeID` v1 binds domain, cohort, retry namespace and nonce. S2 must
establish namespace fencing across restore/rejoin. `OperationIdentity` owns
domain-separated bounded typed JSON bytes and binds exact actor, complete
original `SemanticCut`, ordered request items and every command option. It
excludes transport origins and credentials/purpose/quorum witness variants.
The first authentic variant owns its original H digest and CommitRef forever.
Getters return immutable values or detached bytes/items/images.

Canonical structures have fixed versioned field order, no maps/floats, and
retain request item/Role-rule order. They are internal S1 encoding contracts,
not a deployed public codec or a signature verification protocol.

The cut binds full compiled Image (Role/Deny, all identities, human enrollment,
Issuer configuration, sessions, machines, operator locks and audit), separately
sorted session-lineage floors, generation, semantic sequence, previous-cut
digest, successful semantic-event ancestry and explicit independent fences.
Every successful semantic operation advances ancestry, including a visible
policy ABA. NOOP, ledger refusal, replay and replaceable witnesses do not.

This is the approved conservative **exact full-semantic-CAS admission mode**.
It never rewrites original review/observation from control order. If grant G
first succeeds at C, a revoke reviewed at C fails CAS and requires new review
and a new ID. A successful fresh revoke observes G's accepted ancestry; G's
historical result remains established although its author loses current
authority. If revoke succeeds first, stale G fails CAS. This does not implement
the older exploratory concurrent-event union, concurrent Role heads or imports.
Those inputs are unsupported rather than interpreted as ordinary vertex LWW.
No causal invalidation of already established descendants is fabricated.

## Supported transition matrix

All management items use existing typed validation and detached full-batch
reduction. Compilation verifies every Role/Issuer/identity dependency and the
last active qualified OIDC human with effective `security.manage`. Unknown or
removed assigned Roles fail closed; no Deny dependency is silently omitted.

| Command | Supported semantic effect | Additional check |
| --- | --- | --- |
| issuer.put | Create/reconfigure exact Issuer, increment config revision | Trust purpose; operator lock; invalidate affected sessions and advance lineage; human enrollment remains revision-bound |
| issuer.disable / issuer.delete | Disable/tombstone Issuer and revoke its sessions | Trust purpose; operator lock; advance affected lineage; full qualified-admin closure |
| role.put / role.delete | Replace/delete exact Role within complete batch | All references; Deny precedence; effective expansion over every principal |
| principal.put | Create/activate/suspend exact identity | Cannot resurrect deleted principal; suspension revokes sessions and advances lineage |
| principal.delete | Tombstone exact identity and remove assignments | Revoke sessions and advance lineage; operator-owned memberships preserved |
| assignment.put / assignment.delete | Exact Role membership | Registered Role; no cluster_replica; operator-owned deletion forbidden |
| session.revoke_user | Revoke all subject sessions | Advance durable symbolic lineage even when no session exists |
| session.issue | Trusted verified Code issuance/enrollment or exclusive replacement | Exact self, active principal, Issuer incarnation, full review and lineage; original fixed times/expiry; predecessor ownership and nonrevocation |
| session.revoke | Authenticated exact self-session logout | Exact session ownership and lineage |

No generic identity linking exists in the current product: Principal identity
is exact issuer/subject or machine name. Bootstrap reconciliation, machine
credential mutation, concurrent-head imports/reconciliation, purpose approval,
automatic expiry deletion, cancellation and retirement commands are explicitly
unsupported in this S1 command surface. Their existing production entry points
remain unchanged. Session issue/logout are pure semantic contracts here, not
new routes or authentication/session minting paths.

Effective `security.manage` expansion, including Deny rule/assignment removal,
activation and indirect Role changes, requires exact operation-bound purpose.
Every Issuer trust operation does too. Data-only expansion remains ordinary.
Reserve eligibility is a separate full-closure proof: all effective global
capabilities and data intervals must be subsets, no trust change and no new
session issuance. Edge permissions derive from Vertex subsets. Machines and
unresolved actors cannot mutate; qualified human Bearer remains supported.

## Atomic Apply and trusted boundaries

`ApplyS1` accepts opaque `S1CertifiedNext`. Its private historical authorization
and prefix certificate have **no production constructor/verifier in S1**.
Tests explicitly simulate verified evidence. An exported caller Boolean cannot
claim authenticity. S2 must authenticate complete H, its original sealed
consume/deadline association, eligible provenance and purpose binding, origin
durability, historical membership and complete prefix/checkpoint ancestry.
Nonempty fixture digests and timestamps do not prove any of these obligations.

Apply requires the exact next slot, scoped membership/value and matching
installed predecessor. It validates authentic scoped H before ID lookup, then:

1. A known same identity returns the first immutable OriginalOutcome before
   current CAS/authority/purpose. Changed actor, content or review conflicts.
2. A certified retired namespace refuses revival. Otherwise absence remains
   unresolved, never definitive non-commit.
3. An unknown ID requires terminal metadata capacity, exact semantic CAS,
   deterministic current authority, purpose classification and all post-state
   invariants/resource checks.
4. One pure return contains the successor projection and first ID outcome
   together. Terminal no-effect CAS/authority/purpose/invariant/capacity outcomes
   are retained with immutable request-index-aligned item dispositions.

A malformed/unverified H or prefix changes nothing. Known duplicates/conflicts
and NOOP advance only the certified control position. State input remains
semantically unchanged. A full ledger cannot promise even a terminal refusal:
Apply returns capacity failure without advancing prefix. Rejected ordinary work
cannot spend restrictive metadata reserve. S2 must reserve enough capacity
before promising acceptance, including failure outcomes and recovery tails.

`S1Retention` is an opaque externally certified retirement input; S1 implements
no barrier, floor advancement, TTL, outcome pruning or checkpoint restoration.
The initial state constructor does not certify an arbitrary Image/checkpoint.
Independent fence updates, native byte/work accounting, accepted-tail bounds,
checkpoint ancestry serialization and rollback protection are S2 obligations.

`OriginalOutcome` is a historical fact. `CommitRef` omits replaceable ballot/
quorum witness and does not prove first choice time or current enforcement.
Lookup/background Apply does not grant current public disclosure rights;
authentication, origin protection and qualified freshness remain necessary at
future API adapters. Profile B serving/enforcement and clock/suspend proofs are
S3; no old numerical bounds become guarantees here.

## Validation

Focused typed tests exercise all supported management kinds, Deny expansion,
last qualified OIDC administrator closure, Issuer incarnation, immutable
identity/outcomes, equal-generation different policy, semantic ABA, authentic
scope before ownership, missing prefix, NOOP, terminal rejection replay,
restrictive capacity, retired versus unresolved absence, stale grant/revoke
ordering and session lineage/exclusive rotation. Bounded tests cover all 720
orders of six authentic origin/proof variants and all 24 deliveries of four
already certified slots. They validate the pure atomic result boundary, not
native durability, cryptography, consensus, timing or deployed freshness.

Run from `server/`: `go test ./internal/security -run '^TestS1' -count=1`.
