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
   = {origin: seq + 1 for each (origin, seq) in cutoff_seq_per_origin})`
   so the snapshot and the live tail stitch without gap or overlap. See
   #415 (Reading B) and the wire types in §8.2/§8.3.
4. **Readiness gates traffic.** `/healthz/ready` returns `NOT_SERVING`
   whenever replication lag exceeds `LANTERN_MAX_REPLICATION_LAG`, so the
   load balancer drains the instance. **Single-instance mode** (empty
   `LANTERN_PEERS`) bypasses this gate.
5. **No leader, no Raft, no external storage.** v1 is intentionally
   ephemeral. Single-pod loss recovers from peers. Total-cluster loss is
   accepted data loss.
6. **Rolling update safe.** One pod down → remaining pods serve → new pod
   bootstraps → ready → next.

## 3. Binding decisions (D1–D7)

| # | Decision | Default | Rationale |
|---|---|---|---|
| D1 | Crash persistence | **None for v1.** WAL is a hook only. | Bootstrap from peers covers single-node loss; persistence adds operational surface area we don't yet need. |
| D2 | External CDC | **Same `Subscribe` RPC**, gated by auth/ACL later. Under the leaderless Subscribe contract (#415, Reading B), an external CDC consumer attaches to any **one** replica and observes every committed cluster mutation — failover to a different replica is supported by passing the per-origin watermark in `SubscribeRequest.from_seq_per_origin`. | Internal replication and external CDC are isomorphic; splitting RPCs would duplicate machinery. The per-origin cursor lets consumers spread load across replicas without reimplementing the internal pump's dedup. |
| D3 | WAN replication | **Out of scope for v1**, single DC only. HLC max skew bound = **500 ms**. | Geo replication requires looser skew + read repair; defer until single-DC HA is proven. |
| D4 | Tombstone TTL | **Cluster-wide config, default 24h.** Any `Add*` / `Put*` whose TTL would exceed tombstone TTL is **rejected** with `InvalidArgument`. | Resurrection-proof deletes require tombstones to outlive every live contribution. This is a real backwards-incompatible constraint. |
| D5 | Workload kind (k8s reference impl) | **StatefulSet** (not Deployment). | Stable pod identity simplifies peer discovery; leaves room for an optional WAL PVC later. The *user experience* is Deployment-like; the *resource kind* is `StatefulSet`. |
| D6 | Cluster membership v1 | **Static `LANTERN_PEERS` env var.** v2 adds DNS-based discovery (#190). | Smallest surface that ships. Any DNS-routable platform (k8s headless Service, Compose service name, Nomad, plain DNS A-records) can populate it trivially. |
| D7 | Supported deployment topologies | **Tier A (full HA):** k8s StatefulSet, Nomad, plain VMs, Docker Compose with stable peer hostnames. **Tier B (single-instance, no HA):** any container PaaS — Docker Compose single service, Cloud Run, Azure Container Apps, App Runner. **Not supported:** running multiple Cloud Run / ACA *instances* as a replicated cluster. | Leaderless P2P needs **stable inter-instance addressing** and **long-lived inbound gRPC streams between peers**. Serverless container platforms intentionally hide instance addresses and recycle instances; they fit single-instance deploys (still useful as a fast in-memory KVS) but not the replicated topology. |

## 4. CRDT semantics per RPC

Every write RPC is classified as one of three CRDT shapes. The classification
is **load-bearing**: it determines whether two concurrent writes commute, and
whether re-applying an already-seen mutation is a no-op.

| RPC | CRDT shape | Conflict rule |
|---|---|---|
| `PutVertex(es)` | LWW-Register | Higher HLC wins; same HLC ⇒ higher origin ID wins (deterministic tiebreak). |
| `AddEdge(s)` | G-Set of contributions | Each `(origin, contributionID)` is an element. Re-apply = set-insert ⇒ no-op. Weight = Σ live contributions. |
| `PutEdge(s)` | LWW-Register on `(tail, head)` | Replaces all contributions atomically. Higher HLC wins; same HLC ⇒ higher origin ID wins. |
| `DeleteVertex(es)` | Tombstone (LWW) | A tombstone is itself an entry with HLC. Any `Put*` / `Add*` whose HLC < tombstone HLC is dropped. Tombstone TTL = D4. |
| `DeleteEdge(s)` | Tombstone (LWW) on `(tail, head)` | Same as vertex tombstone. |

Reads (`GetVertex(es)`, `GetEdge(s)`, `Illuminate`) are local-only — they
never block on peers and never read-repair. Read-after-write across nodes is
**eventual**, bounded by the readiness gate (invariant 4) plus the pump's
flush latency target (§9).

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
fires (default wiring: increment `lantern_hlc_skew_clamped_total`). The
remote timestamp is never rejected — replication keeps making progress even
when peers drift, and operators observe the drift through the counter and
are expected to fix NTP. (Earlier drafts of this RFC rejected with
`OutOfRange`; that was changed during #176 implementation because rejecting
risks a cascading replication stall while the clock heals.)

### 5.4 Comparison

`a < b` iff `(a.wallNs, a.logical, a.nodeID) < (b.wallNs, b.logical, b.nodeID)`
lexicographically. `nodeID` is compared bytewise. Two distinct origins thus
yield a strict total order without any extra tiebreak machinery.

## 6. Contribution IDs

Every locally-originated mutation gets a 16-byte contribution ID:

```
contribID = uint64(originID) << 64 | uint64(localSeq)
```

- `originID` is `LANTERN_NODE_ID` parsed as 16 bytes of hex (32 chars, "0x"
  prefix tolerated). Malformed values fall back to a `crypto/rand` 16-byte
  identifier and emit a warning. Origin is stable for the pod's lifetime.
- `localSeq` is a per-origin monotonic counter, 1-indexed at startup.

Properties:

- Globally unique without coordination.
- Deterministic LWW tiebreak when HLCs collide (higher origin wins).
- Suitable as a G-Set element for `AddEdge` contributions, so re-applying a
  mutation already present is a cheap set-insert no-op.

## 7. Mutation log

In-memory ring buffer (`core/mutationlog`), append-only, with a WAL hook
(D1 leaves the hook empty for v1).

```go
type Mutation struct {
    Seq        uint64        // local sequence number (monotonic per node)
    Origin     uint64        // contribID >> 64
    ContribSeq uint64        // contribID & 0xFFFFFFFFFFFFFFFF
    HLC        HLC
    Op         MutationOp    // PUT_VERTEX | PUT_EDGE | ADD_EDGE | DELETE_VERTEX | DELETE_EDGE
    Payload    []byte        // marshaled per-op body
}
```

The log indexes by `Seq` for `Subscribe(from_seq=...)` resume, and tracks the
**high-water mark per origin** (`map[uint64]uint64 // origin -> last contribSeq`)
to detect duplicates from out-of-order Subscribe delivery.

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
  }
}

