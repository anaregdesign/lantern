# Replication architecture (RFC)

> Status: **Accepted** for v1.
> Tracking issue: [#175](https://github.com/anaregdesign/lantern/issues/175).
> Implementation: issues [#176–#192](https://github.com/anaregdesign/lantern/issues?q=label%3Aha).

This document is the single source of truth for Lantern's High Availability
(HA) story. All replication-related issues implement against it; any deviation
from the invariants below requires amending this file in the same PR.

---

## 1. Goal

Lantern must run as a **leaderless, full-replica cluster** with the operational
shape of a Kubernetes `Deployment` (any pod accepts any RPC) while still
holding the actual workload shape of a `StatefulSet` (stable pod identity for
peer discovery).

```
Client (sdks/go: Connect over h2c to a ClusterIP / reverse proxy)
                │
   ┌────────────┼────────────┐
   ▼            ▼            ▼
 pod-0 ◀──── pod-1 ◀──── pod-2     (StatefulSet, RF = number of pods)
 full replica / any pod accepts any R/W / no leader / no consensus
```

Every replica holds the **full graph**. Writes accepted on any node are
replicated asynchronously to all peers. No single node is special; no quorum
is required for either reads or writes.

## 2. Invariants

1. **Single-node commit, async fan-out.** A write is committed locally on the
   receiving node, then asynchronously fanned out to all peers. Clients see
   success as soon as the local commit lands.
2. **Idempotent, commutative mutations.** Every mutation carries an HLC
   timestamp (§5) and a contribution ID (§6). Applying the same mutation more
   than once, or out of order relative to other mutations from other origins,
   is a no-op or produces the same final state.
3. **Snapshot + tail bootstrap.** A new pod calls `Snapshot` against any peer
   to seed its in-memory state. `SnapshotHeader.cutoff_seq_per_origin`
   carries the per-origin watermark the snapshot was materialised against;
   the bootstrapping peer resumes by opening `Subscribe(from_seq_per_origin
   = {origin: seq + 1 for each (origin, seq) in cutoff_seq_per_origin},
   from_local_seq = cutoff_local_seq + 1)` against the same responder, so the
   snapshot and live tail stitch without gap or overlap while portable
   per-origin cursors still detect an evicted replay window. See #415
   (Reading B) and the wire types in §8.2/§8.3.
4. **Readiness gates traffic.** `/healthz/ready` returns `NOT_SERVING`
   whenever replication lag exceeds `LANTERN_MAX_REPLICATION_LAG` or an
   observed peer reports a different search config fingerprint, so the load
   balancer drains the instance. Graph replication continues across a search
   mismatch for repair and diagnosis. **Single-instance mode** (static
   discovery with empty `LANTERN_PEERS`) bypasses this gate; DNS discovery
   selects peer mode even before the first peer resolves.
5. **No leader, no Raft, no external storage.** v1 is intentionally
   ephemeral. Single-pod loss recovers from peers; total-cluster loss is
   accepted data loss **unless snapshot backups are configured**
   (`LANTERN_BACKUP_*`, see [backup.md](backup.md)), in which case each
   node restores its newest dump on boot.
6. **Rolling update safe.** One pod down → remaining pods serve → new pod
   bootstraps → ready → next. This invariant is about **cluster
   availability** (the cluster keeps accepting requests throughout). **Zero
   client-visible request drops** additionally require the graceful-drain
   window (#768): on `SIGTERM` the rotating pod flips readiness to
   `NOT_SERVING` immediately, then keeps its listener serving for
   `LANTERN_DRAIN_DELAY_SECONDS` so kube-proxy / load balancers deregister
   it before it stops accepting. See the runbook §7.

## 3. Binding decisions (D1–D7)

| # | Decision | Default | Rationale |
|---|---|---|---|
| D1 | Crash persistence | **None for v1.** WAL is a hook only. | Bootstrap from peers covers single-node loss; persistence adds operational surface area we don't yet need. |
| D2 | External CDC | **Same `Subscribe` RPC**, authenticated within one deployment-wide security domain; tenant ACLs are not defined. Under the leaderless Subscribe contract (#415, Reading B), an external CDC consumer attaches to any **one** replica and observes every committed cluster mutation — failover to a different replica is supported by passing the per-origin watermark in `SubscribeRequest.from_seq_per_origin`. | Internal replication and external CDC are isomorphic; splitting RPCs would duplicate machinery. The per-origin cursor lets consumers spread load across replicas without reimplementing the internal pump's dedup. The offline storage contract is specified in [ADR 0002](decisions/0002-dart-offline-repository-contract.md#sqlite-and-asynchronous-store-implementation): atomically persist invalidation and chunk progress, advance the last-applied origin sequence only on the final chunk, then resume at that sequence plus one. The #1116 projection, typed SDK facades, and storage-neutral offline consumer are implemented. The production Dart bridge and physical release qualification remain in #1314. |
| D3 | WAN replication | **Out of scope for v1**, single DC only. HLC max skew bound = **500 ms**. | Geo replication requires looser skew + read repair; defer until single-DC HA is proven. |
| D4 | Tombstone TTL | **Cluster-wide config, default 1 year (8760h).** Any `Add*` / `Put*` whose TTL would exceed tombstone TTL is **rejected** with `InvalidArgument`. | Resurrection-proof deletes require tombstones to outlive every live contribution. This is a real backwards-incompatible constraint. |
| D5 | Workload kind (k8s reference impl) | **StatefulSet** (not Deployment). | Stable pod identity simplifies peer discovery; leaves room for an optional WAL PVC later. The *user experience* is Deployment-like; the *resource kind* is `StatefulSet`. |
| D6 | Cluster membership v1 | **Static `LANTERN_PEERS` env var.** v2 adds DNS-based discovery (#190). | Smallest surface that ships. Any DNS-routable platform (k8s headless Service, Compose service name, Nomad, plain DNS A-records) can populate it trivially. |
| D7 | Supported deployment topologies | **Full HA:** k8s StatefulSet, Nomad, plain VMs, Docker Compose with stable peer hostnames. **Single-instance (no HA):** any platform without stable per-instance addressing — Docker Compose single service, or any container runtime that hides/recycles instance addresses. **Not supported:** running multiple address-hidden instances as a replicated cluster. | Leaderless P2P needs **stable inter-instance addressing** and **long-lived inbound gRPC streams between peers**. Platforms that intentionally hide instance addresses and recycle instances fit single-instance deploys (still useful as a fast in-memory KVS) but not the replicated topology. |

The bounded mutation-receipt extension is specified in
[ADR 0010](decisions/0010-bounded-mutation-receipts.md). It requires an atomic
graph/result/receipt/log boundary and the contiguous publication work in
#1282. Receipt RPCs and client mutation APIs remain disabled. In private
durable receipt-WAL mode, the guarded follower, Snapshot producer, detached
collector, and durable baseline primitive are wired into Pump and
anti-entropy through one shared exact-`RECEIPT` installer. Graph-only mode
and D1 remain unchanged.

## 4. CRDT semantics per RPC

Every write RPC is classified as one of three CRDT shapes. The classification
is **load-bearing**: it determines whether two concurrent writes commute, and
whether re-applying an already-seen mutation is a no-op.

| RPC | CRDT shape | Conflict rule |
|---|---|---|
| `PutVertex(es)` | LWW-Register | Higher HLC wins; same HLC ⇒ higher origin ID wins (deterministic tiebreak). |
| `AddEdge(s)` | G-Set of contributions above a reset floor | Each unique `ContribID` is an element at its own HLC. Re-apply = set-insert ⇒ no-op. A Put/Delete at HLC `R` removes rows at or before `R`; a later Add survives regardless of delivery order. Weight is the float32 sum of live rows in `(HLC, ContribID)` order. |
| `PutEdge(s)` | LWW reset on `(tail, head)` | The greatest Put/Delete HLC is the reset floor. The Put supplies one base value and keeps every Add whose HLC is strictly greater than its own. Higher HLC wins; the origin ID is part of the HLC tiebreak. |
| `DeleteVertex(es)` | Tombstone (LWW) | A tombstone is itself an entry with HLC. Any `Put*` / `Add*` whose HLC < tombstone HLC is dropped. Tombstone TTL = D4. |
| `DeleteEdge(s)` | Tombstone (LWW reset) on `(tail, head)` | Removes the base value and every Add at or before its HLC while preserving later Adds. The floor is retained for D4, including when later Adds make the edge live. |

An origin samples one absolute D4 deadline for each exact Delete batch and
publishes it as `Mutation.tombstone_expiration` alongside the HLC and victim
identities. Prefix Deletes publish exact victim batches with the same rule.
Followers apply that deadline unchanged, even when delivery is delayed past
it; a D4-enabled receiver rejects a Delete missing the deadline before it
enters the pending replication queue. This keeps Subscribe and FileWAL replay
from silently starting a new retention window. The origin samples the deadline
before its HLC stamp. A receiver rejects deadlines later than either the
origin HLC wall time plus its configured D4 TTL or its own current wall time
plus D4 TTL and the D3 skew allowance. Both checks are needed: the first
prevents a late relay from stretching the origin's window, while the second
prevents a forged future HLC from creating an unbounded tombstone. The origin
runs the same checks before changing its graph, so a wall-clock rollback
between deadline and HLC sampling fails closed.

An unconditional Put whose absolute expiration is already past at the
serving node is still an accepted LWW mutation. It returns `EXPIRED`, removes
the previous value/edge at that identity, records its HLC, and is replicated.
This delete-like overwrite is distinct from a tombstone (it has no independent
tombstone retention window). Its HLC is retained as a causal barrier even
though no live cache entry/bucket is created, and ordinary TTL GC never reaps
that barrier. Otherwise a delayed HLC-older live Put could resurrect the
identity after GC or a local clock rollback. The barrier is part of replication
Snapshot bootstrap state (see §8); it is not exposed by graph reads or
`BackupSnapshot`. Omitting the accepted-expired mutation from the mutation log
would likewise let a peer retain an older live value. `if_absent` evaluates its
condition first: an existing live vertex returns `CONDITION_NOT_MET` and is
preserved; an absent born-expired candidate returns `EXPIRED` without creating
live state.

The barrier is a state transition, not a permanent second representation of
the identity. An equal/newer live Put supersedes the floor and removes the
barrier. An explicit singular/plural Delete with an HLC at least as new as the
floor instead replaces the barrier with a normal D4-bounded tombstone; the
Delete may report that no live value existed while still completing this
causal transition. A strictly older Put or Delete is rejected and leaves the
barrier intact. Prefix Delete RPCs enumerate the live prefix indexes, so they
cannot discover or reclaim an identity represented only by a barrier. Use an
exact-key/pair Delete when bounded D4 retention is required for such an
identity.

For mixed edge histories, each replica keeps the winning reset floor and only
the Add rows causally later than it. A Delete floor is D4-bounded: convergence
requires all lagging replicas to learn it before that deadline, just as for
Put/Delete histories. A reused `ContribID` with a different payload is outside
the contract until server-authoritative operation receipts (#1115) provide
matching evidence.

Reads (`GetVertex(es)`, `GetEdge(s)`, `Illuminate`, `SearchVertices`) are
local-only — they never block on peers and never read-repair. Read-after-write
across nodes is **eventual**, bounded by the readiness gate (invariant 4) plus
the pump's flush latency target (§9).

`SearchVertices` is derived from each replica's local live graph. During lag
or a partition, membership, document frequency, BM25 scores, and top-k order
may differ across replicas; a client-side failover can therefore return a
different response. Once replicas have the same live graph and identical
`search.config_fingerprint`, they return the same ordered hits and score bits.
BM25 scores are query- and corpus-relative, so clients must not compare their
numeric values across lagging replicas or unrelated queries. Mixed
search-affecting configuration is prohibited on serving members: fingerprint
mismatch keeps readiness `NOT_SERVING` even though replication continues.
Snapshot bootstrap, anti-entropy snapshot repair, and backup restore mark the
local search index `INCOMPLETE` before replay and rebuild it from the complete
live graph before reporting `HEALTHY`. `DISABLED` remains a separate state.
The canonical projection, error, cursor, and failover rules are in the
[SearchVertices contract](search.md#replication-failover-and-cursors).

## 5. Hybrid Logical Clock

`Timestamp = (wallNs int64, logical uint32, nodeID [16]byte)`. Wall is in
**nanoseconds since the Unix epoch** to match `time.Time.UnixNano()` and keep
the proto encoding (#178) loss-free. `nodeID` is folded into every timestamp
so two distinct origins can never produce a colliding stamp; the origin
tiebreak from §4 is therefore intrinsic to ordering rather than bolted on.

Reference implementation: [`core/hlc`](../core/hlc/hlc.go).

### 5.1 Local tick (called on every locally-originated mutation)

```
now := time.Now().UnixNano()
if now > last.wallNs:
    last = Timestamp{wallNs: now, logical: 0, nodeID: self}
else:
    last = Timestamp{wallNs: last.wallNs, logical: last.logical + 1, nodeID: self}
return last
```

### 5.2 Update on receive (`recv` carries a remote Timestamp `r`)

```
now := time.Now().UnixNano()
rWall := r.wallNs
if rWall > now + MaxSkew:        // §5.3
    rWall = now + MaxSkew
max := max(now, last.wallNs, rWall)
switch max:
case last.wallNs where last.wallNs == rWall:
    last = Timestamp{wallNs: max, logical: max(last.logical, r.logical) + 1, nodeID: self}
case last.wallNs:
    last = Timestamp{wallNs: max, logical: last.logical + 1, nodeID: self}
case rWall:
    last = Timestamp{wallNs: max, logical: r.logical + 1, nodeID: self}
default: // now strictly greater
    last = Timestamp{wallNs: max, logical: 0, nodeID: self}
```

Note that the returned timestamp always carries the local `nodeID`; the
remote `nodeID` is only used by the comparator in §5.4.

### 5.3 Skew clamp

If `r.wallNs > now + MaxSkew` (default `MaxSkew = 500ms`, per D3), the wall
component is **clamped** to `now + MaxSkew` and an `OnSkewExceeded` callback
fires. The intended default wiring increments `lantern_hlc_skew_clamped_total`,
but that counter is **not yet wired** (the provider leaves `OnSkewExceeded`
nil — see #180 and #182); until then, monitor NTP directly. The remote
timestamp is never rejected — replication keeps making progress even when
peers drift, and operators are expected to fix NTP. (Earlier drafts of this RFC rejected with
`OutOfRange`; that was changed during #176 implementation because rejecting
risks a cascading replication stall while the clock heals.)

### 5.4 Comparison

`a < b` iff `(a.wallNs, a.logical, a.nodeID) < (b.wallNs, b.logical, b.nodeID)`
lexicographically. `nodeID` is compared bytewise. Two distinct origins thus
yield a strict total order without any extra tiebreak machinery.

## 6. Contribution IDs

Every additive edge row carries a 24-byte `ContribID`. A caller can supply a
nonzero 24-byte ID to make retries of the same contribution idempotent. For an
unkeyed replicated Add, the origin and followers synthesize the same ID from
the mutation's origin, origin-local sequence, and original request index:

```
contribID = originID[16 bytes] || bigEndian64((localSeq << 16) | wireIndex)
```

- `originID` is `LANTERN_NODE_ID` parsed as 16 bytes of hex (32 chars, "0x"
  prefix tolerated). Malformed values fall back to a `crypto/rand` 16-byte
  identifier and emit a warning. Origin is stable for the pod's lifetime.
- `localSeq` is a per-origin monotonic counter, 1-indexed at startup.
- `wireIndex` is the original position in an `AddEdges` request, including nil
  slots. Synthesis accepts only indexes 0–65,535 and sequences 1–2⁴⁸−1; an
  out-of-range unkeyed Add is rejected before graph, log, or origin progress.
  Explicit nonzero 24-byte IDs do not use this packing limit.

Properties of synthesized IDs (assuming distinct origin IDs and monotonic
origin sequences):

- Globally unique without coordination.
- Suitable as a G-Set element for `AddEdge` contributions, so re-applying a
  mutation already present is a cheap set-insert no-op.

Callers supplying explicit IDs must keep them unique across distinct Add
intents; reusing one for a different payload is outside the contract until
#1115 provides server-authoritative receipt evidence.

## 7. Mutation log

In-memory ring buffer (`core/mutationlog`), append-only, with a WAL hook
(D1 leaves the hook empty for v1). Each stored `Entry.Seq` is a position in
that replica's mixed local-and-relay log. The carried `Mutation.Seq` is a
separate, contiguous sequence belonging to `Mutation.Origin`:

```go
type Mutation struct {
    Seq    uint64   // next seq for this origin only
    Origin []byte   // 16-byte HLC NodeID
    HLC    HLC
    Op     MutationOp
}
```

`Subscribe.from_local_seq` indexes the responder's `Entry.Seq` ring position;
`from_seq_per_origin` filters by each mutation's portable `(origin, seq)`.
The service tracks the **contiguous committed prefix** per origin, rather
than the maximum seq observed. A relay may receive seq 4 before seq 1–3, but
it must keep seq 4 outside its graph, relay log, and Snapshot cutoff until
the gap closes. The pending queue is bounded (4,096 mutations, 32 MiB of
encoded mutation bytes, and a maximum seq gap of 4,096); overflow rejects
the incoming mutation before graph apply. A failed relay-log append leaves
the graph-applied frontier retryable without advancing the advertised cutoff.
`Pump.PeerSnapshot.AppliedSeq` is an arrival observation that can include a
queued future seq; it is not a contiguous committed cursor. Resume and
Snapshot decisions use `OriginStates` and the Snapshot header cutoff instead.
A relay or local graph-first log append failure also marks the responder's CDC generation as
`gapped`: all already-open `Subscribe` streams close with
`FailedPrecondition`, and new `Subscribe`/`Snapshot` calls fail the same way
until append-only retry or verified Snapshot repair clears the fault. Streams
from the old generation never resume after repair. This prevents an external
consumer from mistaking a graph-visible but unpublished remote mutation for
a complete mutation history.
A completed Snapshot replay may advance the prefix past unavailable log
entries because its graph image supplies the missing effects.

Buffer size is `LANTERN_MUTATION_LOG_CAPACITY` (default 100,000). Overflow
drops **oldest** entries; consumers that fall behind that far are forced to
re-bootstrap via `Snapshot`. The capacity is published as
`lantern_mutation_log_capacity`; successful appends increment
`lantern_mutation_log_entries_total`.

## 8. Wire protocol

### 8.1 Mutation message

The realised wire types live in
[proto/graph/v1/replication.proto](../proto/graph/v1/replication.proto) and
their generated Go form in `pb/graph/v1/replication.pb.go`. The shipped
shapes are:

```proto
message HLCTimestamp {
  int64  wall_ns = 1;   // nanoseconds since Unix epoch
  uint32 logical = 2;
  bytes  node_id = 3;   // 16 bytes, matches core/hlc.NodeID
}

message MutationOp {
  oneof op {
    PutVertexRequest               put_vertex                = 1;
    PutVerticesRequest             put_vertices              = 2;
    DeleteVertexRequest            delete_vertex             = 3;
    DeleteVerticesRequest          delete_vertices           = 4;
    DeleteVerticesByPrefixRequest  delete_vertices_by_prefix = 5;
    AddEdgeRequest                 add_edge                  = 6;
    AddEdgesRequest                add_edges                 = 7;
    PutEdgeRequest                 put_edge                  = 8;
    PutEdgesRequest                put_edges                 = 9;
    DeleteEdgeRequest              delete_edge               = 10;
    DeleteEdgesRequest             delete_edges              = 11;
    DeleteEdgesByPrefixRequest     delete_edges_by_prefix    = 12;
    ReplicatedPutVertices          replicated_put_vertices   = 13;
    ReplicatedPutEdges             replicated_put_edges      = 14;
  }
}

message VertexCausalBarrier { string key = 1; }
message ReplicatedPutVertex {
  oneof outcome {
    Vertex live = 1;
    VertexCausalBarrier causal_barrier = 2;
  }
}
message ReplicatedPutVertices {
  repeated ReplicatedPutVertex entries = 1;
}

message EdgeCausalBarrier { string tail = 1; string head = 2; }
message ReplicatedPutEdge {
  oneof outcome {
    Edge live = 1;
    EdgeCausalBarrier causal_barrier = 2;
  }
}
message ReplicatedPutEdges {
  repeated ReplicatedPutEdge entries = 1;
}

message Mutation {
  uint64       seq    = 1;
  HLCTimestamp hlc    = 2;
  bytes        origin = 3;
  MutationOp   op     = 4;
}
```

Current HA origins encode a successful `Delete*ByPrefix` call as an exact
`DeleteVertices` / `DeleteEdges` mutation containing only the identities that
the origin committed under its per-call limit and causal-metadata budget. The
prefix oneof arms remain decodable for older mutation logs, but must not be
used for new origin writes: applying a broad predicate on a peer could widen a
bounded origin commit and permanently diverge the replicas.
Receivers reject legacy predicate-shaped Delete relay records before graph
apply. A context-interrupted predicate scan could otherwise mutate only part
of the graph before returning an error, with no safe retry boundary.

Deviations from the §7 conceptual sketch (recorded as part of #178):

- HLC is **nanoseconds**, not milliseconds (matches `core/hlc.Timestamp`).
- `origin` is a 16-byte `NodeID` (mirrors `HLCTimestamp.node_id`), not a
  packed `uint64`. The split between `contrib_seq` and `payload` does not
  appear on the wire — per-origin `(origin, seq)` already plays the
  contribution-ID role and the request payload is carried directly by the
  `MutationOp` oneof.
- Most `MutationOp` arms reuse the existing write-RPC request messages.
  Accepted Put requests are the intentional exception: the origin emits one
  ordered, authoritative `ReplicatedPutVertices` or `ReplicatedPutEdges`
  mutation. Its `live` and `causal_barrier` entries remain interleaved in the
  accepted items' original request order. This preserves duplicate-identity
  sequencing inside a batch and prevents a receiver's wall clock from
  reclassifying the origin's accepted-expired decision. `CONDITION_NOT_MET`
  and `SUPERSEDED` items made no committed state transition, so they are not
  replicated. If no item committed, no mutation is logged.
- A receiver applies all ordered authoritative entries under one graph-cache
  lock with the mutation HLC. It must not regroup live entries and barriers by
  outcome: doing so changes the result for duplicate keys/pairs in one request.

### 8.2 Subscribe (server streaming)

```proto
service LanternReplicationService {
  rpc Subscribe(SubscribeRequest) returns (stream SubscribeResponse);
}

message SubscribeRequest  {
  // Per-origin resume cursor. Keys are 32-char lowercase hex of the
  // 16-byte HLC NodeID; values are the next origin-anchored seq the
  // consumer expects from that origin. An empty map = cold start =
  // deliver every retained entry from every origin.
  map<string, uint64> from_seq_per_origin = 1;
}
message SubscribeResponse { Mutation mutation = 1; }
```

Realised on a dedicated `LanternReplicationService` (split from
`LanternService`) so that replication can be authorised, throttled, or
disabled independently of the public read/write API; the split also avoids
a cyclic proto import between `graph.proto` and `replication.proto`.

**Leaderless Subscribe contract** (#415, Reading B). Every replica's
local mutation log retains entries from every cluster origin: a write
that lands at replica X via `PutVertex` is appended at X's local log
(via local publication); replicas Y and Z then receive it through the peer
pump and append it to their own local logs via
`LanternService.ApplyMutation` (which publishes only the next contiguous
origin seq to avoid double-Append from fan-out triangles).

Consequence:

- A consumer that picks **any one** replica and calls `Subscribe` with
  an empty cursor observes the entire cluster's mutation stream,
  ordered by per-origin local seq inside each origin and HLC-orderable
  across origins. There is no need to subscribe to every replica and
  dedupe by `(origin, seq)` on the client side; the server already
  does that work via the per-origin commit cursor.
- A consumer that fails over from replica X to replica Y resumes by
  sending the highest seq it has already observed FOR EACH origin in
  `from_seq_per_origin`. The new replica delivers only entries with
  `mu.Seq >= cursor[origin]` for origins present in the cursor;
  origins absent from the cursor are delivered from the oldest
  retained entry (so a freshly-joined origin is picked up
  automatically).
- `Mutation.Seq` is the originating writer's local seq, NOT the
  forwarding replica's local seq. This is preserved end-to-end: the
  Subscribe relay never overwrites `mu.Seq`, and the originating writer
  allocates it independently of the mixed relay log's `Entry.Seq`.

Local Put/Delete and capped prefix Delete hold the same publication cut from
graph apply through local log append. If the WAL rejects an append after the
graph changes, the handler returns `Unavailable` because its original result
is ambiguous. It keeps one exact mutation, including conditional Put outcomes
and exact prefix victims, for append-only repair before any later local write
can claim that origin seq. A failed repair leaves the graph untouched and CDC
`gapped`. `AddEdges` remains log-first; it repairs an earlier local gap before
its own append. A retried client request is a new operation after repair, so
original-result recovery still requires #1115 receipts. The identity-only CDC
server and storage-neutral offline consumer are implemented under #1116;
their production Dart package bridge and release qualification remain #1314.

The internal peer pump uses the same RPC. Ordinary sessions start with an
empty portable cursor and rely on `ApplyMutation`'s contiguous cursor to dedup
duplicate hops; snapshot recovery resumes with both header-derived origin and
same-responder local cursors. It still performs input-side self-echo
suppression (`Mutation.Origin == local NodeID → drop`) as defence-in-depth.

Back-pressure and publication faults: server terminates the stream with
`FAILED_PRECONDITION` (`gapped`) if (a) the ring has been truncated below the
requested responder-local replay position, (b) the consumer's send buffer
overflows, or (c) a local or remote mutation changed the graph but its log
append failed. The fault also rejects new Subscribe/Snapshot attempts until
repair, and closes every stream from the previous generation even if repair
completes quickly. After repair the consumer must re-bootstrap via
`Snapshot` and resume `Subscribe` with the
`cutoff_seq_per_origin` and `cutoff_local_seq` returned by `SnapshotHeader`.

Handler implementation notes (issue #180):

- The handler is `service.LanternReplicationService` in
  `server/service/replication.go`; it is wired in `server/cmd/wire.go`
  alongside `LanternService` and shares the same `*mutationlog.Log` as
  the write path.
- The handler maps `mutationlog.ErrGapped` to `codes.FailedPrecondition`
  with the reason `"gapped"`, both at subscribe time (initial check) and
  when the in-flight channel is closed by the log's slow-subscriber
  eviction. This matches the wire contract above.
- The handler forwards the buffered `*pb.Mutation` with its originating
  writer's `Mutation.Seq` intact. A relay's replica-local `entry.Seq` is a
  separate transport cursor and must never overwrite the portable origin seq.
- Health: a separate service name `graph.v1.LanternReplicationService`
  is registered with the grpc health server and flipped to
  `NOT_SERVING` on shutdown alongside `LanternService`.
- Metrics: `lantern_subscribe_active_streams` (gauge) and
  `lantern_subscribe_dropped_total{reason}` (counter; `reason ∈ {gapped,
  send_failed}`) are pre-rendered in `server/metrics/metrics.go`.

#### Identity-only CDC contract (#1116; server and core consumer implemented)

External cache invalidation uses this same `Subscribe` RPC and mutation
log. Its explicit `IDENTITY_ONLY` projection does not change the zero/default
full-`Mutation` stream used by peer replication. The request distinguishes
ordinary vector-cursor resume from bootstrap. The response carries exactly one
of a bootstrap checkpoint, a full mutation, or an identity chunk. This is the
server wire projection implemented by #1294 and typed SDK facades by
#1302/#1303/#1305. The storage-neutral offline live consumer is implemented by
#1300. The production Dart adapter and physical-device release qualification
remain in #1314.

A checkpoint contains the responder's **contiguous published** last sequence
for each origin. On bootstrap the server holds the publication cut gate while
it registers a live log subscriber at `last_local_seq + 1` and captures that
vector. It then releases the gate and sends the checkpoint as the first frame;
the registered tail buffers later mutations. Reading `OriginStates()` before
or after an independent `Log.Subscribe` would leave a skip window. The
subscriber buffer is finite; overflow ends the stream as `gapped`. A bootstrap
checkpoint reports this responder's state, not a cluster-wide consensus
barrier or a proof that another replica has caught up.

Normal resume supplies the next sequence for each origin. The server must
validate the vector against its contiguous frontier and retained ring window
while opening the log subscription under the same publication cut. If the
request needs an evicted entry from any origin, it returns `gapped`; it must
not silently begin at the oldest retained entry. A cursor ahead of a lagging
responder is allowed: that origin emits nothing until the responder reaches
the requested sequence. Origins absent from the vector begin at sequence 1.
The responder-local log sequence may optimize same-endpoint replay but is
never a portable cursor. A client durably advances an origin only after every
chunk of its mutation commits and verifies contiguous mutation sequence at
the consumer boundary. An empty identity set still requires a final marker so
it cannot create an invisible cursor hole.

An identity chunk carries `(origin, origin_seq, HLC, operation category,
chunk_index, is_last)` plus exact Vertex keys or collision-free Edge
`(tail, head)` pairs. It has no graph value, Edge weight, contribution ID,
credential, or auth metadata. `first_item_index` counts identities within the
projected mutation, starting at zero, including earlier chunks. The projector maps origin-authoritative Put,
Add, and exact Delete mutations to their exact committed identities. Capped
prefix Delete is already logged as an exact victim list; projecting the old
prefix predicate would invalidate keys outside the capped commit. Legacy
predicate-shaped log entries fail closed because their committed victim set
cannot be reconstructed safely after the fact. Projected chunks obey both
1,024-identity and 1 MiB serialized-frame caps, with a bounded chunk index;
an unrepresentable mutation closes the stream with a typed error rather than
truncating its invalidation set. An invalidation may conservatively include an
identity whose LWW write lost; it may never omit an identity that changed.

The stream is deployment-scoped. Current bearer auth protects one graph and
does not define tenant principals or ACLs. Prefix filtering, if later added,
is a performance optimization and not an authorization boundary. A client
must not infer linearizable global freshness from CDC in a leaderless,
asynchronously replicated cluster. Local append failures and any graph change
that cannot be published must force existing and new CDC streams into a
detectable fail-closed recovery state. #1282 closes the remote relay
boundary, and #1293 closes local Put/Delete graph-first publication. The
Pump and anti-entropy Snapshot installers also close the current CDC
generation before replaying graph frames: those changes have no individual
local-log entries. New streams remain gapped throughout replay, and an
interrupted or invalid Snapshot keeps that gap until a later verified install
advances the origin watermarks. A fresh bootstrap then revalidates resident
identities against the repaired responder. The server identity projection,
typed SDK facades, and storage-neutral mobile consumer are implemented;
physical-device release qualification retains its separate #1314 gate.

After `gapped`, a mobile consumer opens bootstrap and atomically marks its
**resident confirmed cache** Unknown at that checkpoint. It retains resident
identities in durable, bounded key-only recovery state, revalidates them in
bounded `GetVertices`/`GetEdges` batches against the checkpoint responder,
buffers the already-registered live tail, then applies buffered invalidations
and advances cursors transactionally. It does not request the full-value
replication `Snapshot`. If interrupted, it remains Unknown and resumes from
durable recovery progress. A Get started before invalidation must not restore
or return a stale confirmed value after the invalidation's commit; the offline
store and repository coordinate a read-versus-change epoch at that boundary.
The stream emits explicit mutations only. Absolute TTL is enforced locally;
no synthetic expiry event is implied, and finite freshness still bounds
staleness from expiring additive contributions.

### 8.3 Snapshot (server streaming)

```proto
rpc Snapshot(SnapshotRequest) returns (stream SnapshotResponse);

enum SnapshotFormat {
  SNAPSHOT_FORMAT_UNSPECIFIED = 0;
  SNAPSHOT_FORMAT_GRAPH_ONLY_V1 = 1;
  SNAPSHOT_FORMAT_RECEIPT = 3;
}

message SnapshotRequest {
  SnapshotFormat required_format = 1;
}

message SnapshotReceiptMetadata {
  // Each policy includes the 16-byte deployment epoch, 32-byte fingerprint,
  // retention_ms, max_entries, and max_bytes.
  ReceiptPolicy active_policy = 1;
  uint64 clock_high_water_unix_ms = 2;
  repeated OriginState origin_cutoffs = 3; // Sorted by raw origin bytes.
  repeated ReceiptPolicy retired_policies = 4; // Strict epoch order.
}

message SnapshotResponse {
  oneof entry {
    SnapshotHeader header = 1;   // first frame: cutoffs + receipt metadata
    SnapshotVertex vertex = 2;   // body: live vertex with stored HLC
    SnapshotEdge   edge   = 3;   // body: edge with per-contribution payloads
    SnapshotFooter footer = 4;   // last frame: all streamed counts
    SnapshotVertexCausalBarrier vertex_causal_barrier = 5;
    SnapshotEdgeCausalBarrier edge_causal_barrier = 6;
    SnapshotVertexTombstone vertex_tombstone = 7;
    SnapshotEdgeTombstone edge_tombstone = 8;
    SnapshotReceipt receipt = 9;
  }
}

message SnapshotHeader {
  // Per-origin watermark at snapshot-open time. Same keying convention
  // as SubscribeRequest.from_seq_per_origin (§8.2): 32-char hex of the
  // 16-byte HLC NodeID → highest origin-anchored seq the server had
  // applied from that origin. Empty when the cluster is cold.
  map<string, uint64> cutoff_seq_per_origin = 1;
  HLCTimestamp cutoff_hlc = 2;
  uint64 cutoff_local_seq = 3; // same-responder log position
  SnapshotFormat format = 4;
  SnapshotReceiptMetadata receipt_metadata = 5; // RECEIPT only
}

message SnapshotFooter {
  uint64 vertex_count = 1;
  uint64 edge_count = 2;
  uint64 vertex_causal_barrier_count = 3;
  uint64 edge_causal_barrier_count = 4;
  uint64 vertex_tombstone_count = 5;
  uint64 edge_tombstone_count = 6;
  uint64 active_receipt_count = 7;
  uint64 origin_count = 8;
  uint64 retired_epoch_count = 9;
  uint64 retired_receipt_count = 10;
}

enum SnapshotReceiptKind {
  SNAPSHOT_RECEIPT_KIND_UNSPECIFIED = 0;
  SNAPSHOT_RECEIPT_KIND_PUT_VERTEX = 1;
  SNAPSHOT_RECEIPT_KIND_PUT_EDGE = 2;
  SNAPSHOT_RECEIPT_KIND_ADD_EDGE = 3;
  SNAPSHOT_RECEIPT_KIND_DELETE_VERTEX = 4;
  SNAPSHOT_RECEIPT_KIND_DELETE_EDGE = 5;
}

message SnapshotReceiptContribution {
  bytes contribution_id = 1; // Nonzero 24-byte Add ContribID.
}

message SnapshotReceipt {
  bytes operation_id = 1;     // Exactly 49 bytes; strict wire sort key.
  bytes logical_call_id = 2;  // Exactly 16 nonzero bytes.
  uint32 item_index = 3;
  uint32 item_count = 4;
  SnapshotReceiptKind kind = 5;
  bytes intent_sha256 = 6;    // Exactly 32 bytes.
  uint64 deadline_unix_ms = 7;
  bytes original_result = 8;  // Exact opaque Store result, not recomputed.
  SnapshotReceiptContribution contribution = 9; // AddEdge only.
}

message SnapshotVertexTombstone {
  string key = 1;
  HLCTimestamp hlc = 2;
  google.protobuf.Timestamp expiration = 3; // original absolute D4 deadline
}

message SnapshotEdgeTombstone {
  string tail = 1;
  string head = 2;
  HLCTimestamp hlc = 3;
  google.protobuf.Timestamp expiration = 4; // original absolute D4 deadline
}

message SnapshotEdge {
  string tail = 1;
  string head = 2;
  HLCTimestamp hlc = 3;
  repeated SnapshotEdgeContribution contributions = 4;
}

message SnapshotEdgeContribution {
  float weight = 1;
  google.protobuf.Timestamp expiration = 2;
  bytes contrib_id = 3;   // 24-byte ContribID; empty = local-only
  HLCTimestamp hlc = 4;    // original Add HLC; Put row uses SnapshotEdge.hlc
}
```

Framing contract:

- The request and first header negotiate the image format. Zero request and
  zero header retain the graph-only interpretation while receipt writes
  are disabled. Numeric value `2` is intentionally unassigned and unreserved:
  it is not a legacy receipt format and is rejected as unknown. Graph-only
  Pump and anti-entropy explicitly request
  `GRAPH_ONLY_V1` and accept zero or `GRAPH_ONLY_V1` in the first header, but
  reject `RECEIPT` before applying any frame. Durable receipt-WAL mode
  instead gives both consumers one shared installer that requests exactly
  `RECEIPT` and rejects unspecified, graph-only, or unknown formats before
  publication. When receipt continuity is required, the responder
  advertises `PeerStatus.required_snapshot_format = RECEIPT`, rejects
  receipt-less full Subscribe before checking the retained ring, and rejects every
  graph-only Snapshot request. An opt-in receipt producer exists, but it must
  be configured with the exact service-owned atomic capture source and policy.
  The source's private identity must match the responder's primary service,
  serving runtime, graph backend, mutation log, HLC clock, origin tracker, and
  active Store plus the runtime-owned retired-catalog slot; a foreign or
  incomplete configuration fails before a header is sent.
  The durable production runtime configures this producer against its exact
  certified state. Its transport-neutral installer fully drains the stream
  into the bounded detached collector, validates the canonical archive, and
  invokes the exact certified durable baseline publication once. The incoming
  retired evidence is deterministically unioned with the local runtime catalog;
  exact duplicates are idempotent, while policy, row, relationship, and
  capacity conflicts fail before publication. Graph, active receipt Store,
  unioned retired catalog, origin vector, HLC floor, private combined baseline marker,
  generation, and resume cutoff publish as one cut. Cancellation, corruption,
  truncation, capacity, epoch/policy mismatch, and format downgrade failures
  publish nothing. No receipt write/status capability is enabled. Production bounds
  are 8 MiB per frame,
  512 MiB per complete wire/canonical image, 1,048,576 total frames, 65,536
  origin rows, and independent nonzero caps for active receipt rows, retired
  epochs, retired receipt rows, and graph frames. The active and retired row
  caps are each further limited by the configured Store entry cap; the
  complete frame cap still bounds their sum.
- The **header** is always the first frame. `cutoff_seq_per_origin` is
  the primary's contiguous per-origin committed prefix (every prior
  mutation has been applied to the graph and published to its relay log,
  or supplied by a verified Snapshot). Pending future seqs are absent from
  both the graph image and this cutoff. `cutoff_hlc` is the primary's `clock.Now()` at
  snapshot-open time. The consumer Subscribes with
  `from_seq_per_origin = {origin: seq + 1 for each (origin, seq) in
  cutoff_seq_per_origin}` and `from_local_seq = cutoff_local_seq + 1`
  against that same responder to stitch the snapshot and the live tail.
  `cutoff_local_seq` is deliberately not portable across replicas; the
  per-origin map remains the portable CDC/failover watermark.
  An empty map means the primary has not yet applied any origin
  (cold cluster); the consumer should pass an empty Subscribe cursor.
- A `RECEIPT` header carries the complete immutable active policy and every
  represented retired policy (deployment epoch, fingerprint, retention, entry
  capacity, and byte capacity), the active Store's monotonic clock high-water,
  and sorted full origin rows with both HLC and sequence. Retired policies are
  strictly epoch-sorted, exclude the active epoch, and each have at least one
  row. The retired catalog high-water must exactly equal the active Store's
  high-water; that sole clock authority must be no later than `cutoff_hlc` at
  millisecond precision. Origin rows must exactly agree with the graph cutoff
  map. One receipt stream follows the header: active and retired rows
  self-identify their epochs in `operation_id`, are globally strictly sorted
  by raw operation ID, and retain Store-reconstructable identity, grouping,
  intent, deadline, exact original result bytes, and Add contribution
  metadata. An empty retired catalog, zero active rows, and a receipt-only
  graph cut are valid.
- The **footer** is always the last frame. It reports ten separate actually
  streamed counts: live vertices, live edges, vertex causal barriers, edge
  causal barriers, vertex Delete tombstones, edge Delete tombstones, active
  receipt rows, origin rows, retired epochs, and retired receipt rows. Pump and
  anti-entropy consumers reject count mismatches, duplicate/missing
  header/footer frames, or any out-of-order body frame before advancing resume
  watermarks.
- Before sending the header, the receipt producer owns the complete detached
  sequence and canonicalizes graph frames by phase and identity. Contributions
  inside each edge are sorted by raw contribution ID. The receiver stages the
  whole candidate, reconstructs both receipt stores, and compares a canonical
  deterministic re-encoding before any serving-state mutation.
- Pump and anti-entropy open Snapshot with a dedicated bounded Connect client.
  Connect rejects any frame over the configured decompressed per-message limit
  before protobuf unmarshal, and a request-local codec charges the exact
  decompressed protobuf payload bytes, including duplicate known fields,
  against a separate stream transport budget. The collector's deterministic
  length-prefixed spool has its own canonical-byte limit. Its total-frame cap
  is exactly header + footer + the independent active-row, retired-row, and
  graph-frame maxima, so no section borrows another section's capacity.
- Every live vertex frame is self-describing and non-nil. In particular,
  endpoint vertices auto-created by `PutEdge*` / `AddEdge*` are serialized as a
  concrete `Vertex` carrying the endpoint key, expiration, and `nil` value arm;
  the internal `*Vertex == nil` sentinel is never exposed on the wire. A nil
  `SnapshotVertex.vertex` is a truncated/corrupt frame and consumers fail
  closed. This invariant is load-bearing for gap recovery because edge-only
  working sets contain implicit endpoints even when no `PutVertex*` call has
  occurred.
- Retained Put causal barriers are streamed **before live entries**.
  They use explicit `SnapshotVertexCausalBarrier` and
  `SnapshotEdgeCausalBarrier` oneof arms, never overloaded live
  `SnapshotVertex` / `SnapshotEdge` shapes. Receivers replay them through
  dedicated causal-barrier seams that create no vertex, endpoint, edge bucket,
  or Search document. Sending barriers first preserves an older retained floor
  when the same identity also has a newer live value.
- Active D4 Delete tombstones follow causal barriers and precede live entries.
  Their frames carry the original absolute expiration, so bootstrap and a
  repeated Snapshot do not start a new D4 window. Expired frames in transit
  count toward the footer but install no floor. Missing/invalid HLC or
  expiration, a truncated stream, and reordered frames fail closed before
  resume watermarks advance. Replay follows the existing remote-apply rule:
  it may exceed a locally configured causal budget rather than diverging from
  a peer that already committed the Delete.
- The snapshot deliberately preserves **per-contribution decomposition**:
  each `SnapshotEdge` carries its full list of live `SnapshotEdgeContribution`
  rows rather than a pre-summed weight. A zero-`ContribID` row represents the
  LWW Put value and is restored through `PutEdgeWithExpirationHLC`; non-zero
  rows retain their original Add HLC and are restored through
  `AddEdgeWithExpirationContribHLC`. `SnapshotEdge.hlc` carries the winning Put
  floor, including a retained accepted-expired Put barrier. Receivers reject
  malformed or missing Add HLCs and duplicate contribution identities before
  applying the frame. `ContribID` dedup makes the snapshot-then-Subscribe-tail handoff idempotent:
  any additive contribution that also appears in the replayed tail is detected
  and dropped at apply time.
- A live additive edge may coexist with a retained Put barrier or Delete
  tombstone. The source streams those floors before the live edge, then replays
  only Add rows newer than the floor at their own HLCs. Repeating Snapshot or
  replaying the overlapping Subscribe tail cannot duplicate the rows.

Implementation notes:

- The handler is `LanternReplicationService.Snapshot` in
  `server/service/replication.go`. It holds references to the same
  `Backend` and `*hlc.Clock` the write path uses; both are wired in
  `server/cmd/wire.go`. Production uses `GraphCache.SnapshotReplication()`:
  one sampled wall instant and one continuous graph write lock cover barrier
  migration plus materialisation of the barrier, active Delete tombstone, and
  live slices. The service also holds its Snapshot cut gate while it copies
  the origin/local-log cutoffs and this graph image: remote ApplyMutation and
  local Put/Delete graph-first commits and log-before-graph AddEdges cannot
  publish a cutoff ahead of the graph. A pending WAL fault rejects Snapshot.
  It releases the gate before sending any frame. Before copying state, the
  method moves Put floors with non-visible payloads (expired vertices and
  expired or dangling edge buckets) into the retained barrier
  maps. Capturing barriers and live state in separate lock passes is forbidden:
  TTL/GC could move a floor between the passes and make the snapshot omit both
  representations. The completed owned slices are then streamed frame-by-frame,
  canonicalizing implicit nil-valued endpoint
  vertices at the service boundary and honouring `stream.Context()`
  cancellation between sends.
- The receipt producer calls `ReceiptWholeStateSource` exactly once under the
  service publication gate. All header metadata, receipt rows, graph frames,
  origin/local cutoffs, and HLC values are derived solely from that detached
  cut; components are never re-sampled afterward. Before sending the header,
  it validates Store reconstruction, phase order, counts, field sizes, graph
  identities and endpoint closure, timestamp/HLC and contribution validity,
  duplicate/overlapping state, causal floors, origin/cutoff consistency, and
  an 8 MiB per-frame bound across the complete stream. Every nonzero graph HLC
  must be no later than both the global cutoff and that HLC origin's advertised
  frontier. The preflight recursively rejects unknown protobuf fields and
  typed-nil oneof wrappers before sizing or sending. The source reconciles the
  HLC clock floor to the captured Store high-water under the same publication
  cut before sampling the cutoff; overflow fails closed. Any malformed capture
  therefore sends no partial image. The ordinary graph-only producer and wire
  behavior are unchanged.
- Provider selection is runtime-aware. Graph-only mode passes no override and
  retains the historical in-place `GRAPH_ONLY_V1` installer. Durable
  receipt-WAL mode creates one bounded collector-backed installer after
  service/runtime certification and injects that same instance into Pump and
  anti-entropy, preventing their format policy or install serialization from
  diverging. Live durable installs serialize sidecar creation through marker
  publication. Success retains only the committed digest; definite
  pre-marker rejection removes its candidate; an indeterminate marker outcome
  or publication panic retains the candidate and fail-stops serving. A
  post-commit cleanup error is logged without turning a committed install into
  an apparent rejection.
- v1 materialises the full snapshot in memory. Bootstrap is a bounded,
  one-peer-at-a-time operation, so the O(N+E) overhead is acceptable.
  Cursor-based / chunked snapshotting is a follow-up once the bootstrap
  path is exercised at scale (tracked alongside #190).
  Real Connect/h2c tests cover two-node durable gap recovery through Pump and
  anti-entropy plus tail resumption. Exhaustive multi-replica partition,
  restart, soak, and receipt-bearing backup acceptance remains a separate
  #1393 follow-up; #1394 owns the backup/restore continuity boundary.
- Delete tombstones committed before the Snapshot cutoff cannot be re-derived
  from the Subscribe tail. Explicit tombstone frames preserve their exact D4
  deadline across bootstrap. Put causal barriers — whether born expired or
  migrated when a live payload becomes non-visible — remain distinct unbounded
  LWW floors, carried by separate marker frames.

Retention and memory:

- A causal barrier has no time-based GC. It remains until an equal/newer
  accepted live Put supersedes it or an equal/newer explicit Delete replaces it
  with a D4-bounded tombstone. Reaping it any other way would permit an
  indefinitely delayed older replica write to resurrect data. Memory is
  therefore `O(Vb + Eb)` in retained barrier identities, in addition to the
  live graph and bounded delete tombstones. Prefix Delete cannot perform the
  transition for barrier-only identities because those identities are absent
  from the live prefix indexes.
- `LANTERN_MAX_VERTICES` and `LANTERN_MAX_EDGES` remain conservative soft
  admission caps over live identities plus matching Put barriers (a live
  additive edge coexisting with a barrier can be counted twice). This preserves
  the #1178 defense against unique born-expired Put churn. Retained causal state
  additionally has its own exact identity-union budgets:
  `LANTERN_MAX_VERTEX_CAUSAL_ENTRIES` and
  `LANTERN_MAX_EDGE_CAUSAL_ENTRIES` (`0` = unlimited). One key/pair consumes
  exactly one causal slot while represented by a live Put HLC floor, an
  accepted-expired Put barrier, or a Delete tombstone. Barrier→tombstone and
  tombstone→newer Put transitions reuse that slot atomically. The intentional
  overlap means a Put barrier is charged to both the conservative legacy cap
  and the complete causal budget; moving it to a tombstone frees only the
  legacy live/barrier slot.
- A local Put or Delete that needs a new causal identity beyond its per-kind
  budget fails with `RESOURCE_EXHAUSTED` before graph, causal state, or the
  mutation log changes. A write replacing an already-accounted identity still
  proceeds even when remote convergence previously took the replica over its
  local limit. Replication apply and backup restore both bypass admission.
  Non-zero-HLC replicated causal state is accounted and can expose
  `over_limit`; zero-HLC backup restore creates live state without a causal
  floor and therefore affects live/legacy capacity rather than this budget.
- The causal gauges/status report current entries, a stable logical byte
  estimate covering causal records, the budget ledger, and deadline index,
  all-time high-water, local rejects, configured limit, over-limit state, and
  the oldest bounded tombstone deadline. They deliberately separate logical
  accounting from Go heap bytes; pair them with `go_memstats_*` and
  `GOMEMLIMIT`.

## 9. Bootstrap flow

```
new pod boots
  │
  ├── load LANTERN_PEERS (D6) or resolve LANTERN_PEER_DNS_NAME (#190)
  │
  ├── for each peer P (in parallel; first to respond wins):
  │     stream = P.Snapshot(SnapshotRequest{})
  │     header  = stream.Recv()      // per-origin + responder-local cutoffs
  │     mark local search index INCOMPLETE
  │     apply  body frames → local cache
  │     footer = last frame          // assert counts match
  │     rebuild exact local search index; only then mark HEALTHY
  │
  ├── for each peer P:
  │     compare PeerStatus.search_config_fingerprint
  │     go pump(P)                  // Subscribe resumes at
  │                                  // origin cutoffs + 1 and this
  │                                  // responder's local cutoff + 1
  │
  └── /healthz/ready flips SERVING when:
        - lag(P) < LANTERN_MAX_REPLICATION_LAG and observed peer search
          fingerprints match, OR
        - single-instance mode (static discovery and LANTERN_PEERS empty)
```

Target steady-state flush latency: **< 100 ms** intra-DC at 1k mut/s.

### 9.1 Peer discovery (#190)

The pump resolves its peer set via `LANTERN_PEER_DISCOVERY`:

| Mode | Env vars consumed | Behaviour |
|---|---|---|
| `static` (default) | `LANTERN_PEERS` (CSV `host:port,host:port`) | Resolved once at startup. Empty list → single-instance mode. |
| `dns` | `LANTERN_PEER_DNS_NAME`, `LANTERN_PEER_DEFAULT_PORT` (default `50051`), `LANTERN_PEER_DISCOVERY_INTERVAL_MS` (default `10000`) | Periodic `net.Resolver.LookupHost` against `LANTERN_PEER_DNS_NAME`. Every A/AAAA record except the local node's interface IPs is treated as a peer. Re-poll on every interval; reconcile via add/cancel against the active per-peer goroutine set. |

DNS mode is the canonical multi-instance path: it works against k8s
headless Services (`lantern-headless.<ns>.svc.cluster.local`), Docker
Compose service names (Compose's embedded DNS returns one A per
replica), and Nomad+Consul DNS. Self-filter uses
`net.InterfaceAddrs()` for non-loopback IPs; the pump's existing
HLC-NodeID self-echo guard (§5) remains as defence-in-depth.

A transient resolution error logs at `WARN` and preserves the
previously-active peer set — established subscriptions are NOT torn
down on a flapping DNS resolver.

**Manual verification recipe (k8s headless Service).**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: lantern-headless
spec:
  clusterIP: None                      # headless: A records = pod IPs
  selector: { app: lantern }
  ports: [{ name: grpc, port: 50051, targetPort: 50051 }]
---
# StatefulSet pods set env:
#   LANTERN_PEER_DISCOVERY=dns
#   LANTERN_PEER_DNS_NAME=lantern-headless.default.svc.cluster.local
#   LANTERN_PEER_DEFAULT_PORT=50051
```

Scale the StatefulSet up/down and observe
`lantern_peer_connected{peer=...}` add/remove series within one
discovery interval.

**Manual verification recipe (Docker Compose).**

```yaml
services:
  lantern:
    image: lantern:dev
    deploy: { replicas: 3 }
    environment:
      LANTERN_PEER_DISCOVERY: dns
      LANTERN_PEER_DNS_NAME: lantern             # Compose service name
      LANTERN_PEER_DEFAULT_PORT: "50051"
```

Since [#435](https://github.com/anaregdesign/lantern/issues/435) the
canonical compose declares three explicit `lantern-{0,1,2}` services
sharing the `lantern` network alias, so Compose's embedded DNS resolves
that alias to all three replica IPs and the pump picks up new entries
on the next tick. To run with more than three replicas, switch to the
Helm chart.

## 10. Partition & split-brain analysis

Lantern is **AP** in CAP terms. During a partition:

- Each side accepts both reads and writes (no quorum).
- `SearchVertices` remains available but is local/eventual. Corpus membership,
  BM25 statistics, scores, and top-k order may differ until mutation streaming
  or anti-entropy repairs the graph; clients must not interpret a partition-time
  response as a cluster-wide snapshot or compare its numeric score with another
  replica. Cursor sessions and signing keys are endpoint-local; failover must
  discard a continuation and restart from page one.
- For an edge identity with an Add-only history, `AddEdge*` writes on both
  sides combine by G-Set union when the partition heals. No contribution is
  lost.
- For Put-only LWW/barrier histories, `Put*` writes converge to the higher-HLC
  outcome. The losing side's live value or accepted-expired floor is silently
  superseded — this is intentional LWW semantics.
- Put/Delete LWW histories converge while the Delete tombstone is retained. If
  `Delete` HLC is newer, an older remote Put is dropped after heal. If the
  partition lasts **longer than `tombstone_ttl` (D4, default 1 year / 8760h)**,
  the tombstone may GC before the other side learns about it, allowing a stale
  value to resurrect. Operators must keep partition duration below the
  tombstone TTL or extend D4.
- Mixed `PutEdge`/`AddEdge` and `DeleteEdge`/`AddEdge` histories converge after
  the same mutation set is delivered: the greatest reset floor wins, and only
  strictly later Add rows survive. A Delete still requires the D4 retention
  bound above. Each origin's sequence is published in order so Snapshot and
  Subscribe expose one contiguous committed prefix.

The [HA runbook](ha-runbook.md) describes detection (`lantern_replication_lag_seq` and
`lantern_anti_entropy_gaps_found_total`) and recovery (forced re-snapshot).

## 11. Failure modes

| Failure | Detection | Recovery |
|---|---|---|
| Single pod crash | k8s probe / Compose healthcheck | k8s/Compose restarts pod → bootstraps from peers. |
| Pod falls behind > buffer | `Subscribe` returns `FailedPrecondition` (reason `gapped`) | Pump auto re-snapshots and resumes. |
| Search config differs across replicas | `lantern_search_config_match{peer}=0`, mismatch counter/log, readiness `NOT_SERVING` | Make every search-affecting `LANTERN_SEARCH_*` value homogeneous, then wait for the next pump/anti-entropy comparison. |
| All peers unreachable on boot | `Snapshot` fails on every peer | Pod stays `NOT_SERVING`; operator alert on readiness. |
| Total-cluster loss | every replica down | **Accepted data loss** (D1) — bring the cluster back empty, *or* run snapshot backups (`LANTERN_BACKUP_*`, [backup.md](backup.md)) so each node restores its newest dump on boot. |
| NTP skew > 500ms | `lantern_hlc_skew_clamped_total > 0` (planned — #180/#182) | Fix NTP. Mutations from the drifted peer keep applying (their HLC wall is clamped, §5.3); convergence is preserved but the drifted peer's stamps land behind real wall time until it heals. |
| Network partition < tombstone TTL | `lantern_replication_lag_seq` spike | Auto-converges via anti-entropy (#186) when partition heals. |
| Network partition > tombstone TTL | same | Resurrection possible (§10). Manual reconciliation or operator-driven re-snapshot of the winning side. |
| Mixed edge histories have different weights while lag remains | Replication lag and unequal edge weights | Wait for the missing origin prefix; if a D4 Delete floor expired before heal, reconcile from an authoritative snapshot. |

## 12. Deployment-topology suitability matrix

This is the operator-facing decision table. The [HA runbook](ha-runbook.md)
carries the full per-platform instructions; this is the summary.

| Platform | HA mode | Single-instance | Notes |
|---|---|---|---|
| Kubernetes (StatefulSet + headless Service) | ✅ canonical | ✅ | Helm chart in `deploy/helm/lantern/` (#191). |
| Docker Compose (explicit `lantern-N` services + shared DNS alias) | ✅ | ✅ | Example in `deploy/compose/` (#191, #435). Best for local dev / single-host. |
| Nomad + Consul DNS | ✅ | ✅ | User-configured; same `LANTERN_PEER_DISCOVERY=dns` works. |
| Plain VMs / bare metal | ✅ | ✅ | Static `LANTERN_PEERS` CSV or DNS round-robin. |
| Platforms that hide per-instance addresses (autoscaled, request-scoped runtimes) | ❌ HA not supported | ✅ | Instance-level addressing hidden; long-lived peer streams incompatible with the request-scoped lifecycle. Use as a fast in-memory KVS with CDC via `Subscribe`. |

For every "not supported" platform, the **single-instance** deploy is fully
supported: leave `LANTERN_PEERS` empty, the server runs without a pump, the
readiness gate is bypassed, and `Subscribe` still works as a CDC stream for
downstream consumers. Cold-start data loss is expected on these platforms
unless snapshot backups (`LANTERN_BACKUP_*`, [backup.md](backup.md)) or an
external WAL consumer are in place.

## 13. Out of scope (v1)

- Crash persistence / WAL writer (D1 leaves only the hook).
- Cross-DC replication (D3).
- ACL-gated `Subscribe` for external CDC consumers (D2 ships the unified RPC;
  policy is layered later).
- Multi-instance support on platforms that hide per-instance addressing
  (D7 — fundamental platform incompatibility, not a v2 backlog item).

---

## Appendix A — Implementation roadmap

Tracked in [#176–#192](https://github.com/anaregdesign/lantern/issues?q=label%3Aha)
and grouped in dependency order:

| Phase | Issues | Theme |
|---|---|---|
| 1 — Foundations | #176, #177, #178, #179 | HLC + mutation log + proto |
| 2 — Apply semantics | #180, #181, #182, #183 | Subscribe handler, contrib IDs, ApplyMutation, tombstones |
| 3 — Replication | #184, #185, #186, #187 | Snapshot, pump, anti-entropy, metrics |
| 4 — Operability | #188, #189, #190 | Readiness, SDK LB, DNS discovery |
| 5 — Delivery | #191, #192 | Helm + Compose, runbook |
