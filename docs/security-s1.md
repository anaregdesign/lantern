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
The actual validated `PolicyLimits` have a versioned canonical digest in the
reviewed cut. The full `S1ExecutionConfig` (PolicyLimits and all S1Capacity
fields) is committed to certified genesis/prefix and CommitRef. Constructors
and Apply check the actual compiled limits/configuration, rather than accepting
an unverified opaque config digest. Different capacity scopes cannot reuse one
certificate; different policy limits also require a different semantic review.
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
capabilities and data intervals must be subsets, no possible trust expansion and no new
session issuance. Edge permissions derive from Vertex subsets. Machines and
unresolved actors cannot mutate; qualified human Bearer remains supported.

Issuer disable/delete can use restrictive reserve after full subset/closure
checks, while still requiring purpose. PutIssuer remains conservatively
excluded as a possible trust expansion. An expanding member of a mixed batch
excludes the entire batch from reserve. Ordinary nonexpanding Role, assignment,
principal and session revocations acquire no new purpose requirement.

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
The final image-capacity check precedes reserve admission for terminal metadata:
an oversized nonexpanding candidate's refusal cannot consume reserved entries
or prevent a following legitimate restriction from recording its outcome.

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

## S2-A materialized recovery capsule

S2-A adds a package-private, unwired codec and detached replay validation. A
capsule is **data, not a certificate or durability barrier**. It does not prove
original authorization/consume, quorum choice, first ID ownership, freshness,
serving authority or successful native persistence. There are no production
constructors for historical authorization, prefix certificates or the new
opaque trusted genesis capability, and no permissive verifier callback or
serialized trust flag. Tests supply explicit independent trusted fixtures.

The version-one encoding starts with
`lantern/security/s2/materialized-capsule\0\x01`, followed by big-endian uint32
length-prefixed canonical typed JSON header and Image, a uint32 lineage count
with length-prefixed rows, and a uint32 ledger count with length-prefixed rows.
The header contains exact genesis identity, actual `S1ExecutionConfig` and its
digest, membership commitment, complete `SemanticCut`, installed control slot
and prefix, and fixed retirement floor with domain/cohort and a separate root.
Zero floor requires zero retirement root; a nonzero floor requires an
independently trusted matching root at restoration. S2-A never advances a floor
or prunes outcomes.

The complete typed Image preserves S1's exact committed array order, including
ordered Role rules and existing audit/session/machine data. Lineage map rows
are sorted by exact issuer then subject and retain identities without sessions.
FullID map rows use the total order version, domain bytes, cohort bytes,
namespace, nonce bytes. Each ledger row retains exact canonical original
OperationIdentity bytes, actor/review/digest, selected original H digest, first
CommitRef, original disposition, request-index-aligned item outcomes and
observed/resulting cuts. A rejected CAS may retain an original review with
different generation/fences/policy/sequence; it is preserved, never replaced
with the current review. Every retained CommitRef binds the actual execution
configuration. NOOP, same-ID replay and ID conflict retain control advancement
even with identical Image bytes.

Decoding returns only `s2DecodedCandidate` with detached immutable bytes. It
checks total input length before parsing, frame lengths/counts against remaining
input and explicit limits before keyed allocation, and nested JSON counts/depth
before typed decoding. Unknown/duplicate fields, aliases, noncanonical JSON,
short/long fixed arrays, duplicate/reordered keys, malformed original intent or
items, mixed configuration/scope and trailing input fail closed. It recompiles
the full policy/issuer/qualified-admin closure and recomputes projection and
configuration commitments. These checks establish structural consistency only.
There is no old `LNSEC03` decode, legacy Image admission fallback or migration.

Restoration requires a separately supplied `s2TrustedGenesis` and already
verified opaque `S1CertifiedNext` inputs. The exact genesis state and fixed floor
are detached, every input is replayed with `ApplyS1`'s contiguous scoped
predecessor checks, and the resulting **complete canonical aggregate** must
equal the candidate bytes. Only that replayed state escapes, with independently
owned snapshots/maps/item slices; failure returns no state. Bare Image, hash
correct fabricated capsule, slot zero, a later-slot QC or serialized root/floor
cannot create this trusted input. Arbitrary compact checkpoint installation
remains later work; S2-C below supplies a private historical verification path.

All codec limits are explicit. `s2CapsuleMeasure.Bytes` counts exactly this
framed aggregate, including retained operation JSON escaping, lengths, lineage,
outcomes, configuration and control/root fields. Checked uint64 accounting
precedes whole-buffer allocation. The configured capsule limit cannot exceed
the existing 8 MiB whole SystemMetadata compatibility ceiling. This ceiling is
not an approved production capacity. S1's independent 4 MiB Image and entry
limits can pass while the retained aggregate exceeds it; encoding then returns
an unpersistable error with measured bytes and leaves source/prefix/ledger/
reserves/floor unchanged. It never evicts first outcomes or substitutes a value.
Later voting admission must reserve the exact successor before ACK and prevent
unpersistable newly accepted values.

