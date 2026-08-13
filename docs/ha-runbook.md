# Lantern HA runbook

> Status: **Operator-facing runbook**, paired with the design spec
> [`docs/replication.md`](replication.md). Whenever this document
> disagrees with the RFC, the RFC wins — file an issue.
> Tracking issue: [#192](https://github.com/anaregdesign/lantern/issues/192).

This runbook tells operators how to run a Lantern cluster in
production: which signals to watch, how to react to partitions, how
to upgrade, and which deployment topologies actually work.

It assumes you have already read the [README](../README.md) quick
start and have at least skimmed §1–§5 of the
[replication RFC](replication.md).

---

## 1. Topology decision matrix

Start here. Pick a row, then jump to the corresponding section.

| Platform | HA (multi-instance) | Single-instance | Section |
|---|---|---|---|
| Kubernetes (StatefulSet + headless Service) | ✅ canonical | ✅ | [§3.1](#31-kubernetes) |
| Docker Compose (explicit `lantern-N` services + DNS alias) | ✅ | ✅ | [§3.2](#32-docker-compose) |
| HashiCorp Nomad + Consul DNS | ✅ user-configured | ✅ | [§3.3](#33-nomad) |
| Plain VMs / bare metal | ✅ static or DNS | ✅ | [§3.4](#34-plain-vms) |

**Why some platforms can't do HA.** Lantern's leaderless P2P needs
(a) stable per-instance addressing for peer discovery and (b)
long-lived inbound gRPC streams between every pair of instances.
Platforms that hide instance addresses behind a load balancer and
recycle instances on the request lifecycle cannot satisfy these, so
only single-instance mode works there — still genuinely useful as a
fast in-memory KVS with CDC via `Subscribe`. See RFC
[D7](replication.md#3-binding-decisions-d1d7).

---

## 2. The two operating modes

Lantern has exactly two modes; everything else is a knob.

### 2.1 Single-instance mode (no HA)

Triggered when `LANTERN_PEERS` is empty **and** `LANTERN_PEER_DISCOVERY`
is unset (or `static`). In this mode:

- No peer pump is started.
- `/healthz/ready` is **not** gated on replication lag (because there
  is no peer to lag against). It returns `SERVING` as soon as the
  gRPC listener is up.
- `Subscribe` still works — external CDC consumers see every mutation
  in real time.
- Cold start = empty cache **unless snapshot backups are configured**
  (`LANTERN_BACKUP_*`, see [backup.md](backup.md)): the replication layer
  itself has no persistence ([RFC D1](replication.md#3-binding-decisions-d1d7)),
  but the backup engine restores the newest dump on boot.

This is the supported mode for every "❌ HA not supported" row in §1.

### 2.2 HA mode (leaderless replication)

Triggered when `LANTERN_PEER_DISCOVERY=dns` **or** when
`LANTERN_PEERS` is a non-empty CSV. In this mode:

- The pump opens one `Subscribe` stream per peer.
- Every local mutation fans out asynchronously to every peer.
- `/healthz/ready` returns `NOT_SERVING` (503) while replication lag
  for any peer exceeds `LANTERN_MAX_REPLICATION_LAG`, so load
  balancers drain the pod.
- Bootstrap = `Snapshot` against the first responding peer, then
  tail `Subscribe(from_seq = cutoff+1)`.
  See RFC §[9](replication.md#9-bootstrap-flow).

Use one of the §3 topologies to deliver this mode.

---

## 3. Per-platform deployment

### 3.1 Kubernetes

**Recommended via the Helm chart at
[`deploy/helm/lantern/`](../deploy/helm/lantern/).** The chart ships a
3-replica StatefulSet, a headless `Service` (peer-discovery DNS),
a `ClusterIP` `Service` (client traffic), a `PodDisruptionBudget`
(`minAvailable: 2`), and an optional Prometheus `ServiceMonitor`.

Defaults are HA-mode. Override `replication.peers` (CSV) and set
`replication.discovery.mode: static` for static peer lists, or just
leave the defaults to use DNS discovery against the headless Service.

```sh
helm install lantern deploy/helm/lantern
kubectl get statefulset,svc,pdb -l app.kubernetes.io/instance=lantern
```

**Why two Services?** The headless Service (`*-headless`,
`clusterIP: None`, `publishNotReadyAddresses: true`) is what the
DNS pump resolves — its A records include bootstrapping pod IPs so a
simultaneous cold start cannot deadlock behind readiness. The
`ClusterIP` Service is what clients hit; they get a stable VIP and
kube-proxy spreads load across the backing pods. Splitting them means
client traffic never accidentally targets a pod that's still
bootstrapping.

**Single-instance on k8s.** Set `replicaCount: 1` and
`replication.discovery.mode: static` with empty `replication.peers`.
The chart strips the peer env entirely; you get single-instance
mode (§2.1) with k8s scheduling and probes intact.

**Probes.** Startup, liveness, and readiness all hit the metrics port
(9090). Startup and liveness use `/healthz`; readiness uses `/readyz`.
The startup probe waits 60 seconds before its first check, then allows 36
failures at five-second intervals, giving restore-on-start about four minutes
before Kubernetes restarts the container. Liveness and readiness do not run
until startup succeeds. Readiness then checks every five seconds; **brief 503s
on `/readyz` while anti-entropy establishes the peer baseline are expected**
and remove the pod from the client Service without restarting it.

**Probe-port gotcha.** Probes are on the metrics port (9090), not the
Lantern RPC port (6380). The RPC port serves the `grpc.health.v1`
surface via `connectrpc.com/grpchealth` (reachable by `grpc-health-probe`
and any Connect / gRPC / gRPC-Web client); the metrics port serves
HTTP `/healthz` and `/readyz`. Use the HTTP probes — they're cheaper
and don't open an HTTP/2 stream on every check.

**Upgrade procedure.** See [§7](#7-rolling-upgrade-procedure).

### 3.2 Docker Compose

**Example at [`deploy/compose/`](../deploy/compose/).** Since
[#435](https://github.com/anaregdesign/lantern/issues/435) the compose
file declares three explicit `lantern-{0,1,2}` services with pinned
host ports (`6380`, `6381`, `6382`); all three share the `lantern`
network alias so `LANTERN_PEER_DNS_NAME=lantern` round-robins across
them via Compose's embedded DNS.

```sh
cd deploy/compose
docker compose up -d
```

Scaling past three replicas via `docker compose --scale` no longer
applies — the canonical compose is a fixed 3-node topology. For larger
clusters, use the [Helm chart](../deploy/helm/lantern/).

There is **no nginx / haproxy sidecar**. OSS nginx's `resolve` keyword
on `upstream server` is an nginx-plus feature, so a generic Compose
recipe would need stream-block trickery to re-resolve DNS. Two pragmatic
answers:

1. **Reverse proxy / sidecar fan-out.** Drop in Caddy, Traefik, or
   envoy with a `lantern` DNS-resolved upstream pool. Each of these
   re-resolves DNS A records on a cadence (Caddy: `dynamic dns`;
   Traefik: Docker provider; envoy: `STRICT_DNS` cluster), so adding or
   removing replicas is picked up without a config push. The proxy
   speaks h2c upstream → Lantern's single port, terminating TLS at the
   edge.
2. **DNS round-robin from the client.** Point the SDK at a host name
   that resolves to all backends; the OS resolver hands the addresses
   to `net/http`'s `http2.Transport` in shuffled order, and any backend
   returning a transient error triggers a fresh dial against the next
   IP. Suitable for steady-state read traffic from a single client.
3. **SDK static-endpoint failover.** When the replica set is **fixed and
   known**, the Go SDK can fail over client-side with no proxy or DNS
   plumbing via `NewLanternFailover([]string{...})` (#592). It sticks to
   one node and rotates to the next only when a node is unreachable
   (`ErrUnavailable`). It performs **no** dynamic discovery, so it is the
   wrong tool for churning endpoints — use option 1 or 2 there.

```go
// Reverse-proxy fan-out: point one URL at the proxy, not at the pool.
c, err := lantern.NewLantern("http://lantern-proxy.svc:6380")

// SDK static-endpoint failover: a fixed, known replica set, no proxy.
c, err = lantern.NewLanternFailover([]string{
    "http://lantern-0.svc:6380",
    "http://lantern-1.svc:6380",
    "http://lantern-2.svc:6380",
})
```

#### Client-connectivity options at a glance

| Option | Endpoints | Discovery | Extra infra | Best for |
| --- | --- | --- | --- | --- |
| Reverse proxy / sidecar | Churning | Proxy re-resolves DNS | Proxy | Autoscaled / churning replica sets, edge TLS |
| DNS round-robin | Churning | OS resolver | DNS records | Single-client steady-state reads |
| SDK `NewLanternFailover` | **Fixed/known** | **None** (static set) | None | Small pinned replica sets, no proxy/DNS |
| SDK `NewLantern` (single URL) | One | None | None (front with proxy for HA) | Single endpoint or a proxy VIP |

The pre-#367 `NewLanternWithEndpoints([]string{...})` constructor that
did SDK-side round-robin LB has been removed
([#367](https://github.com/anaregdesign/lantern/issues/367)); the
Connect-only SDK takes a single base URL per `NewLantern`. The newer
`NewLanternFailover` (#592) is **not** a revival of that round-robin LB —
it is sticky-current failover over a *static* endpoint set, complementing
the patterns above rather than replacing them. For Swarm mode, use
`tasks.lantern` instead of `lantern` for the discovery DNS name on the
server side; clients still target a single proxy URL (or a fixed failover
list).

**Single-instance on Compose.** `docker compose up -d lantern-0 admin
prometheus` plus omitting `LANTERN_PEER_DISCOVERY` (override the env
locally) puts the service in single-instance mode (§2.1). Useful for
local dev against the same compose file you'd run in HA.

### 3.3 Nomad

Use a `service { check { type = "grpc" } }` block against port 6380,
register the service in Consul, and point Lantern at it:

```hcl
env {
  LANTERN_PEER_DISCOVERY        = "dns"
  LANTERN_PEER_DNS_NAME         = "lantern.service.consul"
  LANTERN_PEER_DEFAULT_PORT     = "6380"
  LANTERN_PEER_DISCOVERY_INTERVAL_MS = "10000"
}
```

Consul DNS returns one A record per healthy instance; the pump
converges on the next discovery interval. Scaling = `nomad job scale`.
We don't ship a sample job spec; the env contract above is the entire
integration.

### 3.4 Plain VMs

Two options, depending on whether you have stable DNS:

**Static peer list (easiest):**

```sh
export LANTERN_PEERS=host-a:6380,host-b:6380,host-c:6380
./lantern-server
```

Empty `LANTERN_PEERS` = single-instance mode. Editing the list
requires restarting the affected pods, but rolling restarts converge
cleanly via the bootstrap flow.

**DNS round-robin (e.g., Route53 multi-value, internal coredns):**

```sh
export LANTERN_PEER_DISCOVERY=dns
export LANTERN_PEER_DNS_NAME=lantern.internal.example.com
export LANTERN_PEER_DEFAULT_PORT=6380
export LANTERN_PEER_DISCOVERY_INTERVAL_MS=10000
./lantern-server
```

The pump re-resolves the DNS name every interval and reconciles its
peer set.

---

## 4. What to watch (signals)

All metrics are exposed at `/metrics` on the metrics address
(`LANTERN_METRICS_ADDR`, default `:9090`). The names below are the
exact Prometheus series.

### 4.1 Cluster health

| Metric | Type | What it tells you |
|---|---|---|
| `lantern_replication_lag_seq{peer,origin}` | gauge | How many mutation log entries this pod is behind `peer` for that `origin`. **The single most important HA signal.** |
| `lantern_replication_applied_total{origin}` | counter | Remote mutations from `origin` (the originating writer node) that landed in the local cache. `rate()` ≈ steady-state replication throughput per origin. |
| `lantern_replication_dropped_total{peer,reason}` | counter | Replication frames or peer interactions dropped. `reason` is `self_echo` / `subscribe_failed` / `snapshot_failed` / `dial_failed` / `peerstatus_failed` / `catchup_failed` / `discovery_failed` / `clean` / `ctx_cancel`. A persistent non-zero rate (excluding `self_echo` / `clean`) signals an unreachable or stuck peer. |
| `lantern_anti_entropy_cycles_total` | counter | Anti-entropy ticks. Should advance at `LANTERN_ANTI_ENTROPY_INTERVAL_MS` cadence. |
| `lantern_anti_entropy_gaps_found_total{peer,origin}` | counter | Non-zero = real divergence detected and being repaired. Spiking after a partition heal = expected. Persistently incrementing in steady state = open a bug. |
| `lantern_search_config_match{peer}` | gauge | `1` means the peer's search capability fingerprint exactly matches this pod; `0` means missing/mismatched and keeps readiness `NOT_SERVING`. |
| `lantern_search_config_mismatch_total{peer}` | counter | Pump/anti-entropy observations of missing or mismatched search config. A rising counter means the topology is still heterogeneous. |
| `lantern_mutation_log_entries_total` | counter | Total mutations ever logged on this pod. |
| `lantern_mutation_log_capacity` | gauge | Ring buffer capacity. If sustained `rate(mutation_log_entries) × subscribe_lag_seconds > capacity`, slow peers will fall off and re-snapshot. |
| `lantern_subscribe_active_streams` | gauge | Inbound subscribers (peers + external CDC). Drop = peer disconnect or CDC consumer crash. |
| `lantern_subscribe_dropped_total{reason}` | counter | `reason=gapped` means a peer's `from_seq` fell off the ring buffer (the pump re-snapshots automatically); `reason=send_failed` is a broken outbound stream. |

### 4.2 Resource / cache health

| Metric | What it tells you |
|---|---|
| `lantern_vertices`, `lantern_edges` | Current **live** working-set size. Must agree (within `lantern_replication_lag_seq`) across peers. These gauges do not include causal barriers. |
| `lantern_vertex_causal_barrier_entries`, `lantern_edge_causal_barrier_entries` | Accepted-expired or otherwise non-visible live Put floors retained to fence delayed older writes. They do not fall merely because TTL GC runs. |
| `lantern_ttl_expirations_total{kind}` | TTL-driven evictions. |
| `lantern_gc_duration_seconds` | GC sweep latency histogram. |
| `lantern_build_info{version,commit,go_version}` | Version pinning for cross-checks during upgrade. |

### 4.3 Search health and replica consistency

Search has both a local derived index and a cluster-wide configuration
contract. Check both before attributing a search incident to ordinary RPC
latency:

**Do not serve mixed search configuration.** Every search-affecting setting
must produce the same `GetServerStatus.search.config_fingerprint` on every
member. A mismatch correctly keeps readiness `NOT_SERVING` while replication
continues. Search reads remain local/eventual, and BM25 scores are relative to
the local corpus; do not compare scores across lagging replicas or unrelated
queries. The [canonical SearchVertices contract](search.md) defines projection,
typed errors, TTL consistency, cursor affinity, and failover behavior.

| Metric | What it tells you |
|---|---|
| `lantern_search_index_state{state}` | One-hot local state. `healthy=1` serves searches; `incomplete=1` rejects them until a bounded rebuild succeeds; `disabled=1` is intentional only when Search is disabled. |
| `lantern_search_index_retained_ratio` | Estimated retained/live byte amplification. Sustained growth indicates compaction pressure even when logical counts look stable. |
| `lantern_search_index_{docs,physical_documents,expired_documents}` | Logical vs physical retention. A widening physical/live gap with expired documents points to purge/GC lag. |
| `lantern_search_index_expiration_queue_entries` / `_expiration_purged` | Expiration backlog and forward purge progress. |
| `lantern_search_index_rebuild_count` / `_last_rebuild_duration_seconds` | Whether recovery rebuilt the index and how expensive the latest rebuild was. |
| `lantern_search_config_match{peer}` | Per-peer fingerprint agreement. Any 0 keeps replicated readiness degraded. |
| `lantern_search_calls_total{mode,outcome,reason}` | Exactly one terminal observation per Search RPC; use it for outcome/error ratios. |
| `lantern_search_phase_duration_seconds{phase,mode}` / `lantern_search_work{kind,mode}` | Splits slow searches into analysis, expansion, postings/positions, and candidate selection. |

The Admin Ops Search card shows the current replica's capability and index
snapshot. Its Search metrics group defaults to per-replica series, including
p50/p99 outcomes and index/config health, so one unhealthy pod is not averaged
away.

### 4.4 RPC layer

The in-house Connect interceptor in
`server/provider/connect_middleware.go` exposes the canonical
`grpc_server_*` metric names (started / handled / handling time).
The names are intentionally retained for operator-dashboard continuity
after the gRPC middleware was deleted in #337/#352; the wire protocol is
Connect. Use them for per-RPC latency SLOs.

### 4.5 Alerts worth shipping (PromQL sketches)

```promql
# Pod has been lagging a peer for > 60s
max_over_time(lantern_replication_lag_seq[1m]) > 1000

# Mutations being dropped persistently (not just at startup)
rate(lantern_replication_dropped_total[5m]) > 0

# Anti-entropy cycles stalled
rate(lantern_anti_entropy_cycles_total[5m]) == 0

# Replica count != desired (paired with the chart's PDB minAvailable)
count(up{job="lantern"} == 1) < 2

# A peer disappeared from /metrics altogether
absent(lantern_replication_lag_seq{peer="…"})

# A local Search index cannot safely serve
max by (instance) (
  lantern_search_index_state{state="incomplete"}
) == 1

# Search retained storage is more than 2x live for 15 minutes
min_over_time(lantern_search_index_retained_ratio[15m]) > 2

# Any observed peer has a different Search configuration
min by (instance) (lantern_search_config_match) == 0

# Search internal/deadline terminal failures are occurring
sum by (instance) (
  rate(lantern_search_calls_total{outcome=~"internal|deadline_exceeded"}[5m])
) > 0
```

Tune thresholds to your write rate; the qualitative shape — lag and
dropped should be ~0, anti-entropy ticks should be non-zero — is the
load-bearing part.

One traversal-specific signal: with `LANTERN_TRAVERSAL_TIMEOUT_MS` set
(#842), an `Illuminate` returning `DEADLINE_EXCEEDED` means the SERVER's
traversal budget expired — an expensive PPR / deep BFS was cut to protect
writers and the GC tick — not a network problem. Raise the budget or narrow
the request (smaller k/step, larger epsilon) rather than retrying as-is.

---

## 5. Capacity planning

Two truisms:

1. **Memory ≈ working set, on every pod.** Lantern is full-replica.
   3 replicas storing 1 GiB of graph use 3 × 1 GiB of RAM total, not
   1 GiB. Plan for the largest single pod plus headroom.
2. **Replication adds network, not storage.** Each write fans out to
   every peer once. With `R` replicas and write rate `W`, peer egress
   is `(R − 1) × W` per pod. Snapshots are O(N+E) per peer on cold
   bootstrap.

**Sizing checklist:**

- Estimate working set: avg vertex size × vertex count + avg edge
  payload × edge count. Add causal-barrier and D4 tombstone metadata, then
  ~20% for radix trie / index overhead.
- Multiply by 1 (memory per pod). Add ~30% headroom for transient
  GC and snapshot serialisation buffers.
- Set the chart's `resources.limits.memory` to that number. Hitting
  the limit OOM-kills the pod, which forces a re-bootstrap from
  peers — disruptive, do not undersize.
- For `ANTI_ENTROPY_INTERVAL_MS`: the tighter, the smaller the
  recovery window after a hiccup, the higher the steady-state load.
  Default `30000` (30s) is sane.
- For `MAX_REPLICATION_LAG`: must be larger than the lag a peer can
  accumulate during one snapshot pause. Default `10000` (entries)
  works for write rates up to a few thousand mut/s.

**Saturation guard (`LANTERN_MAX_VERTICES` / `LANTERN_MAX_EDGES`, #848):**

For an in-memory store the kernel OOM kill is the worst failure mode — it
takes down *all* data on the pod (and forces a re-bootstrap from peers),
instead of degrading the one misbehaving writer. Cap the entry counts so a
write burst faster than TTL decay fails fast instead:

- Set both caps from the sizing estimate above: divide the memory budget
  allocated to graph data by measured per-entry heap cost and leave slack for
  concurrent in-flight batches. Each cap counts the matching live identities
  **plus retained Put causal-barrier entries**. A live/additive identity that
  also has a retained barrier can be counted twice; this conservative policy
  favours bounded admission over a precise union scan.
- The caps are enforced at the **local write-RPC boundary only**. At
  capacity, `PutVertices` / `AddEdges` / `PutEdges` return
  `RESOURCE_EXHAUSTED` naming the knob; reads, deletes, replication apply
  and backup restore always proceed (rejecting writes peers already
  committed would break convergence). Replication/restore may therefore take
  a replica above its configured admission cap; treat it as a soft local
  guard, not a cluster-wide hard heap limit.
- There is **no eviction policy**: a rejected writer must wait for TTL
  decay, GC, or deletes to free capacity, then retry. This is decay-first
  by design.
- For a non-zero vertex cap, alert on
  `(lantern_vertices + lantern_vertex_causal_barrier_entries) /
  scalar(lantern_capacity_limit{kind="vertex"}) > 0.8`; use the corresponding
  live plus barrier gauges for edges. Only enable this alert when the selected
  cap is non-zero. `lantern_validation_rejected_total{reason="capacity"}` counts
  rejected writes.
- An exact singular/plural Delete whose HLC is at least the barrier floor
  replaces that barrier with a D4-bounded tombstone and immediately frees its
  live/barrier cap slot, even when the Delete reports no live value existed.
  Prefix Delete walks only live indexes and cannot find a barrier-only key or
  edge pair; issue the exact Delete to reclaim that slot.
- Delete tombstones are excluded from both admission caps but still consume
  heap until D4 expiry. There is currently no independent hard count/byte cap
  for all causal metadata, so the entry caps are not a total-memory guarantee;
  [#1204](https://github.com/anaregdesign/lantern/issues/1204) tracks that
  hard metadata budget.
- Keep `GOMEMLIMIT` below `resources.limits.memory` as the second line of
  defense — the caps bound entry counts, not bytes. The Helm chart defaults
  `runtime.goMemoryLimit` to `384MiB` under its `512Mi` container limit so a
  restore plus replication catch-up triggers Go GC before a kernel OOM kill,
  without increasing the billed GKE Autopilot request. Override both values
  together and retain headroom for stacks and non-Go memory.

**Securing the cluster (#850) — decision table:**

| Tier | When | How |
|---|---|---|
| Open | isolated network, single-tenant dev | default (no `LANTERN_AUTH_TOKENS`, no TLS) |
| Bearer token | shared dev cluster, managed platform where client certs are friction — `requirepass`-tier | `LANTERN_AUTH_TOKENS=<token>` on every node; clients use `WithAuthToken` / `--token` / `LANTERN_TOKEN` |
| Token + TLS | anything crossing an untrusted network | the above plus `LANTERN_TLS_*` — **bearer tokens over plaintext h2c are sniffable**; token-only auth is NOT transport security |
| mTLS | zero-trust | `LANTERN_TLS_CLIENT_CA_FILE` (unchanged; the strong option) |

Operational notes:

- All nodes in a cluster share the token set; the pump and anti-entropy
  clients send `tokens[0]`. **Rotation order:** add the new token to
  `LANTERN_AUTH_TOKENS` on every server (old,new) → switch clients and
  restart nodes so peers pick the new `tokens[0]` → drop the old token.
- `grpc.health.v1.Health` is always exempt (Kubernetes gRPC probes cannot
  attach headers). Reflection is exempt by default; set
  `LANTERN_AUTH_EXEMPT_REFLECTION=false` to require the token there too.
- The metrics listener is bind-address-scoped and carries no auth — keep
  it off public interfaces.
- Watch `lantern_auth_rejected_total`: a non-zero steady rate after a
  rotation usually means a client is still on the dropped token.

---

## 6. Partition behaviour & split-brain

Lantern is **AP**. Both sides of a partition keep accepting reads and
writes. See RFC §[10](replication.md#10-partition--split-brain-analysis)
for the full analysis. The operational summary:

`SearchVertices` is also available on both sides, but it is explicitly
local/eventual: document membership, BM25 statistics, scores, and top-k order
can differ until heal. Static SDK failover may therefore change a search
response. Route user traffic only to ready replicas and wait for lag plus
search-config signals to converge before comparing exact results.

Search pagination is intentionally endpoint-sticky. The first page may land on
any ready replica, but every non-empty search cursor must return to that same
process: sessions and signing keys are neither replicated nor portable. Enable
load-balancer affinity for at least the advertised
`GetServerStatus.search.cursor_ttl_seconds`; static Go SDK failover pins a
continuation to its current endpoint. If that endpoint is lost, the client gets
typed `SEARCH_CURSOR_INVALID` or `SEARCH_CURSOR_STALE` and must restart from
page one—never resume the cursor on another replica. Tune the session count,
hit, and aggregate-byte caps together with replica memory, and alert on clients
that repeatedly restart deep page chains.

One-page failover is also a new local/eventual read, not continuation of the
lost replica's snapshot. Its membership and numeric scores may legitimately
differ until graph state and `config_fingerprint` converge.

Accepted Put batches replicate as one authoritative mutation containing
ordered `ReplicatedPutVertices` or `ReplicatedPutEdges` entries. Live outcomes
and causal barriers keep their original accepted order, including duplicate
identities; receivers must not regroup them or re-decide an accepted-expired
outcome from their local wall clock.

| Mutation type | Partition-time behaviour | Post-heal |
|---|---|---|
| Add-only `AddEdge*` history | Both sides accumulate contributions. | G-Set union: every contribution survives. Weight = sum. |
| Put-only `PutVertex*` / `PutEdge*` history | Both sides accept an authoritative live or causal-barrier outcome. | Higher HLC wins (LWW); same HLC → higher origin ID wins. Losing outcome is silently superseded. |
| Put/Delete LWW history | Both sides accept. | Higher-HLC value, barrier, or tombstone wins while D4 is retained. **Resurrection hazard if partition > `tombstone_ttl`**. |
| Mixed `PutEdge`/`AddEdge` or `DeleteEdge`/`AddEdge` | Both sides accept. | **Unsupported:** arbitrary delivery order can leave different weights; tracked by #1203. |

Snapshot bootstrap preserves the current mixed edge state it observes: if a
live additive bucket coexists with a retained Put barrier, the edge frame uses
the maximum of the bucket's Put HLC and the barrier floor, and barrier frames
arrive first. That max-floor rule fences delayed older Puts, but it does not
make subsequent arbitrary-order Put/Add or Delete/Add delivery convergent.

**Known convergence boundaries** are therefore (a) a partition that exceeds
the D4 tombstone horizon, and (b) the mixed edge operation families above. For
the D4 case, a `Delete` on side A may be reaped before side B learns about it,
after which side B's stale Put can resurrect the identity. Mitigations, in
order of preference:

1. Detect partitions early (alert on `lantern_replication_lag_seq`
   spiking and `lantern_anti_entropy_gaps_found_total` going non-zero
   on **both** sides of the alleged partition).
2. Keep partitions short. If you can't, extend D4
   (`LANTERN_TOMBSTONE_TTL` — see RFC §4 for the constraint
   that no live TTL may exceed tombstone TTL).
3. After a long partition heals, [force re-snapshot](#9-recovery-procedures)
   from the side you trust.

For mixed edge operation families, route all operations for one edge identity
to a single replica or use one operation family until #1203 lands. If weights
already diverged, quiesce writes and force a snapshot from the authoritative
replica.

There is no "split-brain detector"; the RFC is explicit that
partitions are healed by anti-entropy ([RFC §§6, 10](replication.md)).
If you suspect divergence after a long event, compare
`lantern_vertices` / `lantern_edges` and the two causal-barrier gauges across
pods after lag reaches zero. Equal cardinalities are necessary but not proof of
equal contents; use anti-entropy signals and application-level spot checks
before declaring recovery complete.

---

## 7. Rolling upgrade procedure

The chart uses `RollingUpdate` with `podManagementPolicy: Parallel`
for boot, but one-at-a-time for upgrades. Manual steps for non-Helm
deploys:

1. **Pre-flight:** verify all peers are in sync.
   `max(lantern_replication_lag_seq) == 0` across the cluster.
2. **Drain one pod:** stop or evict it. The PDB (`minAvailable: 2`)
   prevents draining a majority simultaneously.
3. **Restart with the new image.** It will `Snapshot` from a remaining
   peer, then tail `Subscribe`.
4. **Wait for `/readyz` = 200** on the new pod. It should land within
   `LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS` + a few seconds. If
   not, see [§9.3](#93-pod-stuck-not-serving-after-restart).
5. **Confirm convergence:** `lantern_replication_lag_seq{peer=*} == 0`
   for the new pod. `lantern_build_info{version=…}` reflects the new
   tag.
6. Move to the next pod.

**Never** upgrade the whole cluster simultaneously. There's nothing
in the protocol that *strictly* requires sequencing — but the PDB and
the readiness gate are what make this safe; bypass them and you've
created an unnecessary availability dip.

### 7.1 Zero-drop drain (no client-visible request errors) (#768)

The procedure above keeps the *cluster* available, but a rotating pod
can still drop **in-flight / freshly-routed** client requests in the
window between `SIGTERM` and the moment kube-proxy / the load balancer
removes the pod's endpoint. The graceful-drain knobs close that window:

- **`LANTERN_DRAIN_DELAY_SECONDS`** (server-side, the primary mechanism;
  Helm `drain.delaySeconds`, Compose env, default 5). On `SIGTERM` the
  server flips the overall `""` gRPC health entry and `/readyz` to
  `NOT_SERVING` **immediately** (so probes/LBs deregister it) but keeps
  the listener **serving** for this long, so requests routed during the
  propagation window still succeed. It needs no shell in the image — the
  drain is internal. Set it slightly above your platform's
  endpoint-propagation lag (k8s: typically 1–5 s).
- **`terminationGracePeriodSeconds`** (Helm `terminationGracePeriodSeconds`,
  default 45; Compose `stop_grace_period: 45s`). Must be **≥
  `drain.delaySeconds` + `LANTERN_SHUTDOWN_TIMEOUT_SECONDS`** (default 30)
  so the drain window *and* the in-flight flush both finish before
  `SIGKILL`. The old hard-coded 30 s left no headroom.
- **`minReadySeconds`** (Helm, default 10). A freshly-restarted pod must
  stay Ready this long before the controller disrupts the next one, so a
  pod whose readiness flaps right after restart doesn't cascade.
- **`drain.preStop`** (Helm, default **off**). An optional `sleep`
  preStop hook for meshes that drain on container lifecycle rather than
  readiness. Usually unnecessary because the server-side hold already
  covers the propagation window; it requires `/bin/sleep` (present on the
  alpine server image, absent on distroless).

**Verify** a zero-drop rollout by driving steady SDK load across a
`kubectl rollout restart statefulset/<release>` (or a Compose
recreate-one-at-a-time loop) and confirming no `Unavailable` /
connection-reset errors, and that `/readyz` returns **503 at the start
of termination** before the listener stops accepting.

**Single-instance** deploys (`replicaCount: 1`) have no peer to fail over
to, so the drain still flips `/readyz` (the platform shifts traffic to the
new instance) but durability across the rotation comes from the snapshot
backup/restore feature
(`LANTERN_BACKUP_*`, #770) — see [docs/backup.md](backup.md) — not
replication.

**Backwards compatibility.** v1's Subscribe/Snapshot wire format is
versioned at the proto level; minor version bumps within v1 are
wire-compatible. Cross-major upgrades (v1 → v2) are out of scope for
this runbook.

---

## 8. Scaling up / down

### 8.1 Scale up (add a pod)

- New pod starts → resolves discovery DNS → picks a peer → `Snapshot`
  → `Subscribe` from `cutoff_seq + 1`.
- Existing pods detect the new IP within
  `LANTERN_PEER_DISCOVERY_INTERVAL_MS` (default 10s) and start pumping
  to it.
- `/readyz` flips to 200 once lag-to-all-peers is below the threshold.

No operator action required. Just `kubectl scale statefulset lantern
--replicas=N`. On Docker Compose the canonical topology is a fixed
three-node cluster (`lantern-{0,1,2}`) since
[#435](https://github.com/anaregdesign/lantern/issues/435); use the
Helm chart when you need to scale past three.

### 8.2 Scale down (remove a pod)

- The departing pod is removed from the headless Service's A records.
- Peers detect the removal on the next discovery interval and cancel
  the pump goroutine for that address.
- Mutations already in flight to that pod are simply lost from its
  perspective — that's fine, the pod is going away. No
  consumer-visible data loss because the writes were already
  committed to the receiving pod and other peers.

PDB still applies: on k8s, `kubectl scale` will block if going below
`minAvailable`. Lower the PDB before scaling down hard. Going to
zero = total-cluster loss = accepted data loss
([RFC D1](replication.md#3-binding-decisions-d1d7)) unless snapshot
backups are configured ([§9.4](#94-total-cluster-loss)).

---

## 9. Recovery procedures

### 9.1 Force a re-snapshot

Symptom: anti-entropy keeps incrementing `gaps_found` on the same
peer, or you suspect divergence after a long partition.

```sh
# Restart the suspect pod. On startup it will Snapshot from a peer
# and discard its local state.
kubectl rollout restart statefulset/lantern -n <ns>      # all pods
kubectl delete pod lantern-2 -n <ns>                     # one pod
```

The new pod starts in `NOT_SERVING` until snapshot+tail completes.

### 9.2 Subscribe ring buffer overflow

Symptom: `lantern_subscribe_dropped_total{reason="gapped"}`
increments; the server replies `FailedPrecondition` (reason
`gapped`) and the affected peer logs a `replication pump: peer
transition` line with `transition="snapshot_start" reason="gapped"`,
then re-snapshots on its own. No manual action needed.

An edge-only working set is valid Snapshot input: endpoint vertices created
implicitly by `PutEdge*` / `AddEdge*` are carried as concrete nil-valued
Vertex frames. A peer repeatedly logging `snapshot_failed` with
`snapshot: nil vertex payload` is therefore not normal gap pressure; it means
the responder is an incompatible pre-fix build or the stream is corrupt.
Finish the rolling upgrade (or remove the incompatible responder) before
increasing the log capacity.

If overflow is **chronic**, write rate has outgrown
`mutation_log_capacity`. Bump
`LANTERN_MUTATION_LOG_CAPACITY` or add cluster capacity.

### 9.3 Pod stuck `NOT_SERVING` after restart

Walk the bootstrap flow:

1. `kubectl logs <pod>` — look for `snapshot from peer …`.
2. If no log line: pump can't reach any peer.
   - Check `LANTERN_PEER_DISCOVERY` env values.
   - From inside the pod, `getent hosts <discovery-dns-name>` should
     return one A record per other pod.
   - Confirm the headless Service has `publishNotReadyAddresses: true` so
     bootstrapping pods are discoverable. The separate client Service remains
     readiness-gated.
3. If snapshot succeeds but `/readyz` stays 503: lag is above
   threshold. Check `lantern_replication_lag_seq`. Either wait
   (steady-state catch-up) or bump `LANTERN_MAX_REPLICATION_LAG`.
4. **Port mismatch gotcha.** The server's default
   `LANTERN_PEER_DEFAULT_PORT` is `"50051"` (legacy), but the gRPC
   port (`LANTERN_PORT`) defaults to `"6380"`. The Helm chart and
   Compose example both override `LANTERN_PEER_DEFAULT_PORT=6380`
   explicitly. Custom Pod specs that don't set it will try to
   connect to `:50051` and time out. Set it.

### 9.4 Total-cluster loss

Accepted ([RFC D1](replication.md#3-binding-decisions-d1d7)) by the
**replication** layer — it has no persistence. Re-deploy from your image
and rehydrate from upstream sources via a fresh ingestion pass.

**Unless you run snapshot backups** (`LANTERN_BACKUP_*`, see
[backup.md](backup.md)): with a mounted dump volume each node restores its
newest dump on boot, so the cluster comes back at roughly its last
`LANTERN_BACKUP_INTERVAL` instead of empty. Peer bootstrap then reconciles
any per-node differences via HLC.

If you need finer-grained crash-survival than the snapshot interval, the
WAL hook exists in the apply path but is not wired to a writer in v1;
that's a v2 conversation.

---

## 10. Common misconfigurations (gotchas)

A short checklist to walk before opening an incident:

- [ ] **Port mismatch:** `LANTERN_PEER_DEFAULT_PORT` defaults to
      `50051` but `LANTERN_PORT` defaults to `6380`. Set
      `LANTERN_PEER_DEFAULT_PORT=6380` (or whatever you've set
      `LANTERN_PORT` to). The Helm chart and Compose example do this
      for you; custom manifests must.
- [ ] **Probes on wrong port:** `/healthz` and `/readyz` are on the
      **metrics** port (9090), not the gRPC port (6380).
- [ ] **`publishNotReadyAddresses: false` deadlock:** if every pod restarts
      simultaneously, none is "ready", the headless Service publishes
      nothing, and nobody bootstraps. The maintained Helm chart sets this to
      `true`; custom manifests must do the same for peer discovery while
      keeping the client-facing Service readiness-gated.
- [ ] **OSS nginx as a Lantern LB:** the `resolve` keyword on
      `upstream server` is nginx-plus only. Use an HTTP/2-aware
      reverse proxy (Envoy, Caddy, Traefik) or kube-proxy via a
      ClusterIP Service instead.
- [ ] **Tombstone TTL < live TTL:** RFC §4 / D4 — any `Put*` whose
      TTL would exceed `tombstone_ttl` is **rejected** with
      `InvalidArgument`. Either lower the live TTL or raise the
      tombstone TTL.
- [ ] **Empty `LANTERN_PEERS` + `LANTERN_PEER_DISCOVERY=static`** =
      single-instance mode, not "no readiness gating please". If you
      want HA, set discovery to `dns` (or put hosts in
      `LANTERN_PEERS`).
- [ ] **Scaling a single-instance deploy to > 1 replica expecting HA.**
      On a platform that hides per-instance addresses it won't form a
      cluster, and there's no warning. See [§2.1](#21-single-instance-mode-no-ha).
- [ ] **NTP drift > 500 ms.** RFC D3. Watch
      `lantern_hlc_skew_clamped_total` if/when present, or just keep
      NTP healthy.
- [ ] **Heterogeneous search configuration.** Keep every `LANTERN_SEARCH_*`
      value identical across replicas. Pump and anti-entropy compare
      `PeerStatus.search_config_fingerprint` automatically; a mismatch sets
      `lantern_search_config_match{peer}=0`, increments
      `lantern_search_config_mismatch_total{peer}`, logs both fingerprints,
      and keeps readiness `NOT_SERVING` while graph replication continues.
      `GetServerStatus.search.config_fingerprint` remains the direct per-pod
      diagnostic. In particular, a replica with
      `LANTERN_SEARCH_POSITIONS=false` rejects `phrase=true` with
      `SEARCH_POSITIONS_DISABLED` while a positions-enabled replica executes
      the phrase query.
      See the [canonical search HA contract](search.md#replication-failover-and-cursors)
      before changing routing, cursor affinity, or any search setting.

---

## 11. Failure mode reference

Mirrors RFC §[11](replication.md#11-failure-modes), annotated with
operator actions.

| Failure | Signal | What to do |
|---|---|---|
| Single pod crash | k8s probe / Compose healthcheck flips | Auto-restarts. Pod bootstraps from peers. No action unless it crashloops. |
| Pod falls behind buffer | `subscribe_dropped_total{reason="gapped"}` increments | Pump auto re-snapshots. Chronic = bump capacity. |
| All peers unreachable on boot | Pod `NOT_SERVING`, no `replication_applied` increments | Check discovery env (§9.3). |
| Total-cluster loss | Every pod down | Accepted data loss; rehydrate — or restore from snapshot backups if configured ([§9.4](#94-total-cluster-loss)). |
| NTP skew > 500 ms | `lantern_hlc_skew_clamped_total` (planned — #180/#182; until then watch NTP) | Fix NTP. Convergence preserved, but the drifted peer's stamps land behind real wall time. |
| Network partition < tombstone TTL | `replication_lag_seq` spike; `anti_entropy_gaps_found_total` non-zero after heal | Auto-converges. No action. |
| Network partition > tombstone TTL | Same signals + possible resurrection | Force re-snapshot from the side you trust ([§9.1](#91-force-a-re-snapshot)). Consider extending tombstone TTL. |
| Causal barriers approach an admission cap | Live + matching `lantern_*_causal_barrier_entries` fill ratio rises; capacity rejections begin | Use exact Deletes to transition intended barrier-only identities into D4 tombstones. Prefix Delete cannot find them. Size heap for tombstones; #1204 tracks a hard metadata cap. |
| Mixed Put/Add or Delete/Add edge history diverges | Lag is zero but the same edge weight differs | Unsupported pending #1203. Quiesce that identity and force a snapshot from the authoritative replica. |
| Search config mismatch | `search_config_match{peer}=0`, readiness 503 | Make search-affecting env identical; graph replication is intentionally still active. |

---

## 12. Quick smoke-test for a fresh cluster

After install, verify HA actually works:

```sh
# k8s
kubectl get pods -l app.kubernetes.io/instance=lantern
kubectl port-forward svc/lantern 6380:6380 &

# Compose
cd deploy/compose && docker compose up -d && docker compose ps
```

Then from a host that can reach the cluster:

```sh
# Write to one pod (the Service VIP / reverse proxy fans it out;
# it might land anywhere — that's the point).
go run ./sdks/go/example -addr http://localhost:6380 -op put-vertices

# Within ~1s, every pod should report the same vertex count.
for pod in lantern-0 lantern-1 lantern-2; do
  kubectl exec "$pod" -- wget -qO- http://localhost:9090/metrics \
    | grep '^lantern_vertices '
done
```

If the counts agree, replication is working. If they don't agree
within a few seconds, check `lantern_replication_lag_seq` and walk
[§9.3](#93-pod-stuck-not-serving-after-restart).

---

## 13. Where to file issues

- Operational surprises (a knob behaves differently than this runbook
  claims): file against the `ha` label with a `[runbook]` prefix.
- Protocol-level surprises (CRDT semantics, HLC behaviour, snapshot
  format): file against `ha` and reference the relevant RFC section.
- Performance / capacity surprises: include
  `lantern_mutation_log_entries_total`, `lantern_replication_lag_seq`,
  and `lantern_vertices`/`lantern_edges` at the time of the event.

Cross-references:
[RFC](replication.md) ·
[Helm chart](../deploy/helm/lantern/) ·
[Compose example](../deploy/compose/) ·
[README HA sections](../README.md#run-on-kubernetes-ha-mode).
