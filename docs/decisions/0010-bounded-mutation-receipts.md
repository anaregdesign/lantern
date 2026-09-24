# 0010: Bounded mutation receipts for ambiguous responses

- Status: Accepted as the #1115 design; internal Store, Edge Delete commit, and guarded receipt-tail wire prerequisites exist, but capability/status RPCs remain disabled and no receipt-enabled write RPC is enabled
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
Before #1282, remote `ApplyMutation` could advance an origin watermark before
graph apply or local relay publication; its contiguous-publication fix alone
still does not provide an atomic receipt seam. A condition-not-met Put has no
graph mutation to replicate, while `BackupSnapshot` and restore carry live
graph records only.
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
loss creates a new active epoch. An HA node starting without certified cluster
state must reject **new** receipt-capable mutations until a complete peer
Snapshot establishes the shared epoch and policy, or an explicit operator
bootstrap seed establishes a fresh shared epoch. Independently generated or
reused static seeds cannot certify continuity after total-cluster loss;
partitioned cold starters must not select competing active epochs
automatically. A single-node fresh start may mint a new epoch but cannot
inherit prior continuity. Known receipts
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
immutable root swap, or release of a comprehensive read cut after reversible
in-place staging). In the latter case, every observer must share that cut and
a definite WAL abort must roll back all staged graph, receipt, index, and
origin state before releasing it. Calling a fallible graph or index mutation
after the WAL commits is forbidden. The envelope is written to the configured
WAL **before** any state becomes visible. A staging failure or a WAL failure
**proven to be a definite abort** releases every reservation and returns an
error with no graph, result, receipt, origin seq, or subscriber-visible change.
A generic I/O error, timeout, or lost acknowledgement is indeterminate: the
WAL may already contain the envelope. Its seq must not be reused, and the
endpoint must fail closed for graph reads, receipt status, new writes,
Snapshot, and Subscribe until replay proves the committed frontier, or a
verified complete Snapshot replaces the ambiguous state and establishes its
certified epoch with a new local generation. It must never report an
indeterminate result as a definite abort. The
[`WAL.Write(Entry) error`](../../core/mutationlog/mutationlog.go) signature
alone provides neither an abort proof nor replay. After a successful WAL
commit, publication of the already-staged graph/search state, receipt set,
and log entry is one infallible cut under the same gate;
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

A receipt-only envelope still advances the origin seq. Identity-only Subscribe
must emit a final zero-key chunk with an explicit receipt-only operation so a
CDC consumer advances its cursor without invalidating graph data; maintained
SDK decoders must accept that bounded marker. The existing
[`projectMutationIdentities`](../../server/service/identity.go) has no such
operation yet. During peer Snapshot installation, new receipt-capable
admission and receipt status fail closed until graph, receipts, epoch, and
cutoffs are verified together. The existing
[`BeginSnapshotInstall`](../../server/service/publication.go) read fault is
not, by itself, an admission interlock.

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

### Internal implementation boundary

