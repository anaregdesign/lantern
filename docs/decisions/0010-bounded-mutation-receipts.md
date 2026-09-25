# 0010: Bounded mutation receipts for ambiguous responses

- Status: Accepted as the #1115 design; internal Store, receipt-bearing Edge Delete and Vertex Put/Delete commit, guarded receipt-tail wire, active-plus-retired durable local baseline recovery, guarded RECEIPT Snapshot production/install, manifest-last retired-aware receipt backup-set production, and pre-certification durable startup restore are wired for private durable replication, but capability/status RPCs and receipt-enabled client writes remain disabled
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

This catalog is wired into the runtime-owned slot, one-cut capture, canonical
combined baseline, receipt backup set, RECEIPT Snapshot transport, and durable
startup restore. The startup layer can convert one fully validated archived
active Snapshot into a retired member only through the constructor in
`core/mutationreceipt`, which owns the private Snapshot versions, validates the
source Store, and preserves its exact same-cut clock high-water and original
policy. The deterministic destination union charges distinct raw rows against
configured aggregate bounds before pruning expired evidence. The catalog is
not yet wired into service lookup routing, and it does not choose a replacement
epoch/generation by itself. That routing remains required before retired-epoch
status is exposed.

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
from the receiver-local accepted graph projection. A relaying Vertex Put
reconstructs the origin-authoritative effect from that original result rather
than forwarding the relay's local projection. Live accepted values must match
the original canonical intent bit-for-bit (including floating-point NaN
payloads); only a live result with a finite absolute expiration may later be
recorded as a barrier. A permanent live value cannot become a barrier. Its
ordinary graph kind
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
cap under the FileWAL frame's 32 MiB bound. Vertex receipt decoders scan the
raw protobuf framing and count item fields before unmarshalling; their
10,000-item hard cap is the lower of the minimum-canonical-item byte ceiling
and the default plural-RPC batch limit. The `FileWAL` payload decoder cannot
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
a receiver that did not opt in rejects the unknown oneof before
advancing its origin watermark. Identity-only Subscribe emits DeleteEdge
keys only for causally accepted items, and an all-rejected call emits a final
zero-key `RECEIPT_ONLY` marker to advance its cursor without invalidation.
Graph-only Pump does not opt in, and graph-only remote apply rejects the arm.
This remains an internal wire prerequisite, not a supported receipt CDC
contract. In durable receipt-WAL mode, Pump and anti-entropy now opt in only
after the runtime is certified; they require `RECEIPT`, so an evicted
receipt entry cannot fall through to a graph-only Snapshot. The shared
installer refuses a zero or graph-only header before publication.
The `SnapshotFormat` request/header and
`PeerStatus.required_snapshot_format` fields establish this downgrade
boundary. `WithReceiptSnapshotRequired` is a lifetime service latch: when set,
a receipt-less full Subscribe is rejected before the ring is inspected, and
graph-only Snapshot requests fail closed. The opt-in `RECEIPT` producer is
configured separately with the exact service-owned
`ReceiptWholeStateSource`, immutable active Store policy, and runtime-owned
retired catalog. The source carries a private owner identity; configuration
rejects a source unless its primary service, runtime, graph backend, mutation
log, HLC clock, origin tracker, active Store, and retired catalog are the exact
state owned by the replication responder. It calls that source once. The
active Store is the sole clock authority; the retired catalog must snapshot at
exactly that high-water under the same publication cut.
The producer materializes and preflights the complete detached sequence before
sending its header. Metadata carries the active policy, sorted retired
policies, shared clock high-water, and full origin HLC/sequence rows. One
receipt stream carries active and retired rows in strict global raw
OperationID order, followed by canonical graph frames and a footer that
independently counts active receipts, retired epochs, retired receipts, and
origins. A receipt-only cut and an empty retired catalog are valid. Missing,
malformed, reordered, count-mismatched, oversized, recursively
unknown-field-bearing, or typed-nil oneof metadata produces no header. The
same preflight validates policy fingerprints, epoch ownership, graph payload
identities, timestamps/HLCs, Add contribution IDs, duplicate/overlapping
state, causal floors, and edge endpoints. Every nonzero graph HLC must be
bounded by both the global cutoff and its matching origin row; an unknown
origin is invalid. The shared receipt clock high-water must not exceed the
cutoff's wall time at millisecond precision.
`WithReceiptSnapshotRequired` without that configured source still fails
closed.
The production provider selects Snapshot behavior from the runtime mode.
Graph-only mode retains the existing in-place `GRAPH_ONLY_V1` installer.
Durable receipt-WAL mode constructs one transport-neutral receipt installer
after runtime certification and passes that exact instance to both Pump and
anti-entropy. It requires `RECEIPT`, drains the complete bounded stream
through `ReceiptSnapshotCollector`, retains and revalidates the canonical receipt
frame spool, and stages the graph, active Store, and retired catalog without
touching serving state. Installation unions incoming retired evidence with
the runtime catalog: exact duplicates are idempotent and any policy, row,
group-position, contribution, or high-water conflict fails closed. It then
calls the runtime's combined atomic baseline install once. A cancellation,
receive error, malformed/truncated stream, independent count or capacity
breach, epoch/policy mismatch, candidate tampering, or format downgrade
returns before live publication. This wiring does not enable receipt writes,
public status, or capability.
The private [whole-state archive codec](../../server/backup/whole_state_archive.go)
is the active-epoch-only LANTARCH codec (internal format version 1), separate
from both `.lbk` and the RECEIPT transport. Its graph section carries the
current receipt-format tag but no transport receipt metadata; separate archive
records carry the active Store snapshot/policy, clock high-water, and origin
HLC cutoffs. It does not carry retired evidence; the backup-set member
replacement is separate work.
Bounded records and a counted SHA-256 footer reject incomplete or damaged
containers. The digest detects corruption, not malicious tampering or an
inconsistent source cut. The codec checks graph frame wire fields, order,
counts, payload semantics, and causal relationships, but not whether graph,
receipts, and origin cutoffs were captured under one publication cut. Its
wire-field validation rejects unknown fields, ambiguous duplicates, and
malformed encodings without comparing bytes from a particular protobuf
runtime; field and map-entry order remain semantically irrelevant. The v1
codec pins the reachable graph schema and rejects unreviewed proto changes. The
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
result. It decodes the complete active archive and canonical `LANTRET1` retired
catalog before returning those immutable bytes together with the exact WAL-cut
witness. Runtime certification binds the one source shared by replication
Snapshot and production backup to the exact primary, graph, active Store,
retired slot, origins, Log/FileWAL owner, HLC, NodeID, and endpoint generation.
Neither producer alone certifies a durable recovery frontier. The service
binds the first private coordinator's Store pointer, rejecting a different
Store even with matching policy; a misconfigured first binding therefore
fails closed on later construction.
Direct Core Store access remains outside the service publication gate.
The private [archive staging path](../../server/backup/receipt_archive_stage.go)
decodes the complete LANTARCH container before reconstructing a fresh,
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
consumes a transport-neutral `RECEIPT` stream into a canonical
length-prefixed frame spool and detached graph, active Store, and retired
catalog stage. Active receipt rows, retired epochs, retired receipt rows,
origins, graph frames, total frames, per-frame bytes, decompressed transport
payload bytes, and canonical spool bytes each have an explicit positive
limit. The total frame limit must equal header + footer + the three independent
active-row, retired-row, and graph-frame maxima, so one section cannot consume
another section's budget. A task-owned spool is removed on every outcome,
while a successful candidate owns a read-only digest-bound spool until
`Close`. The candidate exposes cloned header/policy metadata, the canonical
spool digest/size, and a copying writer; its detached state remains private
until the receipt installer asks the certified service to publish the decoded
canonical cut. Production Pump and anti-entropy Snapshot clients enforce the
per-frame limit in Connect before unmarshal and charge the exact decompressed
protobuf payload presented to the codec against the stream transport budget;
compression and duplicate known fields therefore cannot hide received work.
A decoded Connect client stream does not expose those bytes to the collector,
so its spool remains a separate deterministic canonical-byte contract: the
collector recursively validates the parsed message, including unknown fields
and typed-nil oneofs, then deterministically re-encodes it. Raw-observing
stream adapters may additionally supply exact frame bytes, in which case
ambiguous duplicate fields and nonminimal wire encodings are rejected before
staging. Production durable mode shares one
collector-backed installer across Pump and anti-entropy; graph-only mode never
constructs it. The production collector caps each frame at 8 MiB, decompressed
transport payloads and the canonical spool independently at 512 MiB, graph
frames at 1,048,576, origin rows at 65,536, and active receipt rows, retired
epochs, and retired receipt rows independently at the configured Store entry
cap. Its total frame cap is derived exactly from those independent row and
graph limits plus the header and footer.
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
The production backup scheduler persists that immutable cut as a canonical
unversioned, instance-scoped set with exactly three ordered members: unchanged
canonical active `LANTARCH` bytes, the exact `LANTWCUT` version 1 witness, and
canonical `LANTRET1` retired-catalog bytes. Its internal-schema-version-1
canonical JSON commit manifest binds the exact
member basenames, roles, formats, versions, sizes, and SHA-256 digests together
with a monotonic set ID, UTC timestamp, stable NodeID, active endpoint
generation-chain head, active epoch and policy fingerprint, retired policy-set
commitment, exact active/retired high-water, origin cutoffs, snapshot HLC/local
sequence, and WAL cut/tip frontier captured under the same committed view. A
domain-separated publication commitment covers that metadata and every
ordered member identity. The manifest/member decoder rejects unsafe paths;
missing, duplicate, reordered, or unknown members or fields; malformed or
noncanonical data; trailing bytes; invalid identity/time/ID/count/size fields;
digest mismatches; and cross-cut archive, retired-catalog, or WAL evidence.
The fully validated loader returns owned active and retired bytes, decoded
cut/tip witnesses, and the same-cut identity without consulting the live
appendable WAL, and is shared by retention so selection rules cannot drift.
Older backup-set marker namespaces and the obsolete `LRWLCUT2` witness are
unsupported rather than migrated.

