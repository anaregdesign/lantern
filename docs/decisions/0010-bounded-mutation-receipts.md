# 0010: Bounded mutation receipts for ambiguous responses

- Status: Accepted as the #1115 design; internal Store, Edge Delete commit, guarded receipt-tail wire, active-plus-retired durable local baseline recovery, guarded RECEIPT_V1 Snapshot production/install, and manifest-last active-epoch backup-set production are wired for private durable replication, but durable backup restore, retired backup-set transport, capability/status RPCs, and receipt-enabled client writes remain disabled
- Date: 2026-09-24
- Issues: #1115, #1282, #1203, #1116, #1393, #1394

## Context and boundary

The first `lantern_client_offline` release admits unconditional Put only.
Durable Add, conditional Put, and Delete remain unsupported by its outbox.
A stable Add contribution ID does not recover the original result after a
response is lost and a later Delete removes the contribution. A receipt must
record the server's **original per-item result**, including a no-op, before a
client can reconcile those operations. It cannot manufacture a global
exactly-once guarantee in Lantern's leaderless, asynchronous cluster.

Today's write paths are not an atomic receipt seam. Graph-only Add/Put/Delete
publication applies `GraphCache` first, captures an effect-complete private WAL
envelope, and fails the shared publication cut closed if append needs repair.
That makes strict graph recovery reproducible but does not atomically commit a
receipt Store or original receipt result. Before #1282, remote `ApplyMutation`
could advance an origin watermark before graph apply or local relay
publication; its contiguous-publication fix alone still does not provide an
atomic receipt seam. A condition-not-met Put has no graph mutation to
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

The private `core/mutationreceipt.RetiredCatalog` is the bounded read-only
representation for that retained evidence. It imports strictly sorted,
nonempty per-epoch Store snapshots together with each epoch's immutable
retention and capacity policy, recomputes the policy fingerprint, and validates
every original deadline before retaining the row. The catalog has separate
aggregate entry and logical-byte caps, rejects the current active epoch, and
advances only from a caller-supplied nondecreasing effective clock high-water.
It keeps no admission, logical-call, contribution, or eviction indexes: only an
exact known unexpired ID can return `CONFIRMED`; every absent or expired
retired ID is `NO_LONGER_PROVABLE`, and an active-epoch lookup fails distinctly
for routing back to the active Store. Its deterministic deep-copied snapshot
omits expired rows and empty epoch members and can be validated and imported
without an active Store.

This catalog is not wired into the archive codec, Snapshot transport, runtime,
service routing, scheduler, or restore path. It does not persist its own clock,
merge a newly retired active Store, choose a replacement epoch/generation, or
prove that its evidence and graph/WAL state share one cut. The #1394
integration layer must rebuild and publish the active Store plus catalog
atomically, durably preserve the effective high-water, enforce its configured
capacity, and carry the catalog in a versioned backup/peer format before any
retired-epoch status is exposed.

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

The private [Edge Delete coordinator](../../server/service/receipt_edge_delete.go)
stages graph, per-item receipts, and one origin row before a WAL call. Public
receipt-bearing mutation RPCs do not invoke it. Its
private log envelope distinguishes the original request, request-indexed
results, causally accepted graph transitions, epoch/policy, and origin HLC/seq.
The private [Edge Delete WAL codec](../../server/service/receipt_edge_delete_codec.go)
can encode that envelope as a bounded, versioned, deterministic payload and
decode it with strict structural and cross-field checks. It reconstructs the
graph-only `Mutation` from indexed accepted keys. The enclosing FileWAL frame
owns the checksum and replica-local log seq; its HLC must match the envelope
HLC, while its local seq is independent of the origin-local seq. Tombstone
expiration is encoded as UTC Unix nanoseconds, without Go location or monotonic
clock metadata. Durable receipt-WAL mode carries this codec through the union
WAL and replay path; the codec alone does not certify receipt recovery,
replication, or status continuity.
The private [FileWAL union codec](../../server/service/receipt_wal_union_codec.go)
adds a versioned kind discriminator for graph-only `Mutation`, private
[graph Delete effect](../../server/service/graph_delete_effect_wal.go) and
[graph Put effect](../../server/service/graph_put_effect_wal.go) and
[graph Add effect](../../server/service/graph_add_effect_wal.go) envelopes,
the receipt Edge Delete envelope, or the private receipt Vertex Put and exact
Vertex Delete envelopes. Each Vertex receipt envelope carries the complete
ordered original intent and immutable request-index-aligned result separately
from the receiver-local accepted graph projection. Its ordinary graph kind
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