The unwired [Edge Delete coordinator](../../server/service/receipt_edge_delete.go)
now stages graph, per-item receipts, and one origin row before a WAL call. Its
private log envelope distinguishes the original request, request-indexed
results, causally accepted graph transitions, epoch/policy, and origin HLC/seq.
The private [Edge Delete WAL codec](../../server/service/receipt_edge_delete_codec.go)
can encode that envelope as a bounded, versioned, deterministic payload and
decode it with strict structural and cross-field checks. It reconstructs the
graph-only `Mutation` from indexed accepted keys. The enclosing FileWAL frame
owns the checksum and replica-local log seq; its HLC must match the envelope
HLC, while its local seq is independent of the origin-local seq. Tombstone
expiration is encoded as UTC Unix nanoseconds, without Go location or monotonic
clock metadata. The codec is not wired to a serving WAL or replay path and
does not certify receipt recovery, replication, or status continuity.
The private [FileWAL union codec](../../server/service/receipt_wal_union_codec.go)
adds a versioned kind discriminator for graph-only `Mutation`, a private
[graph Delete effect envelope](../../server/service/graph_delete_effect_wal.go),
or the receipt Edge Delete envelope. Its ordinary graph kind
encodes protobuf plus an ordered sidecar for nil repeated-message slots,
which protobuf otherwise turns into empty messages on decode. The decoder
checks the exact kind, version, lengths, sidecar indexes, supported oneof
arms, and nested unknown fields. A raw protobuf wire scan rejects duplicate
oneof arms at every depth and duplicate outer `Mutation.op` fields, so a
receipt arm cannot be hidden by a later graph arm. Other valid protobuf field
orders and duplicate scalar values retain protobuf semantics. The decoder
accepts valid protobuf encodings without requiring a byte-for-byte match with
this build's deterministic encoder:
protobuf does not promise stable deterministic bytes across library versions.
Union version 3 retains `Mutation.tombstone_expiration`: each graph-only exact
Vertex or Edge Delete must retain the origin's absolute D4 deadline. Origin
handlers use one sampled deadline for the graph effect and published mutation;
follower apply uses that value without renewing it. An older graph Delete WAL
record lacking the field, an invalid timestamp, or an older union fails
closed on replay. Prefix Deletes still publish exact victim batches and carry
that same sampled deadline. The deadline is sampled before the origin HLC and
checked against both origin HLC + D4 and receiver now + D4 + D3 maximum skew;
an arbitrary future deadline or forged future HLC fails closed. The origin
checks those bounds before graph mutation, including after a clock rollback.
A genesis recovery audit must not infer a missing deadline from its current
clock. The new graph Delete kind preserves the original `Mutation` for existing
Subscribe/relay projection and stores strictly increasing accepted request
indexes separately. `Existed=false` is insufficient: an accepted absent-key
tombstone and a Delete rejected by newer causal state both report false.
Prefix origins already publish only exact accepted victims, so the sidecar
indexes refer to that exact batch, never a predicate. The decoder rejects
old version 2 graph Deletes and version 3 ordinary-graph-kind Deletes without
this sidecar. An expired absolute deadline remains valid historical evidence
and is never replaced with `now + D4` during decode. The checked GraphCache
batch APIs can return response outcomes and accepted indexes from one lock,
but no serving producer selects the new kind: a future producer must retain
the same sidecar across an ambiguous WAL append and publication repair,
including for remote relay and singular Delete mutations. The detached
recovery candidate still rejects graph Delete envelopes and all graph writes
after receipt envelopes. A later Put/Add rejected while a tombstone was live
can become accepted on replay after it expires; Delete evidence alone cannot
certify a complete graph/receipt restore.
The encoder rejects typed-nil message-valued oneof payloads, whose wire bytes
are indistinguishable from present empty messages and would change meaning on
replay. The receipt kind retains the existing LRED validation and its 8 MiB
body cap under the FileWAL frame's
32 MiB bound. The `FileWAL` payload decoder cannot see frame metadata, so a
replay/restore visitor must additionally validate the frame HLC against the
decoded graph or receipt HLC before applying state. This remains unwired and
does not yet constitute a complete replay or durable serving configuration;
production activation also needs a WAL schema migration policy across future
protobuf changes. The private graph kind now pins the reachable `Mutation`
schema and rejects an unreviewed field change under union v3. A production
migration must still retain a decoder for prior WAL versions before any schema
change is allowed on a receipt-enabled node.
The private [read-only mixed-WAL audit](../../server/service/receipt_wal_recovery.go)
checks a complete, genesis-based FileWAL for frame/payload HLC agreement,
contiguous per-origin sequences, one configured epoch/policy, and a bounded
set of known unexpired receipt results. It validates those rows through the
Store snapshot rules and releases expired entry/byte capacity as each frame's
HLC advances, before admitting another row. It returns no Store or append
writer. A missing ID has
no status from this audit: aborted Store clock advances, graph/search state,
and an atomic origin/log cut remain outside the WAL decision inventory. A
non-genesis WAL requires a verified receipt-bearing Snapshot baseline before
it can be audited or resumed.
The HLC `RestoreFloor` API can seed a clock from the greatest verified
committed timestamp without applying the live-peer skew clamp, so a wall-clock
rollback cannot put the next local mutation below that frontier. No serving
restore currently calls it. The caller must validate the entire WAL/Snapshot
cut first; this clock floor does not recover graph state, Store clock
high-water, receipt epoch continuity, or an absent-ID status.
The guarded full Subscribe projection carries receipt-bearing entries as one
`ReplicatedReceiptEdgeDelete` mutation arm. A full-stream consumer without
`accept_receipt_envelopes` receives `INVALID_ARGUMENT` before that frame;
an old peer that receives the unknown oneof rejects the operation before
advancing its origin watermark. Identity-only Subscribe emits DeleteEdge
keys only for causally accepted items, and an all-rejected call emits a final
zero-key `RECEIPT_ONLY` marker to advance its cursor without invalidation.
The existing Pump does not opt in, and remote apply rejects the arm. This
remains an internal wire prerequisite, not a supported receipt CDC contract.
If the receipt-bearing entry has already left the log ring, this per-entry
opt-in guard is never reached: an old Pump can receive the ordinary gapped
error and fall back to a graph-only Snapshot. Production enablement therefore
requires authenticated PeerStatus capability/version negotiation and a
receipt-aware Snapshot install gate that refuses a graph-only downgrade,
including a real-wire test with an evicted receipt entry.
The staged `SnapshotFormat` request/header and `PeerStatus.required_snapshot_format`
fields establish this downgrade boundary without producing a receipt image.
`WithReceiptSnapshotRequired` is a lifetime service latch: when set, a legacy
full Subscribe is rejected before the ring is inspected, and every Snapshot
request fails closed until the receipt-bearing producer exists. Current Pump
and anti-entropy request graph-only format and reject a receipt format header
before applying a frame; a future receipt receiver must request and require
`RECEIPT_V1`. No production provider sets the latch or enables receipt writes.
The follow-up producer must tie the latch to receipt admission and then stage,
validate, and atomically install graph, receipts, epoch, policy, clock, and
cutoffs before allowing status or resumed Subscribe.
The private [whole-state archive codec](../../server/backup/whole_state_archive.go)
is a separate format from `.lbk`. It requires a `RECEIPT_V1` graph Snapshot
header, receipt Store snapshot and policy, clock high-water, and origin HLC
cutoffs; bounded records and a counted SHA-256 footer reject incomplete or
damaged containers. The digest detects corruption, not malicious tampering or
an inconsistent source cut. The codec checks graph frame wire fields, order,
counts, payload semantics, and causal relationships, but not whether graph,
receipts, and origin cutoffs were captured under one publication cut. Its
wire-field validation rejects unknown fields, ambiguous duplicates, and
malformed encodings without comparing bytes from a particular protobuf
runtime; field and map-entry order remain semantically irrelevant. The v1 codec
pins the reachable graph schema and rejects unreviewed proto changes. No production
producer, backup scheduler, or restore installer uses this codec yet. The
private [whole-state capture](../../server/service/receipt_snapshot_capture.go)
now copies graph Snapshot frames, Store receipts/policy, origin cutoffs, local
log seq, and an HLC frontier under one exclusive service publication cut. It
clones mutable Vertex protobuf values before releasing that cut and rejects a
publication fault or incomplete Store export. It does not write an archive,
enable the Snapshot RPC, or certify a durable recovery frontier: `Clock.Now()`
advances only in-memory HLC state, and aborted `Store.Begin` or a `Store.Lookup`
may advance high-water without a WAL entry. A future producer must use this
coherent source cut; the installer must validate and install all sections
together before serving. Total-cluster restore still rotates the active epoch
unless a complete durable WAL proves the exact current frontier.
The diagnostic `GetReplicationStatus` dashboard remains available during a
publication fault; it reports pump health, not a receipt or graph cut.