Every configured backup-directory component is inspected without following an
intermediate symlink, and missing components are created one at a time. The
nearest existing ancestor's parent is synced first, so a retry certifies an
entry that may have survived an earlier failed parent sync; each new directory
entry is then made crash-durable by syncing its parent before publication
proceeds. Directory flushes use the platform durability primitive, including a
write-capable directory handle with `FlushFileBuffers` on Windows. Each member
is staged with `O_CREATE|O_EXCL`,
completely written, file-synced, closed, and renamed before the directory is
synced. The manifest is staged, written, file-synced, closed, and renamed last
as the sole commit point, followed by a final directory sync. The strict
canonical loader must validate the manifest and every bound member before the
set exists.
Cancellation and every filesystem error abort the attempt and clean only paths
whose exclusive create or completed rename proved ownership; files from
interrupted processes or racing writers are preserved and ignored rather than
guessed-owned. A manifest-rename error is publication-ambiguous even when the
rename reports no committed result: the operation returns the error and
preserves every final member unless it owned and removed the marker and
directory-synced that absence. This deliberately permits bounded member
orphans rather than risking a committed marker whose members were deleted.

Directory enumeration streams 128-entry batches and fails after 100,000 total
entries, including foreign and orphan names. Discovery and ID allocation keep
only the highest relevant candidate. Retention keeps only its configured
newest-set min-heap, then makes a bounded second pass to collect older
candidates. It closes the directory stream, revalidates each candidate, and
then prunes its manifest first, syncs the directory, removes its members, and
syncs again.
Other instances, invalid or legacy markers, unrecognized files, and unproven
orphan files are untouched. `retain=0` keeps all. Periodic, manual, and
final-shutdown attempts serialize, while per-instance IDs remain unique and
increasing across repeated/backward clocks and process restarts.

