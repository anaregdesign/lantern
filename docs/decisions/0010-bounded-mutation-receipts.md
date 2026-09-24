# 0010: Bounded mutation receipts for ambiguous responses

- Status: Accepted as the #1115 Phase 0 design; no receipt RPC or storage is implemented
- Date: 2026-09-24
- Issues: #1115, #1282, #1203, #1116

## Context and boundary

The first `lantern_client_offline` release admits unconditional Put only.
Durable Add, conditional Put, and Delete remain unsupported by its outbox.
A stable Add contribution ID does not recover the original result after a
response is lost and a later Delete removes the contribution. A receipt must
record the server's **original per-item result**, including a no-op, before a
client can reconcile those operations. It cannot manufacture a global
exactly-once guarantee in Lantern's leaderless, asynchronous cluster.

Today's write paths are not an atomic receipt seam. Put/Delete change
`GraphCache` before `logMutationAt`, whose append failure is only logged.
Add appends before graph application to obtain its contribution sequence.
Remote `ApplyMutation` can advance an origin watermark before graph apply or
local relay publication. A condition-not-met Put has no graph mutation to
replicate, while `BackupSnapshot` and restore carry live graph records only.
Simply adding a receipt map to any one of these paths would permit graph,
result, receipt, and log to disagree. The future implementation must replace
that ordering; this ADR changes no current RPC or write behavior.

## Decision

### Identity, intent, and scope

A receipt-capable mutation carries one **per-item** operation ID. Its v1 raw
byte form is a version byte, a 16-byte deployment epoch, an unsigned 64-bit
UTC millisecond issuance time in network byte order, and 24 CSPRNG bytes. A
zero/randomness-free, unknown-version, future-dated by more than five minutes,
or expired ID is rejected before mutation. The client mints and durably stores
an ID immediately before its first send and reuses those exact bytes on every
attempt. The operation's local enqueue ID is distinct. A plural call also
carries one stable 16-byte CSPRNG logical-call ID plus item index and total
count; every request-index-aligned receipt stores that grouping and the
original public result. A singular call is a one-item group. Reusing an item
ID in a different group is an intent conflict.

Receipt-enabled Add requires the existing, nonzero, 24-byte client-supplied
`ContribID` for every item; the client durably stores and reuses it with the
operation ID. The two IDs serve different purposes: `ContribID` identifies the
graph contribution, whereas the operation ID retains its original result even
after Delete removes that contribution. A bounded reverse index binds each
live `ContribID` to exactly one operation ID and intent, and its bytes count
toward receipt capacity. A missing `ContribID`, reuse under another operation
ID or intent while that binding is live, or a mixed keyed/unkeyed receipt
batch is rejected before commit. After its deadline, that binding expires;
clients must generate fresh cryptographic IDs for later operations. The
current unkeyed Add path remains outside receipt guarantees.

The deployment epoch is a random cluster identity, not a user identity or a
hash of a bearer token. All HA members must have the same epoch and receipt
policy fingerprint before serving receipt-capable traffic. A fresh offline
client that has never learned the epoch cannot safely enqueue a
receipt-required mutation. An authenticated capability probe must expose the
current epoch, retention policy, and endpoint continuity marker before the
first send. Each receipt-capable mutation echoes that marker; the server may
return an already committed matching receipt on any replica, but rejects
a different node or generation before **new execution**. Any recovery that
cannot prove all in-horizon local receipts changes the generation. Token
rotation does not change the epoch. A node that rejoins from a complete peer
Snapshot adopts its peer's epoch; incomplete local recovery or total-cluster
loss creates a new active epoch. Known receipts
restored from an older backup may remain queryable, but an absent old-epoch ID
must never execute in the new epoch. A future authenticated-principal/ACL
design is required before tenant-scoped receipts are claimed.