#### Receiver-local graph publication evidence

Serving graph publication no longer appends raw `Mutation` rows. Every enabled
local and remote Add, Put, and exact Delete path first proves that its largest
private envelope is encodable, then applies the graph operation and captures
the receiver's exact accepted effects from that same `GraphCache` lock. The
local relay log stores the corresponding graph Add, Put, or Delete effect
envelope. If WAL append fails after graph apply, pending repair owns that exact
immutable envelope and retries only its append; it never re-evaluates or
reapplies the graph operation. The envelope's `GraphMutation()` remains the
unchanged graph-only Subscribe/relay projection, so ordering, origin cursors,
CDC payloads, and nil-slot wire indexes do not expose this private durability
representation. This boundary does not enable a receipt write, receipt status,
or receipt capability.

Union version 4 retains `Mutation.tombstone_expiration` when graph tombstone
retention is enabled: each graph-only exact Vertex or Edge Delete then carries
the origin's absolute D4 deadline. Origin handlers use one sampled deadline
for the graph effect and published mutation; follower apply uses that value
without renewing it. An invalid timestamp, a missing deadline on a
tombstone-retaining node, or an older union fails closed on replay. A
graph-only node with tombstone retention disabled instead records a physical
Delete envelope whose accepted indexes must be the complete ordered request;
strict replay can reproduce that exact effect without inventing a causal
floor. Prefix Deletes still publish exact victim batches and, when enabled,
carry the same sampled deadline. The deadline is sampled before the origin HLC
and checked against both origin HLC + D4 and receiver now + D4 + D3 maximum
skew; an arbitrary future deadline or forged future HLC fails closed. The
origin checks those bounds before graph mutation, including after a clock
rollback. A genesis recovery audit must not infer a missing deadline from its
current clock. The graph Delete kind preserves the original `Mutation` for existing
Subscribe/relay projection and stores strictly increasing accepted request
indexes separately. `Existed=false` is insufficient: an accepted absent-key
tombstone and a Delete rejected by newer causal state both report false.
Prefix origins already publish only exact accepted victims, so the sidecar
indexes refer to that exact batch, never a predicate. The version 4 decoder
rejects all version 2 and version 3 union payloads outright; there is no dual
reader or fallback path. An expired absolute deadline remains valid historical
evidence and is never replaced with `now + D4` during decode. The checked
GraphCache batch APIs return response outcomes and accepted indexes from one
lock. Every enabled local and remote graph Delete path now publishes this
private kind; singular facades, plural operations, and prefix operations all
retain exact request positions. A WAL failure keeps the same immutable envelope
for append-only repair without reapplying graph effects.
The detached
recovery candidate replays only accepted exact Vertex/Edge identities in
request order, preserving duplicates, accepted absent-key floors, and the
origin's absolute tombstone deadline. These effects were already committed
through the receiver's non-strict replication path, so recovery uses the same
convergence path even when the configured causal-metadata budget is full. It
still rejects an accepted transition that is no longer causally admissible;
local-origin checked admission remains bounded and unchanged. Zero-accepted
frames advance only the origin/log frontier. Raw graph writes after a receipt
remain gated. A later
Put/Add rejected while a tombstone was live can become accepted on naive replay
after it expires;
Delete evidence alone cannot certify a complete graph/receipt restore.
The graph Put kind records the original Mutation and a strictly ordered subset
of receiver-local accepted request indexes. Each accepted index distinguishes
a live value or Edge from an accepted-expired causal barrier; omitted indexes
include locally rejected Put slots. Ordinary plural Put nil slots retain their
original positions; replicated Put nil entries fail closed because serving
apply rejects them. A zero-accepted mutation is valid evidence. The outcome list
comes from the GraphCache application lock, not an origin projection or later
read. The inner Put body has its own version, length, and reserved-byte checks.
Raw older Put rows can be read before a receipt but remain unproven; the
read-only audit rejects one after a receipt instead of treating its original
mutation as evidence of a receiver-local effect. The detached recovery
candidate replays only the accepted subset, preserving live/barrier decisions
and allowing a live value to expire by recovery time. Because these are
already-committed receiver effects, replay bypasses local causal-metadata
admission while still requiring every recorded outcome to remain causally
accepted. A contradictory accepted decision fails the candidate. Every
enabled local and remote Put path emits this private kind. Serving use of this
graph-only evidence enables neither Store admission nor an absent-ID answer.
The stricter effect-complete staging path rejects even pre-receipt raw Put/Add
rows before replay. It is a prerequisite for future serving recovery, not a
serving certificate: Store clock high-water, epoch continuity, and atomic
publication remain unproven. Its caller must hold the FileWAL path lease
through both audit and replay passes and configure an empty staged graph with
the intended indexes and limits before replay; a bounded search-index rebuild
must succeed before a candidate is returned. An original Delete receipt result
cannot be recomputed from a later graph view because its former Edge may have
expired.
The graph Add kind records an ordered subset of receiver-local accepted wire
indexes, including nil-slot position preservation for synthesized ContribIDs.
Rejected, deduplicated, and causally fenced Adds are omitted. GraphCache's
private result path captures each accepted decision under its application lock
without allocating an outcome slice in the ordinary serving path. Older raw
Add rows after receipt evidence fail the read-only audit. The detached
candidate replays only accepted original wire indexes, using the original
explicit or synthesized ContribID for each row; omitted, nil, deduplicated,
and causally fenced rows stay absent even if their old floor has expired.
A recorded accepted Add that now conflicts with recovered causal state rejects
the whole candidate. Serving local and remote Add paths now emit this private
kind, but it authorizes neither durable offline Add nor an absent-ID status
answer.
The encoder rejects typed-nil message-valued oneof payloads, whose wire bytes
are indistinguishable from present empty messages and would change meaning on
replay. Each receipt kind retains strict envelope validation and an 8 MiB body
cap under the FileWAL frame's 32 MiB bound. The `FileWAL` payload decoder cannot
see frame metadata, so a
replay/restore visitor must additionally validate the frame HLC against the
decoded graph or receipt HLC before applying state. The union is bound only to
the opt-in durable runtime; public receipt capability remains disabled.
Before v1, a private WAL schema change replaces the union version: the graph
kind pins the reachable `Mutation` schema, rejects an unreviewed field change
under union v4, and old union versions fail closed rather than gaining aliases,
dual readers, or compatibility fallback. Any future compatibility policy is a
v1 activation decision, not a prerequisite for changing this private format.
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
The detached recovery candidate exposes no receipt-status helper: a prior
`Store.Lookup` or aborted `Store.Begin` can advance high-water beyond a
receipt deadline without a WAL row. After wall-clock rollback, replay can
retain that committed row again, but its bytes alone do not prove that
`CONFIRMED` is still valid. Serving status needs a durable clock bound or
must fail closed after epoch rollover.
`mutationlog.AcquireFileWALLease` provides an advisory process lock for the
WAL path across separate audit, replay, and append calls. A production owner
must hold it throughout those calls and shutdown, use its canonical path,
and never unlink the stable `.lease` sidecar. The private production
`ServingRuntime` now holds this lease together with the graph, Store, origin
tracker, appendable Log, clock/tip journals, and endpoint generation for the
entire process lifetime. The lease alone still does not certify application
recovery.
The HLC `RestoreFloor` API can seed a clock from the greatest verified
committed timestamp without applying the live-peer skew clamp, so a wall-clock
rollback cannot put the next local mutation below that frontier. Durable
restart validates and reconstructs the full genesis WAL cut first, then calls
`RestoreFloor` with the maximum of the replayed HLC frontier and persisted
Store clock high-water before exposing the runtime to Wire. The clock floor
itself does not recover graph state, Store clock high-water, receipt epoch
continuity, or an absent-ID status.
The guarded full Subscribe projection carries receipt-bearing entries as one
`ReplicatedReceiptEdgeDelete` mutation arm. A full-stream consumer without
`accept_receipt_envelopes` receives `INVALID_ARGUMENT` before that frame;
an old peer that receives the unknown oneof rejects the operation before
advancing its origin watermark. Identity-only Subscribe emits DeleteEdge
keys only for causally accepted items, and an all-rejected call emits a final
zero-key `RECEIPT_ONLY` marker to advance its cursor without invalidation.
Graph-only Pump does not opt in, and graph-only remote apply rejects the arm.
This remains an internal wire prerequisite, not a supported receipt CDC
contract. In durable receipt-WAL mode, Pump and anti-entropy now opt in only
after the runtime is certified; they require `RECEIPT_V1`, so an evicted
receipt entry cannot fall through to a graph-only Snapshot. The shared
installer refuses a zero or graph-only header before publication.
The `SnapshotFormat` request/header and
`PeerStatus.required_snapshot_format` fields establish this downgrade
boundary. `WithReceiptSnapshotRequired` is a lifetime service latch: when set,
a legacy full Subscribe is rejected before the ring is inspected, and
graph-only Snapshot requests fail closed. The opt-in `RECEIPT_V1` producer is
configured separately with the exact service-owned
`ReceiptWholeStateSource` and immutable Store policy. The source carries a
private owner identity; configuration rejects a source unless its primary
service, runtime, graph backend, mutation log, HLC clock, origin tracker, and
Store are the exact state owned by the replication responder. It calls that
source once, preflights the complete detached image before sending its header,
and streams the epoch, policy fingerprint/retention/capacity, clock high-water,
sorted unexpired receipt rows with original result and Add contribution
metadata, full origin HLC/sequence rows, local cutoff, graph frames, and a
counted footer. A receipt-only cut is valid. Missing, malformed, reordered,
count-mismatched, oversized, recursively unknown-field-bearing, or typed-nil
oneof metadata produces no header. The same preflight validates graph payload
identities, timestamps/HLCs, Add contribution IDs, duplicate/overlapping state,
causal floors, and edge endpoints before the first frame is sent. Every
nonzero graph HLC must be bounded by both the global cutoff and its matching
origin row; an unknown origin is invalid. The receipt clock high-water must not
exceed the cutoff's wall time at millisecond precision.
`WithReceiptSnapshotRequired` without that configured source still fails
closed.
The production provider selects Snapshot behavior from the runtime mode.
Graph-only mode retains the existing in-place `GRAPH_ONLY_V1` installer.
Durable receipt-WAL mode constructs one transport-neutral receipt installer
after runtime certification and passes that exact instance to both Pump and
anti-entropy. It requires `RECEIPT_V1`, drains the complete bounded stream
through `ReceiptSnapshotCollector`, revalidates the canonical archive, and
then calls the certified durable baseline install once. A cancellation,
receive error, malformed/truncated stream, count or capacity breach,
epoch/policy mismatch, or format downgrade returns before live publication.
This wiring does not enable receipt writes, public status, or capability.
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
pins the reachable graph schema and rejects unreviewed proto changes. The
private [whole-state capture](../../server/service/receipt_snapshot_capture.go)
copies graph Snapshot frames, Store receipts/policy, origin cutoffs, local log
seq, and an HLC frontier under one exclusive service publication cut.
`ReceiptWholeStateSource.Capture` returns that detached in-memory cut only.
`ReceiptWholeStateSource.CaptureForBackup` acquires the service's exclusive
committed view exactly once and, before releasing it, captures the same
graph/Store/origins/HLC/local-seq cut together with the owned runtime's live
FileWAL witness for that exact local sequence. It returns those values as one
detached result after validation. It rejects a graph-only runtime, a closed or
foreign runtime owner, a publication or receipt-commit fault, cancellation,
and unusable, legacy-uncertain, sequence-mismatched, or otherwise uncertified
Log/FileWAL state. Every failure returns neither a partial whole-state image
nor a witness.
Both capture paths clone mutable Vertex protobuf values before releasing the
cut. Before sampling the cutoff, they restore the service clock floor from the
captured Store high-water under that same cut; an incomplete Store export or
unrepresentable high-water fails closed. The opt-in replication Snapshot
producer calls `Capture` once. The private
[archive producer](../../server/backup/receipt_archive_producer.go) instead
calls `CaptureForBackup` exactly once and encodes only that detached combined
result. It decodes the complete archive before returning immutable archive and
WAL-cut bytes. Runtime certification binds the one source shared by replication
Snapshot and production backup to the exact primary, graph, Store, origins,
Log/FileWAL owner, HLC, NodeID, and endpoint generation. Neither producer alone
certifies a durable recovery frontier. The service binds the first private
coordinator's Store pointer, rejecting a different Store even with matching
policy; a misconfigured first binding therefore fails closed on later
construction.
Direct Core Store access remains outside the service publication gate.
The private [archive staging path](../../server/backup/receipt_archive_stage.go)
decodes the complete `RECEIPT_V1` container before reconstructing a fresh,
unpublished GraphCache and receipt Store. It prepares the identity prefix/head
index before replay, allows the caller to configure an optional search index,
rejects any graph configurator that leaves physical or hidden state, and
requires a successful bounded search rebuild before returning detached
graph, Store, policy, origin rows, local log position, and HLC cutoff. An
implicit endpoint may coexist with a retained Vertex Put barrier or Delete
tombstone: staging preserves that floor instead of interpreting the endpoint
as a new HLC Put. Staging rejects a tombstone whose absolute D4 deadline has
already elapsed and verifies every staged tombstone's HLC and deadline before
returning; it also rejects a live implicit endpoint whose HLC conflicts with
its retained floor. A failed decode, apply, or index rebuild discards the whole
candidate. The production durable installer consumes this candidate only
after complete validation; the existing graph-only Snapshot receiver's
in-place overlay remains separate and cannot certify receipt continuity.
The private bounded
[Snapshot collector](../../server/backup/receipt_snapshot_collector.go)
consumes a transport-neutral `RECEIPT_V1` stream into that detached archive
and stage. Every frame, section count, and total byte dimension has an explicit
positive limit; a task-owned spool is removed on every outcome, while a
successful candidate owns a read-only canonical archive until `Close`.
The candidate exposes cloned header/policy metadata, the canonical archive
digest/size, and a copying writer; its detached GraphCache and Store remain
private until the receipt installer asks the certified service to publish the
decoded canonical cut. A decoded Connect client stream does not
expose its original protobuf bytes, so the collector treats that incoming
encoding as non-authoritative: it recursively validates the parsed message,
including unknown fields and typed-nil oneofs, then deterministically
re-encodes it. Raw-observing stream adapters may additionally supply exact
frame bytes, in which case ambiguous duplicate fields and nonminimal wire
encodings are rejected before staging. Production durable mode shares one
collector-backed installer across Pump and anti-entropy; graph-only mode never
constructs it. The production collector caps each frame at 8 MiB, the complete
wire/canonical image at 512 MiB, total frames at 1,048,576, origin rows at
65,536, and receipt rows at the smaller of the configured Store entry cap and
1,048,574.
The private [FileWAL cut manifest](../../server/backup/receipt_archive_wal_cut.go)
binds complete archive bytes to both the original WAL frame bytes through the
archive's local sequence and the exact complete valid FileWAL tip observed at
manifest creation. The cut and observed-tip witnesses each carry sequence,
offset, raw-prefix digest, and rolling frame chain from one stable two-pass
inspection. Staging requires both recorded prefixes to remain exact, checks
the complete current FileWAL, rejects loss or replacement of a suffix that
already existed at bind time, and permits a valid suffix appended after the
recorded tip. This byte pairing does not prove that the archive source used
that FileWAL, that commits after the recorded tip were retained, or that the
runtime tip journal belongs to the archive. The caller must own a non-mutating
WAL path during each inspection. This stable-path, two-pass inspection helper
remains available for staging and its existing tests, separate from the
lease-owned live witness; it does not inspect a mutating FileWAL and is not part
of `CaptureForBackup`. The private active-epoch archive producer instead calls
`CaptureForBackup` exactly once and builds both manifest witnesses from its one
captured live tip, so archive cut and observed tip are identical without a
path reopen.
The production backup scheduler persists that immutable pair as a v1
instance-scoped set. Its canonical JSON commit manifest binds the exact member
basenames, roles, formats, versions, sizes and SHA-256 digests together with a
monotonic set ID, UTC timestamp, stable NodeID, and the active generation
captured under the same committed view. The manifest codec rejects unsafe
paths; missing, duplicate, reordered, or unknown members; malformed or
noncanonical JSON; trailing bytes; invalid identity/time/ID fields; digest or
size mismatches; and an archive/WAL-cut pair not taken from one live tip. The
fully validated loader returns owned archive bytes, decoded cut/tip witnesses,
and the same-cut identity without consulting the live appendable WAL, and is
shared by retention so selection rules cannot drift.