This byte count excludes pending original H, voter promises/accepted values,
evidence, WAL framing/tips and checkpoint overlap. WAL append, native owner,
voting/transport, runtime calls, checkpoint selection/install, compaction and
operational migration are outside S2-A. The later one-unsettled-slot, exact
certified predecessor, reservation-before-ACK and recovered-WAL self-sync
barrier contracts remain obligations. No unmerged #1667/#1674/#1678 helper is
required by this code.

Run from `server/`: `go test ./internal/security -run '^TestS[12]' -count=1`.
S2-A tests cover complete replay, original ownership, component substitution,
all actual policy/capacity fields, strict bounded parsing, proof separation,
detached storage, fixed trusted floor and a complete-ledger overflow with valid
S1 bounds. They retain S1 reserve/issuer regressions and confer no native or
deployment qualification.

## S2-B local materialization journal

S2-B adds a private, unwired append-only owner of already-certified S1
transitions. Its local receipt identifies the fixed journal scope, local index,
physical chain, complete capsule digest and control prefix. It records local
materialization, including terminal rejection, NOOP, replay and conflict. It
does not acknowledge original H, a voter acceptance, quorum choice, current
authorization or serving freshness. S2-B itself provides no production
trusted-genesis or historical-proof constructor; S2-C below supplies the private
historical/QC verification path. Neither component has a runtime, transport or
environment switch.

The existing FileWAL framing, canonical-path lease and bound tip are reused.
The application family is
`lantern/security/s2/local-materialization\0\x01`: a GENESIS or APPLY tag,
big-endian uint32 canonical-header length, typed canonical JSON header,
big-endian uint32 capsule length and raw complete S2-A capsule. The GENESIS
binds independently supplied store identity, journal epoch, S1 scope, actual
execution configuration, genesis/fixed retirement roots and explicit storage
policy. Each APPLY binds the previous and next whole-capsule digests and that
control entry's logical CommitRef. Local index and metadata revision equal
control slot plus one; semantic sequence is independent. The header is at most
4 KiB and the entire application payload must fit FileWAL's existing bound.

Prepare evaluates the exact verified next input and freezes its successor and
payload. Exactly one volatile plan holds the complete `payload bytes + 88`
WAL/tip append charge against the fixed file-content budget. Stale, discarded
or previous-owner plans cannot append. This is local quota accounting, not a
physical block reservation or durable credit before a vote. The journal is
finite. An unrepresentable or full-budget decision remains
`BlockedSameDecision`; the owner cannot manufacture a capacity rejection,
change H, evict an original result or advance the retirement floor.

Publication holds one owner gate across SystemMetadata, the matching complete
S1 state and Log completion. Reads return detached historical state through
that gate. Any uncertain I/O or interrupted publication closes reads and
writes until a new recovery owner succeeds. No raw cache, metadata handle or
Log escapes. Close stops admission and drains work before releasing the lease.

Resume needs independent exact trusted genesis, the complete verified
contiguous history through the selected local head, and an explicit trusted
minimum local receipt. Every GENESIS/APPLY capsule must equal deterministic
replay. Capsule bytes retain H's digest, not the original authorization and
purpose evidence: this journal cannot reconstruct its own missing proofs.
Deployments must retain that evidence independently. A genesis-only minimum
protects genesis continuity; it provides no whole-family rollback guarantee
after a later acknowledged cut. A known minimum must never be lowered to
reopen stale storage.

After all detached validation, the dedicated durable resume bridge syncs the
same reopened WAL descriptor that will append, verifies or catches up the tip,
unconditionally syncs the same tip descriptor, then syncs the parent directory
before binding the live Log. A readable equal-frontier tip still needs that
barrier. The WAL may lead its existing tip by only one complete frame; recovery
can spend the reserved final 44 bytes for that record, without duplicating an
already-complete tip. Missing files, partial frames/tips, incomplete creation,
scope/path changes, invalid proof history or failed barriers leave no owner.
No truncation, automatic genesis, migration, rotation or compaction is used.

Focused native tests cover the B1–B9 format, proof, reservation, recovery
barrier, ambiguous failure, quota, visibility, ownership and finite-exhaustion
contracts. Process restarts and injected sync order/failures do not qualify
hardware power loss. The guarantee assumes cooperative exclusive writers and
a storage stack honoring successful file and directory syncs. Pending-H and
Promise/Accept durability, before-vote reservations, network quorum, Profile B
serving and #1668 remain outside this component.