The backup package exposes read-only strict newest-marker discovery as a
prerequisite for startup restore. It recognizes canonical current-format
manifest names in one configured instance scope plus exact owned markers in
the obsolete versioned v1 and v2 namespaces solely for terminal
unsupported-format refusal. It selects the highest recognized set ID before
decoding it and returns an explicit not-found sentinel when no marker exists.
Once selected, an unsupported namespace or schema version, or any
canonical-loader failure in that marker or its members, is terminal; discovery
never scans backward to an older valid set.

The durable restore layer consumes this loader inside
`provider.NewServingRuntime` and never reuses the graph-only
`Backupper.RestoreOnStartup` path. Normal `restart` recovery is attempted
first and a complete current WAL remains authoritative without reading a
backup. Fallback is limited to a missing or damaged newest committed baseline
sidecar. Under the FileWAL lease it validates the backup's recorded archive
cut, exact FileWAL offset/digest/rolling chain, clock and tip journals, NodeID,
epoch, policy, and generation at that cut; validates the complete current
suffix; and rejects lease contention, ambiguous WAL state, mismatched
identity/policy, or a later valid generation without fallback. It restores
the graph, active Store, retired catalog, origins, HLC floor, Log/WAL, and
generation coherently, then commits a fresh canonical combined baseline
before certification.