message Mutation {
  uint64       seq    = 1;
  HLCTimestamp hlc    = 2;
  bytes        origin = 3;
  MutationOp   op     = 4;
}
```

Deviations from the §7 conceptual sketch (recorded as part of #178):

- HLC is **nanoseconds**, not milliseconds (matches `core/hlc.Timestamp`).
- `origin` is a 16-byte `NodeID` (mirrors `HLCTimestamp.node_id`), not a
  packed `uint64`. The split between `contrib_seq` and `payload` does not
  appear on the wire — per-origin `(origin, seq)` already plays the
  contribution-ID role and the request payload is carried directly by the
  `MutationOp` oneof.
- `MutationOp` reuses the existing write-RPC request messages so the
  server handlers can be invoked verbatim when applying replicated
  mutations.

message HLC {
  uint64 wall_millis = 1;
  uint32 logical = 2;
}
```

> The shipped HLC type is `HLCTimestamp` with `int64 wall_ns` (not millis)
> and an explicit `bytes node_id`; the §7/§8.1 sketch above is retained
> as historical context. See §8.1 for the wire shape that ships.

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
(via `logMutation`); replicas Y and Z then receive it through the peer
pump and append it to their own local logs via
`LanternService.ApplyMutation` (which calls `Log.Append` only after a
per-origin watermark CAS to avoid double-Append from fan-out triangles).