Each final member is created with `O_CREATE|O_EXCL`, completely written,
file-synced, and closed before the directory is synced. The final manifest is
then created with `O_CREATE|O_EXCL`, written, file-synced, and closed last,
followed by a final directory sync. A visible partial manifest is not a commit:
the strict canonical loader must validate it and every bound member before the
set exists. The protocol requires neither file locking nor hard-link support
and never replaces an existing name. Cancellation and every filesystem error
abort the attempt and clean only paths whose exclusive create proved ownership;
files from interrupted processes or racing writers are preserved and ignored
rather than guessed-owned. If the attempt created the manifest, cleanup may
remove members only after the manifest is absent and that absence has been
directory-synced; a failed marker removal or sync leaves every member in place.
Retention counts only complete valid sets for the configured instance and
prunes each manifest first, syncs the directory, removes its members, and syncs
again. Other instances, unrecognized files, and unproven orphan files are
untouched. `retain=0` keeps all. Periodic,
manual, and final-shutdown attempts serialize, while per-instance IDs remain
unique and increasing across repeated/backward clocks and process restarts.

The backup package exposes read-only strict newest-marker discovery as a
prerequisite for startup restore. It recognizes only canonical current-format
manifest names in one configured instance scope, selects the highest
recognized set ID before loading it, and returns an explicit not-found
sentinel when no marker exists. Once selected, any canonical-loader failure in
that marker or its members is terminal; discovery never scans backward to an
older valid set.

