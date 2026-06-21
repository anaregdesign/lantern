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
| Google Cloud Run | ❌ HA not supported | ✅ | [§3.5](#35-serverless-paas-single-instance-only) |
| Azure Container Apps | ❌ HA not supported | ✅ | [§3.5](#35-serverless-paas-single-instance-only) |
| AWS App Runner / Fly Machines (autoscale) | ❌ HA not supported | ✅ | [§3.5](#35-serverless-paas-single-instance-only) |

**Why some PaaS platforms can't do HA.** Lantern's leaderless P2P
needs (a) stable per-instance addressing for peer discovery and
(b) long-lived inbound gRPC streams between every pair of instances.
Cloud Run / ACA / App Runner deliberately hide instance addresses,
route every request through a load balancer, and recycle instances
on the request lifecycle. Single instance always works — and is
genuinely useful as a fast in-memory KVS with CDC via `Subscribe` —
but multiple instances cannot form a cluster on those platforms.
See RFC [D7](replication.md#3-binding-decisions-d1d7).

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
- Cold start = empty cache. There is no persistence in v1
  ([RFC D1](replication.md#3-binding-decisions-d1d7)).

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
`clusterIP: None`, `publishNotReadyAddresses: false`) is what the
DNS pump resolves — its A records are the live pod IPs. The
`ClusterIP` Service is what clients hit; they get a stable VIP and
kube-proxy spreads load across the backing pods. Splitting them means
client traffic never accidentally targets a pod that's still
bootstrapping.

**Single-instance on k8s.** Set `replicaCount: 1` and
`replication.discovery.mode: static` with empty `replication.peers`.
The chart strips the peer env entirely; you get single-instance
mode (§2.1) with k8s scheduling and probes intact.

**Probes.** Liveness and readiness both hit the metrics port (9090),
paths `/healthz` and `/readyz`. The readiness probe is intentionally
slow (`initialDelaySeconds: 5`, `periodSeconds: 10`,
`failureThreshold: 6` = ~60 s window) so a freshly-scaled-up pod can
finish anti-entropy bootstrap without being restarted as "not ready
fast enough". **Brief 503s on `/readyz` immediately after a scale-up
are expected**, not a bug.

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

### 3.5 Serverless PaaS (single-instance only)

**Cloud Run / Azure Container Apps / AWS App Runner / Fly Machines:**

- Set instance count / max replicas / scale = 1.
- Do **not** set `LANTERN_PEER_DISCOVERY` or `LANTERN_PEERS`.
- Allocate enough memory for your working set (§5).
- Treat the service as a fast in-memory KVS. Cold start = empty
  cache.
- For CDC: open `Subscribe` from a long-lived consumer outside the
  PaaS (e.g., a worker on k8s or a VM) and persist downstream.

The single-instance gating ([§2.1](#21-single-instance-mode-no-ha))
keeps `/readyz` returning `SERVING` immediately after the gRPC port
opens, so the platform's health-check happy-path works unchanged.

Why not just run more instances? Because the platforms (deliberately)
won't let those instances see each other on stable per-instance
addresses, and the request lifecycle is request-scoped — long-lived
peer streams are torn down. Set scale=1 and stop fighting the
platform.

---

## 4. What to watch (signals)

All metrics are exposed at `/metrics` on the metrics address
(`LANTERN_METRICS_ADDR`, default `:9090`). The names below are the
exact Prometheus series.

### 4.1 Cluster health

| Metric | Type | What it tells you |
|---|---|---|
| `lantern_replication_lag_seq{peer}` | gauge | How many mutation log entries this pod is behind `peer`. **The single most important HA signal.** |
| `lantern_replication_applied_total{peer,origin}` | counter | Mutations from `peer` that landed in the local cache. `rate()` ≈ steady-state replication throughput per peer. |
| `lantern_replication_dropped_total{peer,reason}` | counter | Mutations rejected at apply. `reason` is `tombstone` / `older_hlc` / `self_echo` etc. Persistent non-zero rate signals real conflict or a stuck peer. |
| `lantern_anti_entropy_cycles_total` | counter | Anti-entropy ticks. Should advance at `LANTERN_ANTI_ENTROPY_INTERVAL_MS` cadence. |
| `lantern_anti_entropy_gaps_found_total{peer,origin}` | counter | Non-zero = real divergence detected and being repaired. Spiking after a partition heal = expected. Persistently incrementing in steady state = open a bug. |
| `lantern_mutation_log_entries_total` | counter | Total mutations ever logged on this pod. |
| `lantern_mutation_log_capacity` | gauge | Ring buffer capacity. If sustained `rate(mutation_log_entries) × subscribe_lag_seconds > capacity`, slow peers will fall off and re-snapshot. |
| `lantern_subscribe_active_streams` | gauge | Inbound subscribers (peers + external CDC). Drop = peer disconnect or CDC consumer crash. |
| `lantern_subscribe_dropped_total{reason}` | counter | `reason=out_of_range` ≥ 1 means a peer's `from_seq` fell off the ring buffer; the pump will re-snapshot automatically. |

### 4.2 Resource / cache health

| Metric | What it tells you |
|---|---|
| `lantern_vertices`, `lantern_edges` | Current working-set size. Must agree (within `lantern_replication_lag_seq`) across peers. |
| `lantern_ttl_expirations_total{kind}` | TTL-driven evictions. |
| `lantern_gc_duration_seconds` | GC sweep latency histogram. |
| `lantern_build_info{version,commit}` | Version pinning for cross-checks during upgrade. |

### 4.3 RPC layer

The in-house Connect interceptor in
`server/provider/connect_middleware.go` exposes the canonical
`grpc_server_*` metric names (handled / handling time / received / sent).
The names are intentionally retained for operator-dashboard continuity
after the gRPC middleware was deleted in #337/#352; the wire protocol is
Connect. Use them for per-RPC latency SLOs.

### 4.4 Alerts worth shipping (PromQL sketches)

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
```

Tune thresholds to your write rate; the qualitative shape — lag and
dropped should be ~0, anti-entropy ticks should be non-zero — is the
load-bearing part.

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
  payload × edge count. Add ~20% for radix trie / index overhead.
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

---

## 6. Partition behaviour & split-brain

Lantern is **AP**. Both sides of a partition keep accepting reads and
writes. See RFC §[10](replication.md#10-partition--split-brain-analysis)
for the full analysis. The operational summary:

| Mutation type | Partition-time behaviour | Post-heal |
|---|---|---|
| `AddEdge*` | Both sides accumulate contributions. | G-Set union: every contribution survives. Weight = sum. |
| `PutVertex*` / `PutEdge*` | Both sides accept. | Higher-HLC wins (LWW); same HLC → higher origin ID wins. Losing side's value silently dropped. |
| `DeleteVertex*` / `DeleteEdge*` | Both sides accept. | Higher-HLC tombstone wins. **Resurrection hazard if partition > `tombstone_ttl`** (D4, default 1 year / 8760h). |

**The one real failure case** is a partition that exceeds tombstone
TTL: a `Delete` on side A may be GC'd from the mutation log before
side B learns about it, then side B's stale `Put` looks "newer" than
nothing and resurrects the entry. Mitigations, in order of preference:

1. Detect partitions early (alert on `lantern_replication_lag_seq`
   spiking and `lantern_anti_entropy_gaps_found_total` going non-zero
   on **both** sides of the alleged partition).
2. Keep partitions short. If you can't, extend D4
   (`LANTERN_TOMBSTONE_TTL_SECONDS` — see RFC §4 for the constraint
   that no live TTL may exceed tombstone TTL).
3. After a long partition heals, [force re-snapshot](#9-recovery-procedures)
   from the side you trust.

There is no "split-brain detector"; the RFC is explicit that
partitions are healed by anti-entropy ([RFC §§6, 10](replication.md)).
If you suspect divergence after a long event, compare
`lantern_vertices` / `lantern_edges` across pods — equal values
(within current `lantern_replication_lag_seq`) means convergent.

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

**Single-instance (Tier B)** deploys (Cloud Run / ACA, `replicaCount: 1`)
have no peer to fail over to, so the drain still flips `/readyz` (the
platform shifts traffic to the new revision) but durability across the
rotation comes from the snapshot backup/restore feature
(`LANTERN_BACKUP_*`, #770), not replication.

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
([RFC D1](replication.md#3-binding-decisions-d1d7)).

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

Symptom: `lantern_subscribe_dropped_total{reason="out_of_range"}`
increments; the affected peer logs `OutOfRange` and re-snapshots on
its own. No manual action needed.

If overflow is **chronic**, write rate has outgrown
`mutation_log_capacity`. Bump
`LANTERN_MUTATION_LOG_CAPACITY` (if/when the env knob exists) or
add cluster capacity.

### 9.3 Pod stuck `NOT_SERVING` after restart

Walk the bootstrap flow:

1. `kubectl logs <pod>` — look for `snapshot from peer …`.
2. If no log line: pump can't reach any peer.
   - Check `LANTERN_PEER_DISCOVERY` env values.
   - From inside the pod, `getent hosts <discovery-dns-name>` should
     return one A record per other pod.
   - The headless Service has `publishNotReadyAddresses: false`; if
     **all** pods restart simultaneously, no pod is "ready" yet so
     the headless Service publishes nothing. **Boot one pod at a
     time** in this case.
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

Accepted ([RFC D1](replication.md#3-binding-decisions-d1d7)): v1 has
no persistence. Re-deploy from your image; rehydrate from upstream
sources via a fresh ingestion pass.

If you need crash-survival, the WAL hook exists in the apply path
but is not wired to a writer in v1; that's a v2 conversation.

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
- [ ] **`publishNotReadyAddresses: false` deadlock:** if every pod
      restarts simultaneously, none is "ready", the headless Service
      publishes nothing, and nobody bootstraps. Roll pods one at a
      time, or temporarily flip the flag.
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
- [ ] **Scaling a serverless PaaS to > 1 replica.** It won't work
      and there's no warning. See [§3.5](#35-serverless-paas-single-instance-only).
- [ ] **NTP drift > 500 ms.** RFC D3. Watch
      `lantern_hlc_skew_clamped_total` if/when present, or just keep
      NTP healthy.

---

## 11. Failure mode reference

Mirrors RFC §[11](replication.md#11-failure-modes), annotated with
operator actions.

| Failure | Signal | What to do |
|---|---|---|
| Single pod crash | k8s probe / Compose healthcheck flips | Auto-restarts. Pod bootstraps from peers. No action unless it crashloops. |
| Pod falls behind buffer | `subscribe_dropped_total{reason="out_of_range"}` increments | Pump auto re-snapshots. Chronic = bump capacity. |
| All peers unreachable on boot | Pod `NOT_SERVING`, no `replication_applied` increments | Check discovery env (§9.3). |
| Total-cluster loss | Every pod down | Accepted data loss. Rehydrate. |
| NTP skew > 500 ms | `hlc_skew_clamped_total > 0` | Fix NTP. Convergence preserved, but the drifted peer's stamps land behind real wall time. |
| Network partition < tombstone TTL | `replication_lag_seq` spike; `anti_entropy_gaps_found_total` non-zero after heal | Auto-converges. No action. |
| Network partition > tombstone TTL | Same signals + possible resurrection | Force re-snapshot from the side you trust ([§9.1](#91-force-a-re-snapshot)). Consider extending tombstone TTL. |

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