The server computes `SHA-256("lantern-receipt-intent-v1\0" || canonical_intent)`
after validation. The canonical intent is a length-delimited sequence of the
operation kind, exact key/edge identity, value oneof and exact numeric bits,
absolute expiration, condition flag, Add delta and client-supplied
`ContribID`, and the other semantic fields of that operation. It uses
documented network byte order and validated values, not marshaled protobuf
bytes: field order, unknown fields, and alternative encodings of defaults
cannot change the digest. A duplicate with the same ID, group, and digest
returns the recorded result without executing again. A different digest or
group returns `InvalidArgument` without mutation. Prefix Delete is excluded
from v1.

### One origin commit boundary

A receipt-enabled logical call is one ordered **commit envelope** containing
its per-item IDs, digests, original results (including no-op results), graph
transitions, HLC, and a single origin log position. The server first validates
all items and reserves graph, receipt count/byte, and log capacity under one
origin commit gate. It resolves condition outcomes and builds the graph/search
index changes in a staged, non-visible version. A full receipt for each
accepted no-op (for example `CONDITION_NOT_MET`) is still committed in a
receipt-only envelope and consumes an origin log position. Invalid input and
admission failures commit neither graph nor receipt; a duplicate receipt
lookup precedes new-capacity admission.

The staged version must have an infallible publication step (for example, an
immutable root swap); calling a fallible graph or index mutation after the
WAL commits is forbidden. The envelope is written to the configured WAL
**before** any state becomes visible. A WAL write/commit failure or staging
failure releases every reservation and returns an error with no graph,
result, receipt, origin seq, or subscriber-visible change. After a successful
WAL commit, installation of the already-validated graph/search state,
receipt set, and log entry is one infallible publication under the same gate;
reads, status, Snapshot, and Subscribe see all of it or none of it. The public
response is sent only after
publication. A crash between a durable WAL commit and in-memory publication
replays the envelope before serving. With the current no-op WAL, this is an
in-memory single-node commit, **not** crash durability; loss of the only copy
changes the epoch and makes an uncertain old result no longer provable. The
existing `Log.Append` and `logMutationAt` APIs cannot by themselves provide
this boundary and must not be reused as an after-the-fact receipt write.

A plural envelope preserves request order and exact per-item outcomes. It may
contain graph transitions and no-op receipts together; it cannot partially
publish. The original Add effective weight, conditional Put outcome, or Delete
result is retained even if the graph later changes. A response serialization
or transport failure after commit does not roll the operation back.

### Replication, Snapshot, and backup

The receipt is part of the **same** origin mutation-log envelope as the graph
transition, including a receipt-only entry. A follower validates the envelope
and commits its graph effect, receipt, and local relay publication together
under the Snapshot cut gate. It advances its advertised per-origin sequence
only through the contiguous graph-applied, receipt-installed, relay-published
prefix defined by #1282. An out-of-order future entry waits in that Issue's
bounded pending queue; an append failure leaves the frontier retryable. A
Snapshot cutoff must never include graph state without the matching receipt,
or a receipt without its graph state. #1116's later CDC projection cannot
weaken this internal envelope/cutoff contract.

Once an envelope's receipt deadline has passed, a lagging replica still
applies and publishes its graph transition in contiguous order; it need not
retain the expired result. A receipt-only expired envelope still advances the
cutoff. The operation ID's issuance time prevents a later new execution, and
status is `NO_LONGER_PROVABLE`. A live receipt may never be discarded to
resolve capacity pressure or a gap.

Replication Snapshot and the versioned whole-state backup must include the
active epoch, receipt-policy fingerprint, unexpired receipts (including no-op
results), expiration and clock high-water metadata, and the matching graph
and contiguous cutoffs. Restore validates and installs one complete cut before
serving. Existing graph-only `BackupSnapshot` files cannot certify receipt
continuity; a restore from them starts a new epoch and treats old absent IDs
as no-longer-provable. Even a receipt-bearing backup may be stale relative to
unbacked post-cut commits, so total-cluster restore rotates the **active**
epoch unless a complete durable WAL proves the exact current frontier. A
backup can prove only the receipts it actually contains. Retained known
old-epoch receipts may still answer status, but missing old-epoch receipts do
not authorize replay.

### Bounded retention and admission