The later durable restore layer must consume this loader inside
`provider.NewServingRuntime`, while holding the FileWAL lease and before
`NewRuntimeCertified`; it must not reuse the graph-only
`Backupper.RestoreOnStartup` path. A normal complete restart remains stronger
than an older periodic set. A backup fallback must validate its recorded
cut/tip, journals, and generation chain against the lease-owned WAL, then
repair and persist its own current baseline proof before certification. A
lost-WAL restore rotates to an operator-supplied new active epoch and
normalizes known old receipts into the future retired catalog. None of that
WAL-path selection, suffix-proof, repair, catalog, installation, or startup
wiring is implemented by this production layer.

`Clock.Now()` advances only in-memory HLC state, and an aborted `Store.Begin`
or a direct `Store.Lookup` may advance high-water without a WAL entry. A serving
recovery still needs an atomic installer and proof that the WAL covers the
captured frontier or an epoch rollover. The installer
must validate and install all sections together before serving. Total-cluster
restore still rotates the active epoch unless a complete durable WAL proves
the exact current frontier. The production scheduler now consumes the private
producer's immutable pair, but no startup restore path consumes the committed
sets yet. Version 1 contains only the active-epoch archive and WAL-cut members:
it neither preserves retired-epoch receipts nor makes a rotated-epoch or
same-epoch archive-restore claim. A later set version can add a bounded
retired-epoch catalog as another member without resampling or reopening the
live WAL.
The internal Store can now take an optional synchronous
`ClockHighWaterSink`: it persists each higher observed millisecond before
Begin/Lookup changes in-memory state, and a sink error permanently faults
those decisions and Snapshot export. Snapshot import validates first, then
binds and advances the sink. This establishes the in-process persistence seam
only. A raw `ClockJournal` can now create or resume a synced, checksummed
sidecar bound to the canonical WAL path, epoch, and policy fingerprint, while
rejecting torn or incompatible metadata. Its caller must hold the same WAL
lease through journal Close. Neither path binding nor the journal alone
attests the WAL bytes, their archive/suffix cut, or a complete serving state;
the private production runtime owns it only together with the verified WAL,
tip, Store, graph, origin cut, HLC, lease, and endpoint generation.
A separate opt-in FileWAL tip journal durably records each frame's
local sequence and rolling hash after the WAL fsync but before the Log reports
success. On restart it verifies the complete attested prefix and rejects a
valid-looking WAL truncation or changed frame; a fully validated extra suffix
may be attested before serving, since the previous process may have crashed
between WAL fsync and tip publication. The caller must bind the journal to
the active epoch/policy and keep it under the same path lease. The owned
FileWAL also maintains the SHA-256 of its exact raw byte prefix across fresh
creation, each successful synced write, and resume. Once both the WAL and its
bound verified tip journal are synced at the same frontier, it can return an
immutable live witness containing local sequence, byte offset, raw-prefix
SHA-256, and rolling chain digest. A WAL or tip-journal failure leaves the
FileWAL unusable and cannot expose a success-shaped witness.
A private owned recovery candidate requires both clock and tip journals under
that lease, stages the effect-complete WAL graph/Store/origins, binds the Store
to the clock journal, and resumes a tip-certified appendable Log whose bounded
tail matches the detached replay. It closes Log, both journals, and lease on
discard. The candidate retains opaque provenance for the exact Log, FileWAL,
and canonical path. The runtime can sample it only through that candidate's
active lease and rejects a different runtime/service owner, Log or FileWAL,
path, sequence, closed or unusable Log, or legacy-WAL uncertainty. A matching
tip or standalone live witness still does not certify an archive cut;
`CaptureForBackup` is the service-owned composition seam that binds the
witness to one committed in-memory cut. The private `server/backup` archive
producer now consumes that combined result exactly once, validates and
canonicalizes only its detached whole-state image, and builds the paired WAL-cut
manifest directly from the captured witness without reopening the live WAL path.
That pair represents only the active epoch and is persisted by the production
scheduler as a versioned manifest-last backup set; it does not retain
retired-epoch receipts. The producer therefore rejects a capture containing
retired evidence before encoding a member or creating a backup-set file.
Startup selection/install of those scheduler sets remains unwired; the
runtime-local bounded retired catalog and same-epoch baseline continuity are
implemented separately below.
The private production runtime adds a
fixed-size checksummed `.generation` sidecar bound to the canonical WAL path,
epoch, policy fingerprint, and stable replication NodeID. Fresh mode creates
one opaque nonzero generation with exclusive file creation; restart requires
that exact sidecar and rejects missing, corrupt, zero, or mismatched metadata,
including a changed NodeID.
A companion private fresh candidate checks an empty staged GraphCache and
search/index policy before creating any files, then creates WAL, tip, and
clock journal under one lease and binds the empty Store and appendable Log.
Existing files and partially created sidecars are never overwritten or
silently retried as a fresh epoch.

