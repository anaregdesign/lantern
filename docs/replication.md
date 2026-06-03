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
Client (sdks/go: dns:///lantern + grpc round_robin + retry)
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
   to seed its in-memory state, then opens `Subscribe(from_seq = cutoff + 1)`
   to tail new mutations.
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
| D2 | External CDC | **Same `Subscribe` RPC**, gated by auth/ACL later. | Internal replication and external CDC are isomorphic; splitting RPCs would duplicate machinery. |
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

HLC = `(wallMillis uint64, logical uint32)`.

### 5.1 Local tick (called on every locally-originated mutation)

```
now    := time.Now().UnixMilli()
last   := lastHLC
if now > last.wallMillis:
    lastHLC = HLC{wallMillis: now, logical: 0}
else:
    lastHLC = HLC{wallMillis: last.wallMillis, logical: last.logical + 1}
return lastHLC
```

### 5.2 Update on receive (`recv` carries a remote HLC `r`)

```
now := time.Now().UnixMilli()
max := max(now, last.wallMillis, r.wallMillis)
switch max:
case last.wallMillis where last.wallMillis == r.wallMillis:
    lastHLC = HLC{wallMillis: max, logical: max(last.logical, r.logical) + 1}
case last.wallMillis:
    lastHLC = HLC{wallMillis: max, logical: last.logical + 1}
case r.wallMillis:
    lastHLC = HLC{wallMillis: max, logical: r.logical + 1}
default: // now is strictly greater
    lastHLC = HLC{wallMillis: max, logical: 0}
```

### 5.3 Skew clamp

If `|r.wallMillis - now| > 500ms` (D3), the receive is **rejected** with
`OutOfRange`. The operator sees `lantern_hlc_skew_rejected_total` increment
and is expected to fix NTP.

### 5.4 Comparison

`a < b` iff `(a.wallMillis, a.logical) < (b.wallMillis, b.logical)`
lexicographically. Strict total order across the cluster when combined with
the origin tiebreak in §4.

## 6. Contribution IDs

Every locally-originated mutation gets a 16-byte contribution ID:

```
contribID = uint64(originID) << 64 | uint64(localSeq)
```

- `originID` is `LANTERN_NODE_ID` parsed as the low 64 bits of a UUIDv4
  (stable for the pod's lifetime).
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

Buffer size is `LANTERN_REPLICATION_BUFFER` (default 1024). Overflow drops
**oldest** entries; consumers that fall behind that far are forced to
re-bootstrap via `Snapshot`.

## 8. Wire protocol

### 8.1 Mutation message

See [proto/graph/v1/graph.proto](../proto/graph/v1/graph.proto) for the
generated form. Conceptual shape:

```proto
message Mutation {
  uint64 seq = 1;
  uint64 origin = 2;
  uint64 contrib_seq = 3;
  HLC hlc = 4;
  MutationOp op = 5;
  bytes payload = 6;
}

message HLC {
  uint64 wall_millis = 1;
  uint32 logical = 2;
}
```

### 8.2 Subscribe (server streaming)

```proto
rpc Subscribe(SubscribeRequest) returns (stream Mutation);

message SubscribeRequest {
  uint64 from_seq = 1;     // 0 = "from the live tail"; non-zero = resume
  uint64 origin = 2;       // caller's origin, so the server can skip self-echo
}
```

Back-pressure: server drops the stream with `ResourceExhausted` if its send
buffer fills. The pump (§9) reconnects with exponential backoff and may
re-bootstrap via `Snapshot` if the requested `from_seq` is no longer in the
ring buffer (`OutOfRange`).

### 8.3 Snapshot (server streaming)

```proto
rpc Snapshot(SnapshotRequest) returns (stream SnapshotChunk);

message SnapshotChunk {
  oneof chunk {
    Vertex vertex = 1;
    EdgeContribution edge = 2;
    Tombstone tombstone = 3;
    Cutoff cutoff = 4;     // terminal frame: { last_seq, last_hlc }
  }
}
```

The terminal `Cutoff` frame tells the consumer the exact `(seq, hlc)` to
resume `Subscribe` from. Without it, the consumer cannot stitch the snapshot
stream and the live tail without either missing or double-applying mutations.

## 9. Bootstrap flow

```
new pod boots
  │
  ├── load LANTERN_PEERS (D6) or resolve LANTERN_PEER_DNS_NAME (#190)
  │
  ├── for each peer P (in parallel; first to respond wins):
  │     stream = P.Snapshot(SnapshotRequest{})
  │     apply chunks → local cache
  │     cutoff = stream.Cutoff
  │
  ├── for each peer P:
  │     go pump(P, from_seq = cutoff.seq + 1)
  │
  └── /healthz/ready flips SERVING when:
        - lag(P) < LANTERN_MAX_REPLICATION_LAG for all peers, OR
        - single-instance mode (LANTERN_PEERS empty)
```

Target steady-state flush latency: **< 100 ms** intra-DC at 1k mut/s.

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

`#192` runbook describes detection (`lantern_replication_lag_seq` and
`lantern_anti_entropy_gaps_found_total`) and recovery (forced re-snapshot).

## 11. Failure modes

| Failure | Detection | Recovery |
|---|---|---|
| Single pod crash | k8s probe / Compose healthcheck | k8s/Compose restarts pod → bootstraps from peers. |
| Pod falls behind > buffer | `Subscribe` returns `OutOfRange` | Pump auto re-snapshots and resumes. |
| All peers unreachable on boot | `Snapshot` fails on every peer | Pod stays `NOT_SERVING`; operator alert on readiness. |
| Total-cluster loss | every replica down | **Accepted data loss** (D1). Bring the cluster back empty. |
| NTP skew > 500ms | `lantern_hlc_skew_rejected_total > 0` | Fix NTP; rejected writes must be retried client-side. |
| Network partition < tombstone TTL | `lantern_replication_lag_seq` spike | Auto-converges via anti-entropy (#186) when partition heals. |
| Network partition > tombstone TTL | same | Resurrection possible (§10). Manual reconciliation or operator-driven re-snapshot of the winning side. |

## 12. Deployment-topology suitability matrix

This is the operator-facing decision table. The runbook (#192) carries the
full per-platform instructions; this is the summary.

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