Consequence:

- A consumer that picks **any one** replica and calls `Subscribe` with
  an empty cursor observes the entire cluster's mutation stream,
  ordered by per-origin local seq inside each origin and HLC-orderable
  across origins. There is no need to subscribe to every replica and
  dedupe by `(origin, seq)` on the client side; the server already
  does that work via the watermark CAS.
- A consumer that fails over from replica X to replica Y resumes by
  sending the highest seq it has already observed FOR EACH origin in
  `from_seq_per_origin`. The new replica delivers only entries with
  `mu.Seq >= cursor[origin]` for origins present in the cursor;
  origins absent from the cursor are delivered from the oldest
  retained entry (so a freshly-joined origin is picked up
  automatically).
- `Mutation.Seq` is the originating writer's local seq, NOT the
  forwarding replica's local seq. This is preserved end-to-end: the
  Subscribe relay never overwrites `mu.Seq`, and the originating
  writer stamps it atomically at `Log.Append` time via the
  `SeqStamper` callback (see `core/mutationlog`).

The internal peer pump uses the same RPC but with an empty cursor and
relies on `ApplyMutation`'s watermark CAS to dedup duplicate hops; it
still performs input-side self-echo suppression (`Mutation.Origin ==
local NodeID → drop`) as defence-in-depth.

Back-pressure: server terminates the stream with `FAILED_PRECONDITION`
(`gapped`) if either (a) the ring has been truncated below the
oldest seq the cursor could match, or (b) the consumer's send buffer
overflows. In both cases the consumer must re-bootstrap via
`Snapshot` and resume `Subscribe` with the
`cutoff_seq_per_origin` returned by `SnapshotHeader`.

Handler implementation notes (issue #180):

- The handler is `service.LanternReplicationService` in
  `server/service/replication.go`; it is wired in `server/cmd/wire.go`
  alongside `LanternService` and shares the same `*mutationlog.Log` as
  the write path.
- The handler maps `mutationlog.ErrGapped` to `codes.FailedPrecondition`
  with the reason `"gapped"`, both at subscribe time (initial check) and
  when the in-flight channel is closed by the log's slow-subscriber
  eviction. This matches the wire contract above.
- The handler shallow-copies the buffered `*pb.Mutation` into the
  outbound `SubscribeResponse.Mutation` while overwriting `Seq` with
  `entry.Seq`. The write path stores `Seq=0` on the buffered Mutation;
  only Subscribe stamps it. Peer-pump apply (`#182`) must therefore
  read `Seq` from the streamed envelope.
- Health: a separate service name `graph.v1.LanternReplicationService`
  is registered with the grpc health server and flipped to
  `NOT_SERVING` on shutdown alongside `LanternService`.
- Metrics: `lantern_subscribe_active_streams` (gauge) and
  `lantern_subscribe_dropped_total{reason}` (counter; `reason ∈ {gapped,
  send_failed}`) are pre-rendered in `server/metrics/metrics.go`.

### 8.3 Snapshot (server streaming)

```proto
rpc Snapshot(SnapshotRequest) returns (stream SnapshotResponse);

message SnapshotResponse {
  oneof entry {
    SnapshotHeader header = 1;   // first frame: cutoff_seq_per_origin + cutoff_hlc
    SnapshotVertex vertex = 2;   // body: live vertex with stored HLC
    SnapshotEdge   edge   = 3;   // body: edge with per-contribution payloads
    SnapshotFooter footer = 4;   // last frame: streamed vertex/edge counts
  }
}

message SnapshotHeader {
  // Per-origin watermark at snapshot-open time. Same keying convention
  // as SubscribeRequest.from_seq_per_origin (§8.2): 32-char hex of the
  // 16-byte HLC NodeID → highest origin-anchored seq the server had
  // applied from that origin. Empty when the cluster is cold.
  map<string, uint64> cutoff_seq_per_origin = 1;
  HLCTimestamp cutoff_hlc = 2;
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
}
```

Framing contract:

- The **header** is always the first frame. `cutoff_seq_per_origin` is
  the primary's per-origin watermark (every (origin, seq) the primary
  had already applied at snapshot-open time, with seq = the
  origin-anchored local seq stamped by the originating writer's
  `logMutation`). `cutoff_hlc` is the primary's `clock.Now()` at
  snapshot-open time. The consumer Subscribes with
  `from_seq_per_origin = {origin: seq + 1 for each (origin, seq) in
  cutoff_seq_per_origin}` to stitch the snapshot and the live tail.
  An empty map means the primary has not yet applied any origin
  (cold cluster); the consumer should pass an empty Subscribe cursor.