## S2-C durable participant and quorum kernel

S2-C composes the S2-B journal with a separate append-only protocol journal in
one private owner. The kernel has a fixed, independently supplied membership
of 3–31 distinct Ed25519 voting keys with an odd member count. Eligible members
may propose; no member is permanently designated as the security writer. A
serialized in-process scheduler delivers signed messages between independent
native owners for qualification. There is no socket, service, provider, serving
or environment activation path.

Independent bootstrap fixes the exact S1 genesis and execution configuration,
membership and proposer roles, origin keys/incarnations/admission profiles, and
common format bounds. The common protocol scope excludes each member's local
paths, store identity and journal quotas. The local composite scope separately
binds both P and B families, their identities/epochs, canonical paths and
explicit budgets. Stored headers, signatures and embedded keys cannot supply
their own trust root.

The historical verifier authenticates the complete original H: exact operation
bytes, actor and review, FullChangeID, origin serial, authentication and lineage,
credential/consume/profile claims, exclusive deadlines, and the complete
present-or-absent purpose claim. Commitments have a fixed nonrecursive order
and preserve the existing S1 H digest. H is independent of an ordering ballot
or slot; each protocol message additionally binds its scoped attempt. Historical
verification samples no current time and cannot mint a new operation, ID,
serial or consume. Only tests issue origin attestations in this stage. The
private persistence path accepts exact already-signed bytes; a production
current-admission producer remains a separate requirement.

P retains GENESIS, original H, issued ballots, promises with frozen accepted
snapshots, selected values and their phase-one proofs, complete accepts with
credits, complete chosen evidence, and drained B receipts. Durability precedes
each corresponding outward action. Phase one requires distinct configured
majority signers and carries the exact highest accepted value. Equal highest
ballots with different values are corruption. A persisted selection cannot
change on a same-ballot retry or restart. Missing selected H bytes cannot be
replaced with NOOP. A choice needs matching authenticated Accepted witnesses;
replaceable witness signatures never change the logical CommitRef.

Before accepting, the shared pure S1 evaluator previews the exact next result
and whole B capsule without constructing a choice certificate. A genuine
chosen application must reproduce those bytes. P reserves bounded CHOSEN and
DRAINED completion charges, while B reserves the exact APPLY charge. Existing
actual ACCEPT records remain charged; the active slot retains the conservative
maximum of previous and new future credits. Ordinary promises, ballots,
selections and pending H cannot spend those credits. CHOSEN and B success
consume their corresponding credits into actual usage; unused slack is
released only after DRAINED. This is finite logical quota accounting, not a
physical disk-block reservation or unlimited progress guarantee.

The only completion order is durable CHOSEN, durable complete B cut, then
durable DRAINED. Next-slot voting waits for that sequence. Recovery authenticates
the full retained P history, crosses the existing same-descriptor WAL/tip and
directory barriers, then supplies the exact matching verified prefix to B.
A complete B cut without DRAINED is verified and resynchronized without a
second APPLY or a second charge for already-consumed credit. B ahead of retained
authentic choice, missing H, torn families or unexplained suffixes remain closed.
Bounded chosen-history export and contiguous ingestion use retained signed
evidence, rather than another member's capsule as a proof oracle.

Independent minimum P and B cuts detect known rollback. P's floor also protects
promises and selections when B has not advanced. Neither local signatures nor
epochs detect a whole-family rollback when every independent witness is lost.
Stable identity and a storage stack honoring completed sync barriers remain
assumptions. Ambiguous I/O or publication, including errors that also contain a
definite-abort sentinel, closes the composite owner before subsequent reads,
replies or publication. Resume never adopts a B-only store or creates a missing
family under an old voter identity.

The C1–C10 tests cover cryptographic binding, majority/highest-accepted rules,
durability-before-send, lost-quorum-evidence recovery, preview/application
parity, cross-journal crash cuts, exact credits and exhaustion, fail-stop
ownership, independent rollback floors and three-node delivery schedules. The
approved exact preexpiry H may first become chosen and materialized after its
original deadline; expiry or an unrelated NOOP is not cancellation. Progress
still needs surviving H, a connected majority with capacity, eventual delivery
and storage completion, and a period with a stable eligible proposer. These
tests do not qualify physical power loss or a production origin's clock,
credential, CSRF or purpose checks. Current admission and disclosure, qualified
clock/freshness renewal, key lifecycle/fencing/rejoin, production transport and
runtime activation, retention/compaction, and #1668 remain later work.