A durable runtime owns an identity-stable retired-catalog slot beside the
identity-stable active Store. Fresh and marker-free restart initialize a
validated empty catalog from the active epoch, active Store high-water, and
separate aggregate entry/byte caps derived from the configured receipt policy.
The service-owned whole-state source snapshots active receipts first and then
the retired catalog at that exact active high-water inside the same exclusive
graph/origin/HLC/WAL cut. Runtime certification and source ownership bind the
exact slot identity, not merely equivalent contents.

A private fixed-size version-2 WAL baseline marker binds a canonical
`LANTBLN2` sidecar digest and byte count to its source cutoff/HLC, epoch,
policy fingerprint, previous generation, rotated generation, exact active and
retired receipt clock high-water, and actual staged local HLC restore floor.
`LANTBLN2` embeds the existing canonical active `LANTARCH` bytes unchanged and
a bounded canonical `LANTRET1` retired section. The retired section binds the
active epoch and aggregate caps; decoding requires those fields and its
high-water to match the active section exactly. This private persistence path
recognizes only marker version 2 and `.receipt-v2` sidecar names.

Before the marker commit, installation fully validates the incoming active and
retired images, snapshots local retired evidence under a short exclusive cut,
and forms the deterministic exact union with
`NewRetiredCatalogFromUnion`. Conflicts, active-epoch rows, aggregate-cap
overflow, or active high-water rollback fail closed. Encoding and sidecar
fsync happen outside the publication cut; a bounded optimistic retry requires
the active Store, retired revision, and origins to still match before staging.
The immutable combined candidate is fsynced under a content-addressed
`.receipt-v2` sidecar name before the marker commit. Graph, active Store,
retired slot, origins, clock, and generation publish only in the WAL
post-publication callback. Cancellation is checked again after reversible
staging and immediately before the marker write.