`fresh` restore is the explicit total-cluster-loss path. It still rejects any
existing target bytes and requires an operator-configured active epoch
different from the archive's active epoch. It restores the archived graph and
origins, creates a new endpoint generation and empty active Store, converts
the archived active receipt Snapshot under its original policy into retired
evidence, and unions it with the archived retired catalog under the new
configured aggregate bounds and effective high-water. Only still-live
evidence is installed, and a canonical combined baseline commits before
certification. A generation created for pending fresh restore records that
its initial baseline is mandatory; a crash before that marker cannot later
certify the empty target as a normal restart.

`Clock.Now()` advances only in-memory HLC state, and an aborted `Store.Begin`
or a direct `Store.Lookup` may advance high-water without a WAL entry. A serving
recovery still needs an atomic installer and proof that the WAL covers the
captured frontier or an epoch rollover. The installer
must validate and install all sections together before serving. Total-cluster restore still rotates the active epoch unless a complete durable
WAL proves the exact current frontier. The production scheduler and startup
restore both consume the private producer's immutable three-member cut.
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
canonicalizes its detached active whole-state image and retired catalog, and
builds the WAL-cut member directly from the captured witness without reopening
the live WAL path. Those three immutable members are persisted by the production
scheduler as the receipt backup set. Production requires exact active and
retired clock high-water equality and does not normalize, lift, or discard a
zero-value or lower-cut retired snapshot.
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

A private fixed-size WAL baseline marker binds a canonical `LANTCBLN` sidecar
digest and byte count to its source cutoff/HLC, epoch, policy fingerprint,
previous generation, rotated generation, exact active and retired receipt
clock high-water, and actual staged local HLC restore floor. The marker format
is `ReceiptBaselineFormatCombined` with numeric value 1. The `LANTCBLN`
container likewise has schema version 1 and embeds the existing canonical
active `LANTARCH` bytes unchanged plus a bounded canonical `LANTRET1` retired
section. The retired section binds the active epoch and aggregate caps;
decoding requires those fields and its high-water to match the active section
exactly. This private persistence path recognizes only
`<wal>.receipt.<lowerhex-digest>.baseline` sidecars; all other magic, format
values, and path shapes are unsupported foreign input and are neither read nor
cleaned up.

Before the marker commit, installation fully validates the incoming active and
retired images, snapshots local retired evidence under a short exclusive cut,
and forms the deterministic exact union with
`NewRetiredCatalogFromUnion`. Conflicts, active-epoch rows, aggregate-cap
overflow, or active high-water rollback fail closed. Encoding and sidecar
fsync happen outside the publication cut; a bounded optimistic retry requires
the active Store, retired revision, and origins to still match before staging.
The immutable combined candidate is fsynced under its content-addressed
`<wal>.receipt.<lowerhex-digest>.baseline` name before the marker commit.
Graph, active Store, retired slot, origins, clock, and generation publish only
in the WAL post-publication callback. Cancellation is checked again after
reversible staging and immediately before the marker write.

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
corruption. Orphan canonical baseline sidecars without a marker are cleanup
candidates;
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
and graph-only backup restore. `fresh` and `restart` require an absolute
`LANTERN_RECEIPT_WAL_PATH`, a nonzero `LANTERN_RECEIPT_EPOCH`, and explicit
immutable retention, entry-cap, and logical-byte-cap policy. They also require
an explicitly configured, nonzero `LANTERN_NODE_ID`; graph-only mode retains
the historical random-per-boot fallback, but durable mode must not resume a
local Log at sequence N+1 under a new origin. They certify one owned
graph/Store/origin/Log/HLC/epoch/generation bundle before constructing either
service, the primary listener, metrics server, or replication pump.
Wire cleanup releases later owners before this bundle, and `App` retains the
bundle until all serving goroutines stop. Durable mode selects receipt-set
production from the exact certified runtime and performs durable restore in a
private identity-bearing barrier before `NewRuntimeCertified`. Listener,
metrics-server, Snapshot-installer, Pump, anti-entropy, and backup-scheduler
construction all follow that barrier. Graph-only mode alone keeps the
historical `.lbk` restore in `App.Run`; its producer, filenames, retention,
metrics, and behavior are unchanged.

This runtime mode is private infrastructure only. It does not enable
`GetReceiptCapability`, receipt status, receipt-bearing client mutations, peer
capability negotiation, or retired-aware peer transport.
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
shared installer to those exact identities. Durable Pump and anti-entropy
request and atomically install `RECEIPT`; graph-only mode and
`BackupSnapshot` restore remain graph-only and cannot certify receipt
continuity. Public enablement still requires the later capability/status and
receipt-bearing client mutation slice. `Store.Begin` advances clock high-water
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