This coordinator blocks service-gated reads, its own receipt Lookup, PeerStatus,
Snapshot, BackupSnapshot capture, and Subscribe until log publication. Its
`CommitWithPostRingPublication` callback releases the Store, GraphCache, and
origin tracker locks only after the matching log ring entry and sequence are
installed, but before dispatcher handoff can block. Direct Core readers that
unblock at this point see committed state; log readers wait on `Log.mu` and
then see the same entry. `LanternService.LocalSeq` also shares a
receipt-specific origin cut through publication without blocking legacy relay
WAL retries. Tests cover the WAL-held tentative interval and the post-ring
callback interval. This establishes healthy in-process publication ordering,
not a durable or every-failure guarantee: raw GraphCache reads and Store stats
have no error result and cannot report an indeterminate WAL fault. Authoritative
receipt status and graph reads must use the server's error-bearing committed
view until a checked Core read API or equivalent fail-stop gate exists.

No production provider uses the staged cache or coordinator. A durable WAL
encoder/replayer, atomic remote receipt apply, a PeerStatus capability gate,
and receipt-bearing Snapshot/BackupSnapshot with epoch and clock-high-water
validation are still required. Current Snapshot and BackupSnapshot remain graph-only and cannot
certify receipt continuity after restart or restore. `Store.Begin` advances
clock high-water and expires already-dead receipts even if the new mutation
later aborts; only newly staged receipts roll back. Recovery must persist that
monotonic metadata with the committed cut or rotate the active epoch.

## Dependencies and rollout

The first additive wire step exposes a capability probe behind the normal
LanternService auth interceptor. It reports `enabled=false` without an epoch
or endpoint marker. The internal `core/mutationreceipt.Store` is not connected
to a serving commit path, so receipt status RPCs return `FAILED_PRECONDITION`;
they do not label an unknown operation `NOT_YET_OBSERVED` or
`NO_LONGER_PROVABLE`. This schema is not permission to send receipt-enabled
mutations. A later vertical slice must activate status only together with the
atomic commit, recovery, replication, and Snapshot guarantees above, and
require configured authentication before advertising an enabled capability.

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