An indeterminate marker outcome or interrupted publication closes the
graph/CDC publication generation and fail-stops external reads as well as
writes. Startup validates the complete WAL and generation chain before
selecting the newest marker. Successive markers must have nondecreasing
receipt high-waters and responder-local HLC restore floors. The selected
marker receipt high-water must equal both sidecar receipt sections exactly;
its restore floor remains the persisted actual local HLC staged at install and
may be above the minimum implied by the source cutoff and receipt clock.
Recovery requires the newest marker's exact sidecar and never falls back to an
older committed baseline. It advances/prunes active and retired state at one
effective restart high-water, restores the existing GraphCache, Store,
retired slot, origin tracker, HLC, and Log identities, and replays only the
suffix. Suffix replay never mutates retired evidence. Natural D4 tombstone and
receipt expiry is reaped during restore rather than treated as archive
corruption. Orphan v2 sidecars without a marker are cleanup candidates;
missing, mismatched, noncanonical, oversized, or corrupt committed state fails
startup.
Live baseline installs are serialized before candidate encoding and sidecar
creation. After a committed marker, the runtime records its digest and removes
every other recognized candidate. A definite pre-marker rejection or
`DefiniteWALAbort` removes the new candidate while preserving the prior
committed digest. An indeterminate WAL result or publication panic retains the
candidate because its marker may be durable and fail-stops the service; later
installs cannot reap it. Cleanup failure after commit is logged as an
operational error while the install still reports the already-committed
outcome.