Receipt support is disabled until an operator configures a positive entry cap,
byte cap, and one immutable-per-epoch retention horizon `H` between one hour
and 30 days. Every replica uses the same policy fingerprint. A receipt's
admission/lookup deadline is the issuance time plus `H`; the ID timestamp
allows an expired retry to be rejected **even after** its receipt bytes have
been evicted. A client may send a never-before-seen ID only within five
minutes of issuance. An absent older ID is not a fresh executable request.
A persisted/replicated clock high-water prevents a backward wall-clock jump
from reopening either window. Changing `H` or losing that high-water requires
an epoch rollover; increasing `H` must never make an already purged ID
executable again. The encoded deadline and epoch are the compact replay
tombstone: no unbounded per-ID tombstone table is needed after expiry, and an
unseen expired or retired-epoch ID is never admitted as a new mutation.

Only expired receipts may be evicted. If a fresh local operation cannot reserve
all required receipt slots/bytes, it returns `ResourceExhausted` before any
graph or log commit. A remote committed envelope cannot silently evict a live
receipt to fit: if it exceeds local capacity, the replica stops at that
contiguous frontier, fails readiness/catch-up, and requires capacity relief or
expiry before replay. This bounds memory without pretending independent
leaderless origins can coordinate a cluster-wide capacity reservation.
Snapshot/bootstrap likewise fails closed when the complete receipt set cannot
fit. Metrics must expose entries, bytes, oldest deadline, local admission
rejects, replication-capacity stalls, and no-longer-provable status counts.

If a plural retry has even one expired or unverifiable item ID, it cannot
partially re-execute the remaining items; the whole retry returns an unknown
outcome. The client can reconcile already-known individual receipts by status
without changing that rule.

### Status and retry semantics

| Status | Meaning | Client action |
| --- | --- | --- |
| `CONFIRMED` | This replica has the exact committed receipt and original result. | Reconcile the stored result; never infer the current graph still has that value. |
| `NOT_YET_OBSERVED` | No local receipt is visible for an in-horizon ID. Async lag or an in-flight origin commit may still exist. | Poll/reconcile; never treat absence as proof of non-execution. |
| `NO_LONGER_PROVABLE` | The ID expired, its epoch/cut is unavailable, or incomplete recovery lost proof. | Stop automatic mutation replay and surface an unknown outcome; never map it to false, zero, or success. |

A read-only status RPC never executes a mutation. A committed receipt on a
converged second replica is authoritative; absence on any second replica is
not. After response loss, the client may resend the same ID **only to its
original endpoint**, within the fresh-admission window, and only if a
pre-send endpoint continuity marker (node identity plus receipt generation)
proves the endpoint still has complete receipt state. A different instance,
changed generation, or uncertain continuity permits status polling only. The
same rule applies when a load balancer cannot pin a request to that instance:
the SDK must not perform a blind mutation retry. The origin serializes
duplicate lookup and new admission under its commit gate, so an original
in-flight call and same-endpoint retry cannot both execute.

Two independent replicas can accept the same ID concurrently during a
partition if a client violates endpoint stickiness. Without consensus this
cannot be prevented globally; later receipt convergence may detect and alert
on the conflict, but cannot undo both commits. This ADR therefore promises
single-node original-result recovery and eventually replicated status, not
arbitrary-failover exactly-once execution. Receipt expiry, backup loss,
partitioned status, and total-cluster loss remain explicit unknown outcomes.

## Dependencies and rollout

#1282 must establish contiguous relay publication and Snapshot cutoffs before
receipt envelopes can claim replica-safe status. #1203 must establish mixed
Add/Put/Delete convergence before Slice A can re-enable durable Add; receipts
alone do not fix the graph history. #1282's graph-before-relay retry rule is
not itself sufficient for receipts: the receipt implementation must strengthen
that seam to an atomic graph/receipt/relay publication. Slice B for
conditional Put and Delete uses the same envelope architecture. Both
slices require new proto and SDK surfaces, real Connect/h2c failure tests,
three-replica/restart/backup cases, and bounded capacity and performance
gates. None of this blocks #1162's first Put-only offline core release. Until
those vertical slices pass, the offline package continues to reject durable
Add, conditional Put, and Delete.