- The **footer** is always the last frame. It reports the actually
  streamed vertex and edge counts so consumers can detect truncation
  without parsing a sentinel-typed body frame.
- The snapshot deliberately preserves **per-contribution decomposition**:
  each `SnapshotEdge` carries its full list of live `SnapshotEdgeContribution`
  rows rather than a pre-summed weight. The consumer replays each
  contribution via `AddEdgeWithExpirationContribHLC`, and the receiver's
  `ContribID` dedup makes the snapshot-then-Subscribe-tail handoff
  idempotent: any contribution that also appears in the replayed tail is
  detected and dropped at apply time.

Implementation notes:

- The handler is `LanternReplicationService.Snapshot` in
  `server/service/replication.go`. It holds references to the same
  `Backend` and `*hlc.Clock` the write path uses; both are wired in
  `server/cmd/wire.go`. `Snapshot` materialises vertices and edges via
  `Backend.SnapshotVertices()` / `Backend.SnapshotEdges()` (which lock
  the GraphCache once each) and streams them frame-by-frame, honouring
  `stream.Context()` cancellation between sends.
- v1 materialises the full snapshot in memory. Bootstrap is a bounded,
  one-peer-at-a-time operation, so the O(N+E) overhead is acceptable.
  Cursor-based / chunked snapshotting is a follow-up once the bootstrap
  path is exercised at scale (tracked alongside #190).
- Tombstones are NOT carried as standalone frames. The cache snapshot
  returns only live state; the receiver re-derives tombstones from the
  Subscribe tail beginning at the per-origin cutoff + 1.

## 9. Bootstrap flow

```
new pod boots
  │
  ├── load LANTERN_PEERS (D6) or resolve LANTERN_PEER_DNS_NAME (#190)
  │
  ├── for each peer P (in parallel; first to respond wins):
  │     stream = P.Snapshot(SnapshotRequest{})
  │     header  = stream.Recv()      // {cutoff_seq_per_origin, cutoff_hlc}
  │     apply  body frames → local cache
  │     footer = last frame          // assert counts match
  │
  ├── for each peer P:
  │     go pump(P)                  // Subscribe sends an empty cursor;
  │                                  // the local watermark CAS dedups
  │                                  // anything the snapshot already covered
  │
  └── /healthz/ready flips SERVING when:
        - lag(P) < LANTERN_MAX_REPLICATION_LAG for all peers, OR
        - single-instance mode (LANTERN_PEERS empty)
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
`lantern_replication_peer_up{peer=...}` add/remove series within one
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

`docker compose scale lantern=N` triggers Compose's embedded DNS to
add/remove A records; the pump converges on the next tick.

## 10. Partition & split-brain analysis

Lantern is **AP** in CAP terms. During a partition:

- Each side accepts both reads and writes (no quorum).
- `Add*` writes on both sides combine cleanly when the partition heals
  (G-Set union). No data is lost.
- `Put*` writes on both sides converge to the higher-HLC value. The losing
  side's value is silently overwritten — this is intentional LWW semantics.
- `Delete*` followed by `Put*` on the **other** side of the partition is the
  only true hazard. If `Delete` HLC > later `Put` HLC on the other side, the
  `Put` will be dropped after heal. If the partition lasts **longer than
  `tombstone_ttl` (D4, default 24h)**, the tombstone may GC before the other
  side learns about it, resurrecting the deleted entry. Operators must keep
  partition duration < tombstone TTL or extend D4.

The [HA runbook](ha-runbook.md) describes detection (`lantern_replication_lag_seq` and
`lantern_anti_entropy_gaps_found_total`) and recovery (forced re-snapshot).

## 11. Failure modes

| Failure | Detection | Recovery |
|---|---|---|
| Single pod crash | k8s probe / Compose healthcheck | k8s/Compose restarts pod → bootstraps from peers. |
| Pod falls behind > buffer | `Subscribe` returns `OutOfRange` | Pump auto re-snapshots and resumes. |
| All peers unreachable on boot | `Snapshot` fails on every peer | Pod stays `NOT_SERVING`; operator alert on readiness. |
| Total-cluster loss | every replica down | **Accepted data loss** (D1). Bring the cluster back empty. |
| NTP skew > 500ms | `lantern_hlc_skew_clamped_total > 0` | Fix NTP. Mutations from the drifted peer keep applying (their HLC wall is clamped, §5.3); convergence is preserved but the drifted peer's stamps land behind real wall time until it heals. |
| Network partition < tombstone TTL | `lantern_replication_lag_seq` spike | Auto-converges via anti-entropy (#186) when partition heals. |
| Network partition > tombstone TTL | same | Resurrection possible (§10). Manual reconciliation or operator-driven re-snapshot of the winning side. |

## 12. Deployment-topology suitability matrix

This is the operator-facing decision table. The [HA runbook](ha-runbook.md)
carries the full per-platform instructions; this is the summary.

| Platform | HA mode | Single-instance | Notes |
|---|---|---|---|
| Kubernetes (StatefulSet + headless Service) | ✅ canonical | ✅ | Helm chart in `deploy/helm/lantern/` (#191). |
| Docker Compose (`deploy.replicas` + service DNS) | ✅ | ✅ | Example in `deploy/compose/` (#191). Best for local dev / single-host. |
| Nomad + Consul DNS | ✅ | ✅ | User-configured; same `LANTERN_PEER_DISCOVERY=dns` works. |
| Plain VMs / bare metal | ✅ | ✅ | Static `LANTERN_PEERS` CSV or DNS round-robin. |
| Google Cloud Run | ❌ HA not supported | ✅ | Instance-level addressing hidden; long-lived peer streams incompatible with the request-scoped lifecycle. Use as a fast in-memory KVS with CDC via `Subscribe`. |
| Azure Container Apps | ❌ HA not supported | ✅ | Same reason as Cloud Run. Single-revision, single-replica works fine. |
| AWS App Runner / Fly Machines (autoscale) | ❌ HA not supported | ✅ | Same reason. |

For every "not supported" platform, the **single-instance** deploy is fully
supported: leave `LANTERN_PEERS` empty, the server runs without a pump, the
readiness gate is bypassed, and `Subscribe` still works as a CDC stream for
downstream consumers. Cold-start data loss is expected on these platforms
unless an external WAL consumer is in place.

## 13. Out of scope (v1)

- Crash persistence / WAL writer (D1 leaves only the hook).
- Cross-DC replication (D3).
- ACL-gated `Subscribe` for external CDC consumers (D2 ships the unified RPC;
  policy is layered later).
- Multi-instance Cloud Run / ACA support (D7 — fundamental platform
  incompatibility, not a v2 backlog item).

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