The sole production composition boundary selects
`LANTERN_RECEIPT_WAL_MODE=graph-only|fresh|restart`. `graph-only` is the
default and preserves the historical in-memory graph, NopWAL-backed Log, HLC,
and legacy graph-only backup restore. `fresh` and `restart` require an absolute
`LANTERN_RECEIPT_WAL_PATH`, a nonzero `LANTERN_RECEIPT_EPOCH`, and explicit
immutable retention, entry-cap, and logical-byte-cap policy. They also require
an explicitly configured, nonzero `LANTERN_NODE_ID`; graph-only mode retains
the historical random-per-boot fallback, but durable mode must not resume a
local Log at sequence N+1 under a new origin. They certify one owned
graph/Store/origin/Log/HLC/epoch/generation bundle before constructing either
service, the primary listener, metrics server, or replication pump.
Wire cleanup releases later owners before this bundle, and `App` retains the
bundle until all serving goroutines stop. Durable mode selects receipt-set
production from the exact certified runtime while still rejecting legacy
graph-only restore-on-startup because it cannot prove receipt/archive
continuity. Graph-only mode preserves the historical `.lbk` producer, restore,
filenames, retention, metrics, and behavior. #1394 owns the later durable-set
startup selection and installation boundary.

This runtime mode is private infrastructure only. It does not enable
`GetReceiptCapability`, receipt status, receipt-bearing client mutations, peer
capability negotiation, or receipt Snapshot/archive restore.
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

The private production provider installs the staged graph, Store, origin
tracker, retired-catalog slot, Log, restored HLC, epoch, and generation as one
certified serving bundle and binds the receipt Snapshot producer and one
shared installer to those exact identities. `RECEIPT_V1` remains active-only:
its producer rejects nonempty retired evidence before sending a header, and
its installer rejects nonempty local retired evidence before publication.
Durable Pump and anti-entropy can therefore use it only while the retired
catalog is empty. Graph-only mode and `BackupSnapshot` restore remain
graph-only and cannot certify receipt continuity. Public enablement still
requires the later capability/status and receipt-bearing client mutation
slice. `Store.Begin` advances clock high-water
and expires already-dead receipts even if the new mutation later aborts; only
newly staged receipts roll back. Recovery persists that monotonic metadata
through the bound clock journal or the committed baseline marker; losing or
mismatching either required artifact fails startup rather than silently
rotating the epoch.

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
slices require new proto and SDK surfaces, real Connect/h2c failure tests, and
bounded capacity and performance gates. Private two-node real-wire Pump and
anti-entropy gap recovery now cover atomic graph/receipt/origin installation
and same-responder tail resumption. Exhaustive multi-replica, partition,
restart, soak, and backup acceptance remains a separate #1393 follow-up;
#1394 continues to own the receipt-bearing backup boundary. None of this
blocks #1162's first Put-only offline core release. Until those vertical slices
pass, the offline package continues to reject durable Add, conditional Put,
and Delete.
