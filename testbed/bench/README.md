# lantern bench harness (`testbed/bench/`)

Reusable performance + memory-leak harness for the HA `docker compose`
cluster provisioned by `compose.native.yml`; operator deployment examples remain in `deploy/compose/`. Drives the cluster with [`ghz`][ghz] or a
narrow scenario-owned verified Connect/HTTPS receipt driver, captures Prometheus range queries +
Go pprof snapshots, applies per-scenario leak / lifecycle-metric / semantic /
producer-performance gates, and renders a Markdown report.

The explicitly selected [protected query preparation lane](protectedquery/README.md)
uses matched standalone OFF/OIDC verified TLS, local JWT/Role admission and
Search/BFS/PPR/Community contracts for #1612. It does not run through the release
sweep or qualify final performance, provider login or #1608 admission integration.

> **PR CI does not run the wall-clock scenarios.** The default PR pipeline
> runs their schema/contract tests, but not a Compose load run. On a root
> `vX.Y.Z` tag, however, `docker-publish.yml` runs the short
> `search_qualification` scenario plus targeted real-h2c Search tests as a
> blocking qualification. A failed or skipped stage prevents image and GitHub
> Release publication. Its bounded artifact records the tag, full commit SHA,
> every stage status, and the scenario report.
>
> **Pre-tag performance evidence is local.** On the exact reviewed source,
> run `./testbed/bench/release.sh` plus four separate fresh-WAL receipt
> scenarios before tagging. One complete passing run on a chosen suitable
> environment (ARM or x86) is required, not one run on each architecture. Each
> scenario owns a fresh Compose lifecycle so data, retained high-water state,
> and scenario-specific cluster env cannot leak into its successor (#1097). To
> keep wall-time bounded without losing coverage, the sweep is eight
> scenarios: four fan-out scenarios (`broad_rw`, `broad_mutate`,
> `broad_illuminate`, `edge_contrib_idempotent`) that
> exercise many call paths inside a single steady window (read / write /
> batch / scan / edge / delete / ScanVertexKeys / CountVerticesByPrefix /
> idempotent AddEdge, targeted contribution deletion, and the Illuminate
> algorithm/reduction × objective × weighting axes) plus `ttl_churn`,
> `many_subscribers`, `search_churn` (index decay
> / thread-safety under churn, #703), and `replication_apply_churn`
> (vertexHLC map regression gate, #700 / #705). It produces a
> fixed-format `bench-report.md`. Record the source SHA, image ID, exact
> platform/CPU conditions, all gate verdicts and artifact hashes on the
> release-tracking Issue. The release workflow no longer runs a long CI
> benchmark or splices its noisy result into the notes. The short, blocking
> Search qualification above is a deterministic release conformance gate,
> not a substitute for local performance evidence. See issues [#256],
> [#262], [#573], [#708], [#1063], [#1097], [#1556].
>
> **Receipt qualification uses the same local image.** Run four sequential
> fresh-WAL, three-replica scenarios after the canonical sweep: conditional
> Vertex Put, absent Vertex Delete, absent Edge Delete, and contribution-keyed
> Edge Add. None is in
> `release-scenarios.txt`. Historical Edge-only synthetic-parent runs support
> provisional thresholds, **not final merged-stack evidence**; the four-family
> host qualification on a quiet exact integrated image remains required
> ([#1399]). A failed, missing, or truncated family does not qualify.

`bench-nightly.yml` retains its historical filename but has no schedule.
Use it only to investigate hosted-platform failures, not as the authority
for release performance:

```bash
gh workflow run bench-nightly.yml --ref <commit-or-branch>
```

An empty `diagnostic_scenario` runs the full eight-scenario sweep followed by
the four receipt scenarios. Both modes verify the checkout and image,
require a fresh runner without existing containers, and wait up to three minutes
for a low-load host before traffic begins. Specify a scenario stem to diagnose
one case; the workflow then summarizes bounded ghz error reasons. For
`broad_rw` and `broad_mutate` it also prints replication health and the first
80 plus last 160 peer-repair lifecycle events per replica (including Subscribe
gap reasons and Snapshot failures) before teardown; no request payloads are
uploaded. The full manual sweep retains the generated
`broad_rw` and `broad_mutate` ghz/runtime summaries for post-failure error
attribution without changing scenario traffic, gate thresholds, or the
per-scenario Compose teardown. A hosted diagnostic (even a full green sweep)
is not a substitute for local pre-tag qualification.
`diagnostic_log_capacity` and `diagnostic_subscriber_buffer`
override only the named diagnostic run (the full sweep retains the canonical
10,000-entry ring and the server's subscriber-buffer default, currently 512);
the latter accepts an integer from 1 to 100,000. Ordinary PR CI remains
focused on deterministic tests. Supplying a diagnostic override without
`diagnostic_scenario` fails before a full sweep starts.

[#256]: https://github.com/anaregdesign/lantern/issues/256
[#262]: https://github.com/anaregdesign/lantern/issues/262
[#573]: https://github.com/anaregdesign/lantern/issues/573
[#708]: https://github.com/anaregdesign/lantern/issues/708
[#1063]: https://github.com/anaregdesign/lantern/issues/1063
[#1097]: https://github.com/anaregdesign/lantern/issues/1097
[#1399]: https://github.com/anaregdesign/lantern/issues/1399
[#1442]: https://github.com/anaregdesign/lantern/issues/1442
[#1556]: https://github.com/anaregdesign/lantern/issues/1556

## Prerequisites

- Docker + stable `docker compose` v2 or v5 (including Docker Desktop `-desktop.N` builds)
- [`ghz`][ghz] ≥ 0.117 — drives default scenarios over gRPC wire frames. The Lantern
  server is Connect-only (see [#335][i335]), but the Connect-Go handlers
  accept gRPC, gRPC-Web, and Connect on the same h2c socket, and
  `connectrpc.com/grpcreflect` exposes the standard
  `grpc.reflection.v1*` service the harness uses for descriptor
  discovery. ghz keeps working unchanged. See [#383][i383] for the
  verification log. The receipt scenarios use their dedicated Go driver instead.
- [`yq`][yq] v4 (Go reimplementation)
- `jq`, `curl`, Python 3 (standard library only), `bash` ≥ 4
- Go (matching `go.mod` toolchain) — used to build the report renderer
  and generate the receipt scenario's ephemeral TLS material (no external
  certificate tool or committed key is needed)

The manual release driver records the actual Compose version and plugin binary
hash. It rejects legacy or unknown majors, prereleases and unrecognized suffixes
before image construction or workloads (#1908). Docker documents
[v5 as retaining the v2 CLI behavior](https://docs.docker.com/compose/intro/history/);
accepting a version does not establish workload qualification.

[i335]: https://github.com/anaregdesign/lantern/issues/335
[i383]: https://github.com/anaregdesign/lantern/issues/383

## Quick start

```bash
# Build one immutable local image with its exact source commit before any run:
docker build --build-arg COMMIT="$(git rev-parse HEAD)" -t lantern:local .
export LANTERN_IMAGE=lantern:local
# Run any scenario:
./testbed/bench/run.sh write_heavy

# Keep the cluster up after the run (useful for ad-hoc poking):
KEEP_UP=1 ./testbed/bench/run.sh read_heavy

# Each run owns a new signed cohort/state; SKIP_UP is rejected.
# KEEP_UP stops the owned renewal helper: retained peers expire within 10m.

# Also capture a 30s CPU profile per replica after the steady phase:
PPROF_CPU=1 ./testbed/bench/run.sh addedge_contention

# Exploratory four-family receipt sweep; for #1399 qualification, use a clean
# frozen source commit/image and separately record quiet-host evidence.
docker build --build-arg COMMIT="$(git rev-parse HEAD)" -t lantern:local .
export LANTERN_IMAGE=lantern:local
export EXPECTED_LANTERN_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$LANTERN_IMAGE")"
export EXPECTED_LANTERN_COMMIT="$(git rev-parse HEAD)"
for scenario in receipt_vertex_put_admission_lookup \
  receipt_vertex_delete_admission_lookup receipt_admission_lookup \
  receipt_edge_add_admission_lookup; do
  LEAK_GATE_ONLY=1 ./testbed/bench/run.sh "$scenario"
done
```

## Local pre-tag qualification

Owned security initialization keeps exact Env and mount checks. If only a
read-only bind Source differs across Docker's host/VM boundary, a fixed
network-free BusyBox metadata probe must prove the same effective device,
inode and expected file type for the declared/original/clone source pair.
Other differences fail closed; no path-prefix alias is assumed. Owner and
probe snapshots plus the bounded result remain in private
`lantern-bind-proof.*` evidence after initialization. This setup proof neither
starts serving nor qualifies time, providers or workload performance.

Protected receipt preparation builds the same-source original-authority exporter
for the pinned Linux image's architecture and runs it in a separate, network-free
provisioning container. This preserves the Linux time-profile binding when the
driver host is macOS. Only the new private fixture workspace is mounted there;
operator signing keys never enter a serving container. Each private-plane Server, including OFF,
receives `server time.asia.apple.com` as its declared read-only `/etc/ntp.conf`.
The benchmark uses an explicitly controlled local UDP source (#1963). A separate
fixture supplies synthetic UTC over UDP/123; the ordinary Server image still
executes its production resolver, native sampler, nonce/packet/RTT validation,
interval arithmetic, expiry, timeout and backoff. The fixture is outside the
measured Server processes and CPU set. Exact local routing applies to all
initialization clones, serving containers and restarts, on an isolated network
that prevents public fallback. Missing or changed routing/fixture evidence makes
the case incomplete. Normal and retained lanes require the same declared local
time profile. The short release Search scenario uses this profile too.

Docker does not publish host ports for containers attached only to internal
networks. The benchmark therefore uses one separate raw TCP relay: Servers and
the NTP fixture keep only the internal backend, while the relay also joins a
task-owned frontend and publishes only the existing localhost API ports (plus
the existing canonical OFF diagnostics). Relay destinations are immutable,
literal private addresses observed from that cohort. TLS/HTTP2 bytes pass
through without termination. There is no UDP, DNS, CONNECT or arbitrary-target
forwarding; protected receipt metrics retain the existing Docker-exec collector.
The relay declares `net.ipv4.ip_forward: "0"` and
`net.ipv6.conf.all.forwarding: "0"` through Docker's container-scoped sysctls.
Its helper verifies actual namespace forwarding before opening listeners and
refuses enabled or unknown values. It changes no host/daemon/global firewall,
DNS, route or clock setting.

The relay is an additional network hop and changes the connection's remote
address. Its fixed code, configuration, resources and both network attachments
are part of the new profile in both lanes. It provides no performance equivalence
to historical direct-published measurements. Server CPU placement, workloads,
GC cuts and numerical floors remain unchanged. Fixed backend addresses also
exclude the separate manual chaos/recreation scenario from this profile; the
canonical16 and short Search contain no such scenario. The bounded mechanism's
negative restart reads the new process's actual native-time refusal, rather than
inferring refusal from a relay connection failure.

Formal load runs require explicit `LANTERN_BENCH_CPUSET=0-3` and at least five
Docker CPUs: all three Servers share CPUs 0–3; the fixture (0.25 CPU, 64 MiB,
64 PIDs) and relay (1 CPU, 512 MiB, 128 PIDs) use CPU 4. The
short `search_qualification` correctness scenario may keep its existing Server
placement and share a CPU with its fixture and relay; it records that placement explicitly
and cannot supply a formal matrix case. Its own correctness/performance checks
remain blocking. No extra Server CPU capacity is inferred from fixture placement.

The relay-only 512 MiB limit (#1965) is an engineering allowance for the
fixed benchmark workloads. At a nominal 65 TCP connections, two Linux splice
pipes per connection can request 130 MiB of pipe capacity; capacity is not
resident or cgroup-charged memory. The remaining 382 MiB is an unmeasured
allowance, not a measured minimum or a guarantee for the 4096-connection
admission ceiling. Relay CPU, PIDs, copy behavior and Server resources are
unchanged. A separate diagnostic recorded an owned relay OOM under 128 MiB;
it does not establish the cause of the earlier formal failure. Old or missing
relay limits fail the strict profile checks, and historical PASS results do
not carry into the new exact-source matrix.

This is performance evidence with controlled UTC, not external time-source
qualification. The independent normal-build configured-source-integration gate
retains its single three-member cohort, six init/resume acquisitions, schema 3,
SDK4, native 15s checks and final export revocation. Its aggregate <=12 attempts
and <=12 writes are checked after owners stop: they are PASS-admission limits,
not a physical send quota for a failed run. Required PR, main, release and chosen
environment gates remain separate. Public OIDC acceptance is not implicitly
migrated to fixture time. This input never sets OS time. OFF owns one clock per
private-plane process; OIDC peer leases borrow their current authority clock.
Production source selection, sound holdover and all other security checks remain.
Preparation refuses an unsupported image, unsafe/existing exporter workspace or
changed image identity.

A full benchmark matrix formerly created at least 72 time owners: canonical
OFF `8*3`, normal receipt `4*(3 init+3 serve)`, and retained receipt
`4*(3 init+3 serve)`. Refresh/retry could add external writes. The controlled
profile makes those benchmark writes local. The 5s refresh goal describes an
interval width, not a 5s polling interval. A lost fixture does not immediately
invalidate a still-sound anchor; a fresh unanchored process must refuse instead
of falling back to a public source. Historical external-source PASS/FAIL results
retain their meaning and cannot be mixed into the new profile's qualification.

The local cohort explicitly runs under the driver's non-root UID/GID so the
host publisher and serving processes can read the same owner-only authority
inputs after atomic renewal. Every original authority input remains mode 0600;
operator signing keys stay outside Server mounts. After proving its fresh named
project absent, the driver creates its containers and validates every distinct
state/backup volume against its project labels. Receipt cohorts retain exactly
six such volumes. Their receipt overlay sets `nocopy: true` on each state/backup
mount so native clone creation preserves the initializer's selected UID/GID
instead of copying the image user's directory metadata. Graph-only cohorts additionally select
[`compose.graph-snapshot.yml`](compose.graph-snapshot.yml), with three new
per-node named staging volumes at `/var/lib/lantern-snapshot-spool`, for exactly
nine owned volumes. Their `nocopy: true` prevents the image user's private
directory from being copied into the host-UID fixture. One network-free
initializer changes only those new empty volume directories to the selected
UID/GID and mode 0700. Unexpected existing contents or identity/label drift
refuses setup and retains the project for review. The runtime user, dedicated
staging mounts and private directory metadata are checked before/after traffic
and recorded with image provenance. The released image's non-root default and
all production file, time, quorum, durability and authorization checks remain.

Choose one quiet local environment (ARM or x86) for the complete eight-plus-four
qualification on the reviewed source. A second architecture is not a beta
release gate; retain genuine failed gate results rather than switching hosts
to hide them. Platform-specific production SLOs require separate evidence.

Use a quiet, dedicated host with a recorded Docker engine/VM budget. The
example pins all three replicas to the **same four Docker CPUs** while the
load generator runs on the host. On macOS, `ghz`/Go therefore use host CPUs
outside the Linux/arm64 Docker VM; this is a platform-scoped result, **not**
a Linux/amd64 four-CPU result. On Linux, pin the load generator to the same
CPU set (for example, run the commands with `taskset -c 0-3`). Record where
the generator actually ran, and never compare throughput across different
host/VM budgets as if they were the same baseline. The current numeric
`perf_gate:` floors and traffic mix must remain unchanged; any future
platform-specific re-baseline needs a separate reviewed change with
multiple comparable local measurements.

Freeze a **reviewed final source SHA** into a separate directory so
uncommitted work, including device-project changes, cannot enter the image,
benchmark drivers, or reports. Choose a durable evidence directory; do not
run from a preliminary branch and substitute its report for the final tag.
All commands below run from the repository root unless noted.

```bash
set -euo pipefail
SOURCE_SHA="$(git rev-parse HEAD)"
EVIDENCE_PARENT="$HOME/lantern-bench-evidence"
mkdir -p "$EVIDENCE_PARENT"
EVIDENCE_DIR="$(mktemp -d "$EVIDENCE_PARENT/run.XXXXXX")"
mkdir -p "$EVIDENCE_DIR/source"
git archive "$SOURCE_SHA" | tar -xf - -C "$EVIDENCE_DIR/source"
cd "$EVIDENCE_DIR/source"

# Do not run alongside other containers or a busy VM. Record the host, VM,
# installed generator versions, and exact budget before starting traffic.
test -z "$(docker ps --quiet)"
{
  printf 'source_sha=%s\n' "$SOURCE_SHA"
  uname -srm
  uptime
  docker info --format 'engine={{.ServerVersion}} os={{.OSType}} arch={{.Architecture}} cpus={{.NCPU}} memory={{.MemTotal}}'
  docker version --format 'client={{.Client.Version}} server={{.Server.Version}}'
  ghz --version
  yq --version
  go version
} | tee "$EVIDENCE_DIR/host.txt"

docker build --build-arg VERSION=local-qualification \
  --build-arg COMMIT="$SOURCE_SHA" -t lantern:local-qualification .
export LANTERN_IMAGE=lantern:local-qualification
export EXPECTED_LANTERN_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$LANTERN_IMAGE")"
export EXPECTED_LANTERN_COMMIT="$SOURCE_SHA"
docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$LANTERN_IMAGE" |
  grep -Fx "LANTERN_COMMIT=$SOURCE_SHA"
printf 'image_id=%s\n' "$EXPECTED_LANTERN_IMAGE_ID" | tee -a "$EVIDENCE_DIR/host.txt"

# Formal qualification pins every replica to the same four CPUs.
# On macOS the host generator is NOT in that Docker CPU set; state this
# limitation on the tracking Issue. Use the same exact image for all 12 runs.
export LANTERN_BENCH_CPUSET=0-3
docker compose -f deploy/compose/docker-compose.yml \
  -f testbed/bench/compose.override.yml \
  -f testbed/bench/compose.cpuset.yml config --format json |
  jq -e '.services as $services |
    all(["lantern-0","lantern-1","lantern-2"][]; $services[.].cpuset == "0-3")' >/dev/null

# Wait for the Docker VM's one-minute load to fall below half of the four
# allocated CPUs. A busy host or another running container disqualifies
# the sample; do not stop someone else's workload automatically.
quiet=0
for _ in {1..18}; do
  vm_load="$(docker run --rm --entrypoint awk "$LANTERN_IMAGE" '{print $1}' /proc/loadavg)"
  if awk -v load="$vm_load" 'BEGIN { exit !(load < 2) }'; then
    quiet=1
    break
  fi
  sleep 10
done
printf 'docker_vm_load_1m=%s\n' "$vm_load" | tee -a "$EVIDENCE_DIR/host.txt"
test "$quiet" -eq 1 && test -z "$(docker ps --quiet)"
export COMPOSE_PROJECT_NAME=lantern-bench-local
export KEEP_OUT=1 LEAK_GATE_ONLY=1 RELEASE_BENCH_BUDGET_SECONDS=0
export TAG=local-qualification COMMIT="$SOURCE_SHA"
./testbed/bench/release.sh --out "$EVIDENCE_DIR/bench-report.md" |
  tee "$EVIDENCE_DIR/canonical.log"

# Each run.sh invocation starts a fresh Compose cluster and WAL. A failed
# command stops the qualification; keep its raw files for diagnosis.
for scenario in receipt_vertex_put_admission_lookup \
  receipt_vertex_delete_admission_lookup receipt_admission_lookup \
  receipt_edge_add_admission_lookup; do
  ./testbed/bench/run.sh "$scenario" | tee "$EVIDENCE_DIR/$scenario.log"
done

# release.sh's exit 0 alone does not establish that an exploratory budget
# completed all eight scenarios. Its detailed producer tables also contain
# pass rows, so validate only the eight scenario rows in the Summary section.
awk -F '|' '
  /^## Summary$/ { in_summary=1; next }
  in_summary && /^## / { exit }
  in_summary && /^\| `/ {
    rows++
    if ($3 != " `pass` " || $6 != " `pass` ") failed=1
  }
  END { exit (rows == 8 && !failed ? 0 : 1) }
' "$EVIDENCE_DIR/bench-report.md"
! grep -Fq '## Truncated' "$EVIDENCE_DIR/bench-report.md"
while IFS= read -r scenario; do
  case "$scenario" in ''|\#*) continue ;; esac
  runs=(testbed/bench/out/"$scenario"/*)
  test "${#runs[@]}" -eq 1 && test -f "${runs[0]}/report.md"
  for gate in leak_gate perf_gate; do
    jq -e '.verdict == "pass"' "${runs[0]}/${gate}.json" >/dev/null
  done
done < testbed/bench/release-scenarios.txt
for scenario in receipt_vertex_put_admission_lookup \
  receipt_vertex_delete_admission_lookup receipt_admission_lookup \
  receipt_edge_add_admission_lookup; do
  runs=(testbed/bench/out/"$scenario"/*)
  test "${#runs[@]}" -eq 1 && test -f "${runs[0]}/report.md"
  for gate in leak_gate perf_gate; do
    jq -e '.verdict == "pass"' "${runs[0]}/${gate}.json" >/dev/null
  done
done
test "$(docker image inspect --format '{{.Id}}' "$LANTERN_IMAGE")" = "$EXPECTED_LANTERN_IMAGE_ID"

# Write a success manifest only after all 12 gates passed. Keep the raw
# evidence private; put only hashes and aggregate verdicts on public Issues.
jq -n --arg sha "$SOURCE_SHA" --arg image "$EXPECTED_LANTERN_IMAGE_ID" \
  --arg host "$(uname -srm)" \
  --arg container "$(docker info --format '{{.OSType}}/{{.Architecture}}')" \
  --argjson vm_memory_bytes "$(docker info --format '{{.MemTotal}}')" \
  --arg cpuset "$LANTERN_BENCH_CPUSET" --arg generator "native host: $(command -v ghz)" \
  '{source_sha:$sha,image_id:$image,host:$host,container_platform:$container,
    docker_vm_memory_bytes:$vm_memory_bytes,shared_replica_cpuset:$cpuset,
    generator_placement:$generator,canonical_scenarios:8,receipt_families:4,
    verdict:"pass",raw_artifact_hashes:"SHA256SUMS"}' > "$EVIDENCE_DIR/manifest.json"
if command -v shasum >/dev/null 2>&1; then
  hash=(shasum -a 256)
else
  hash=(sha256sum)
fi
find "$EVIDENCE_DIR" -type f ! -name SHA256SUMS -print0 |
  xargs -0 "${hash[@]}" | sort -k2 > "$EVIDENCE_DIR/SHA256SUMS"
"${hash[@]}" "$EVIDENCE_DIR/SHA256SUMS"
```

Run these commands under `set -euo pipefail` so a failed gate cannot be hidden
by `tee`; if any command fails, preserve the failed artifacts but **do not
tag**. The `run.sh` exit status also enforces the admission/lookup counts and
any configured semantic/metric gates. The `SHA256SUMS` list covers the
aggregate report, all 12 per-scenario raw artifact trees, console logs,
host conditions, manifest and frozen source. Retain the originals durably;
record the hash of `SHA256SUMS`, the source SHA/image ID, generator placement,
platform, CPU/memory budget, all 12 verdicts, and any contradictory
hosted-platform result in the release-tracking Issue. The failed GitHub
Actions-hosted Linux/amd64 run is an observed hosted benchmark failure, not
proof of an x86-only defect or merely runner noise. A passing local run on
one platform does not establish production availability on either platform.

The exit code folds together the leak gate and any declared metric, semantic,
and perf gates (`0` = all pass, `1` = at least one failed). Unless `KEEP_UP=1`,
teardown of the named Compose project must also succeed; a teardown failure
disqualifies the run without masking an earlier gate failure. Run artifacts
are written under `testbed/bench/out/<scenario>/<UTC-timestamp>/`.
Runs already failing before teardown also retain a private Server log.
After host clients are reaped and before container stop, failure-only capture
records terminal State and separate bounded stdout/stderr for the exact owned
relay and time fixture. These private records report capture errors explicitly;
capture cannot mask the primary failure or prevent owned cleanup.
Compose replica prefixes identify the emitting node; keep them when interpreting
Snapshot or publication failures. Raw logs may contain runtime context and stay
outside public artifacts. Publish their hashes and bounded findings instead.

## Host-only GC and periodic-backup stress (#1183)

`gc_stress.sh` and `backup_stress.sh` are opt-in measurements outside the
Compose release sweep. They seed an in-process 100,000-vertex, approximately
3.2-million-edge graph on the host, because a container VM with less than that
working-set capacity would change the result. Both scripts require committed
source and record its exact SHA, Go version, CPU/RAM, container-VM memory (when
available), scale, intervals, and offered rates in `host.txt`. They write
content-free timing/count artifacts under ignored `testbed/bench/out/`.
There is currently no running Lantern deployment, so these runs establish
synthetic signals only; observed deployment scale, production latency, and
operator RPO remain unavailable.

```bash
./testbed/bench/gc_stress.sh
./testbed/bench/backup_stress.sh
```

The GC script runs three repetitions of at least 100 direct `GraphCache.Watch`
ticks per configuration: uniform degree 32 or a skewed graph with one
100,000-head tail, crossed with a full sweep or a 5,000-tail budget. Uniform
seeds 3,200,000 edges; skew seeds 3,199,969 because the hub has 100,000
heads and each other tail has 31. This exposes how a tail budget handles one
high-degree tail at nearly identical total edge count. Five expiry bands add
live-edge contributions and short-only buckets; two vertex-deletion bands
create dangling edges. The JSON retains every timestamped raw tick duration,
scanned tail/edge count, expired contributions (including those compacted in
still-live buckets), zero/dangling bucket removals, backlog, and a sampled
expiry-to-physical-reclamation lag. `report.md` recomputes `gc_p99` from the
raw ticks, separately for every run. The script also keeps ten target-scale
single-flush benchmark repetitions and `benchstat.txt`; those benchmarks are
not substitutes for a tick p99. `benchstat` must be installed in `PATH`.

The backup script drives the actual periodic `Backupper.Run` for four
30-second intervals while named `GetVertex`, `ScanVertices`, and `Illuminate`
read producers each offer 200 calls/s, and `PutVertex` offers 50 calls/s, over
the real Connect/h2c path. The JSON records each completed periodic tick's
start/end and materialization/send/finalization durations and every call's
start/end, latency, and success status. Each tick is paired with equal-length
before/during/after windows per producer. A comparison requires at least 100
successful calls and no errors in each window; missing data yields `unknown`,
and a failed periodic attempt breaks a three-tick streak.
The synthetic read trigger fires only if one named read producer's during p99
exceeds twice its matched before p99 for three consecutive completed ticks.
The local results do not establish an operator RPO or deletion-survives-restore
requirement and cannot by themselves justify WAL.

The environment variables `GC_VERTICES`, `GC_DEGREE`, `GC_TICKS`,
`GC_INTERVAL_MS`, `GC_REPETITIONS`, `GC_SHAPES`, `GC_BUDGETS`, and the
optional `GC_RESOURCE_TRACE=1` (compile the test once, then capture per-run
stress-process CPU/maximum-RSS via `/usr/bin/time`), plus the
corresponding `BACKUP_VERTICES`, `BACKUP_DEGREE`, `BACKUP_INTERVAL_MS`,
`BACKUP_READ_RPS`, `BACKUP_WRITE_RPS` override script defaults for a
predeclared run or smoke check. If target-scale setup exceeds host resources,
record that limit explicitly; do not treat a smaller run as equivalent.

## Mixed edge reset/Add microbench (#1203)

The content-free [raw baseline](evidence/issue-1203/base.txt) and
[candidate](evidence/issue-1203/candidate.txt) outputs were captured on an
Apple M3 Max (`darwin/arm64`, one benchmark CPU, three 500ms repetitions),
comparing main `40b5127` with the #1203 candidate `5b0f8a4`. They are
exploratory host measurements, not a release floor or production RSS sample.

| Case | Baseline median | Candidate median | Baseline B/op | Candidate B/op |
| --- | ---: | ---: | ---: | ---: |
| Local `AddEdge`, 1k vertices | 316.8 ns | 316.8 ns | 233 | 312 |
| Local `AddEdge`, 10k vertices | 427.8 ns | 443.8 ns | 265 | 400 |
| Local `AddEdge`, 100k vertices | 601.9 ns | 644.7 ns | 322 | 432 |
| Existing hot edge `AddEdge` | 283.4 ns | 266.6 ns | 348 | 504 |
| Causal Add batch of 64 | — | 5.566 µs | — | 12,256 |
| Replication Snapshot, 1k edges × (Put + 3 Adds) | — | 918.2 µs | — | 1,248,440 |

The local Add allocation count stayed at two per operation; the HLC stored
with every contribution increased allocation bytes. One HLC occupies 32 bytes,
so 3.2 million retained single-contribution edges add about 102 MB of row
payload before slice-capacity and allocator effects. The three samples do not
establish a significant latency change. The on-demand
[`mixed_edge_reset_add.yaml`](scenarios/mixed_edge_reset_add.yaml) scenario
exercises the same operation mix over three replicas and is intentionally not
in the short release sweep until a stable post-change baseline exists.

## Receipt admission and lookup gates (#1442, #1399)

The four independent scenarios each offer 100 operation pairs/s across three
replicas. Each admission is immediately followed by `GetReceiptStatus` on the
**same endpoint and operation ID**. The driver checks `CONFIRMED`, the matching
operation/call IDs, canonical intent SHA-256, deadline, item coordinates, and
the original **present typed result arm**. Conditional `PutVertex` uses unique
live keys and requires `APPLIED_AND_LIVE`; exact absent `DeleteVertex` and
`DeleteEdge` require their respective `existed=false` arms. `AddEdge` supplies
a distinct explicit 24-byte ContribID and a finite float32 source weight;
pairs share an edge on one replica to exercise derived infinity while keeping
per-edge contribution counts bounded. The driver's float32 comparison uses
original response/result bits, not numeric equality, so it retains NaN and
infinity results. Each endpoint must advertise the selected family in
`supported_mutations`. Missing/wrong arms, digests, capabilities, or responses
become non-OK outcomes and fail the run.

ghz cannot safely generate one canonical binary operation ID and reuse it in
another producer, so each scenario selects one of the four allow-listed
receipt family drivers. Each emits the ghz summary shape consumed by the
existing perf evaluator and report renderer. The harness provisions a fresh
authenticated durable-WAL cluster with fresh per-replica origin IDs and rejects
`SKIP_UP=1`. Before any reset, startup proves the named bench Compose project
has no existing containers, volumes or network. An existing project, including
a preceding `KEEP_UP=1` run, is refused without deleting its resources; choose a
new project for the next family. Cleanup removes only the current invocation's
owned project. Before warmup
and after the run, it checks the configured image reference, full embedded
source commit, immutable image ID, Compose project/service labels, restart
count, and all three container image IDs; it rejects changes or missing
provenance. `image_provenance_pre.json` and `image_provenance_post.json` are
saved in each distinct scenario output directory. Nightly pins one image ID
and source SHA across all four runs. Existing scenarios retain graph-only
defaults.

All fresh cohorts use [`compose.native.yml`](compose.native.yml), with three
explicit workloads and a distinct, unpublished private mTLS plane. The
`authfixture -compose` helper generates a host-private CA, separate public and
private leaf keys, operator-signed membership and per-node configuration. OFF
keeps its public h2c baseline; protected receipt runs use trusted HTTPS and a
canonical `lnt_m1_` machine credential bound through the native `fixture_data`
Role. Only that workload's files are mounted read-only. The operator private
key and client token file remain outside all Server containers. DNS names
locate only the signed fixed origins; they never approve membership.

The protected receipt EdgeAdd scenario prepares its live Vertex endpoints with
ordinary authorized `PutVertices` before warmup and the PRE memory snapshot.
Preparation uses one replica and verifies the exact values on every configured
replica through `GetVertices`; it consumes no receipt rows. Both phases have
distinct random nonces and a private, bounded endpoint-plan file binding their
original duration/rate to the discovered endpoint identities and receipt policy.
At 100 pairs/s this prepares at most 501 warmup tails, 2251 steady tails and one
shared head. The duration-derived operation limits are 1001 and 4501; a ready
ticker cannot schedule beyond the prepared set while waiting for its duration
timer. These limits fit the existing 8000-entry receipt policy. Preparation
failure stops the run. Each phase rechecks the plan and live endpoint values;
the server still evaluates current authorization, endpoint liveness and receipt
deadlines on every operation. Preparation RPCs are outside the measured Add and
lookup counts. The expected intent includes the protected Edge-only effect;
the probe does not accept a legacy implicit-endpoint receipt digest as a fallback.

The graph-only fixture explicitly selects a 128 MiB logical Snapshot staging
allowance and a 32 MiB frame limit through the three
`LANTERN_GRAPH_SNAPSHOT_SPOOL_*` settings. These are local fixture choices, not
server or operator deployment defaults. The allowance includes payload, indexes,
sort/merge workspace and charged metadata; it does not reserve filesystem space
or bound decoded-frame/whole-process memory. Quota or disk errors fail closed.
Receipt fixtures omit those settings and the graph staging overlay, preserving
their six existing state/backup volumes. No existing state, backup, runtime or
temporary directory is selected as staging storage.

An owned operator helper renews the same membership every two minutes. Each
successor preserves the domain/member identities and increments its version;
expiry, drift, helper death or unknown restarts disqualify the run. The
1h certificate/credential lifetime still bounds a diagnostic invocation.
Explicit chaos restart changes fresh→resume/restart and gives an ephemeral
graph a new transport origin. Its exact old/new container IDs are recorded;
undeclared recreation or a changed image is rejected. `KEEP_UP=1` retains
private files and the project's staging volumes for operator teardown, but
stops the helper, so membership expires within ten minutes. Ordinary restart
retains the staging volume and its stable owner file; the fresh-empty initializer
is not rerun. Successful named-project `down -v` removes its new staging volumes
alongside its existing volumes. Never archive `.peer-tls.*` with public reports.

[`compose.receipt-tls.yml`](compose.receipt-tls.yml) disables pprof and removes
metrics port publication. An owned `diagnostics_bridge.py` binds only host
loopback and reads `/metrics` and `/readyz` through `docker exec` into the
exact immutable container IDs. It cannot forward profiling, writes or arbitrary
paths. Protected runs require `LEAK_GATE_ONLY=1`; they skip Prometheus/pprof
extras, while retaining all producer, semantic, steady resource and perf gates.
OFF profiling remains explicit and its diagnostic ports bind host loopback.

Before warmup and after the verdict, every cohort proves its native
configuration, separate per-workload mount, immutable image/source commit,
project/service identity and expected lifecycle. Protected runs additionally
verify the public CA, live leaf fingerprint and HTTP/2 without redirects.
`image_provenance_*.json`, `peer_tls_*.json` and `membership_renewal.json` contain
content-free evidence; machine tokens and native bootstrap files stay private.

Public CDC scenarios now use `WatchChanges` and the compiled `changeprobe`
application consumer, not the private replication RPC. It processes a complete
frame before committing its opaque cursor, resumes on a bounded stream deadline
and records gap/rebootstrap separately. Permission, availability or malformed
progress failure disqualifies the scenario. Producer load/durations/floors are
unchanged; the new public projection is not a byte-for-byte comparison with
historical complete mutation delivery. Native pumps continue full private
replication independently.

The protected receipt memory contract uses the explicitly paired #1958 matrix.
One normal immutable image runs the eight canonical and four receipt scenarios.
A separately identified image from the same source, built with
`lantern_bench_heap`, runs the four receipt scenarios for retained memory.
The normal production binary has no measurement control, signal handler or
forced-GC behavior. OIDC still rejects pprof; its diagnostics bridge still
forwards only metrics/readiness. Each lane keeps its own immutable image.

Normal receipt reports use `natural_reference_v2`. They keep the natural PRE/POST
memory snapshots and unforced steady samples on all three replicas every 5s.
Complete finite integral samples
remain mandatory (at least nine rounds over 45s, with no gap above 7.5s).
Performance and +40 MiB natural steady growth remain blocking. The natural POST
+32 MiB comparison is an observation, separate from the retained verdict.
A normal-only result requires its measurement pair and cannot declare overall
memory acceptance.

The #1960 normal receipt contract uses `receipt_goroutine_operational_v1` for
VertexPut, VertexDelete, EdgeDelete and EdgeAdd. Each process must remain at or
below 128 goroutines at PRE, every steady sample, the existing natural POST and
both fixed later readings. The baseline-relative residual must also stay at or
below +15 at that existing POST and both later readings. `goroutine_policy.json`
records the raw-validated goroutine component;
`leak_gate.json` exposes `goroutine_checks_verdict` and
`natural_steady_memory_verdict`. The combined normal result also requires the
unchanged workload, performance, native and natural steady memory gates. The eight
nonreceipt scenarios and the tagged memory procedure retain their existing
contracts. Old reports retain their old meaning, including the old steady +15
check; a new protocol does not reclassify saved results.

The 128 ceiling is an engineering allocation per process: 32 resident owners,
32 public-work/transport owners, 16 authority owners, 16 peer-work owners and
32 reserve. The four families share one cluster-wide pool of 12 serial
admission/status pair workers and the same three-node traffic profile. Admission
binds those three endpoints, 200 aggregate requested RPC/s, warmup10s/steady45s/
cooldown10s, and the existing WAL, performance and native configuration. A changed
profile cannot inherit the ceiling by scenario name. The families retain
different mutation, indexing and retained-state behavior, which each require
fresh family-specific results. The allocation is neither a proven
owner bound nor a statistical tail estimate. The +15 residual allowance retains
the previous sensitivity; it does not assert that smaller leaks are absent.

The collector uses the exact existing PRE and steady samples. It anchors t0
after the existing 10s cooldown, semantic verification and completion/reaping of
the declared client work, immediately before the unchanged natural POST memory
snapshot. That existing point is ordered after t0 and uses three sequential
per-node scrapes; it is not an atomic t0+0 observation. Each later capture must
start and finish within its fixed window: [t0+5s, t0+8s] and [t0+10s, t0+13s].
Their nominal targets correspond to approximately steady-end+15s/+20s, rather
than 5s/10s from the last RPC. Hash-bound warmup and
steady coordinator-return records prove the two synchronous client phases have
returned; the two logical RPC report families are not two additional producers.

Neither later reading postpones the original memory snapshot or triggers GC.
A missed window, unmatched process/configuration/peer/observer premise or missing
work evidence is INCOMPLETE, without another reading or a shifted t0. Native
owners and connections stay alive. Passive gauges describe only their existing
stream/peer coverage. This bounded screen does not prove global idle, complete
owner joins, leak freedom or OOM safety. Its blind spots include sub-5s spikes,
leaks within +15, unobserved owners, slower growth and a few goroutines retaining
large allocations. Memory criteria remain independent. The normal contract
rejects tagged measurement and census images.

The single declared EdgeDelete pilot checked collection/composition mechanics;
it does not qualify the other families or carry into release acceptance. Adoption
requires reviewed source, ordinary checks and fresh final-source normal and
paired-memory results. See #1960 for the scope and retained historical failures.

Measurement runs use `symmetric_post_gc_2_v1`. After warmup and after the unchanged
load/cooldown, each target performs exactly two synchronous completed full GC
cycles, then immediately reads MemStats and last-marked-live metrics before
encoding or native bracket-B allocation. The same procedure applies to BOTH
cuts; there is no minimum selection, post-only collection or forced GC during
steady load. Two cycles give unused `sync.Pool` primary/victim storage the same
aging opportunity at each cut. Active pools, legitimate retained state and
background workers remain; this is neither all-pools-empty nor leak-freedom
proof. HeapAlloc after GC can include contemporaneous background allocation and
is distinct from last-marked-live bytes. The retained delta limit stays +32 MiB.

The tagged process accepts only fixed private phase files under
`/run/lantern-bench-heap`, with a nonce and exact process/source/image bindings;
SIGUSR1 wakes it but is not completion proof. Each cut has a 25s target budget
and a 30s controller budget for all three parallel targets. Relevant work must
drain within 10s and remain observed stable for 250ms, sampled every 50ms.
The cut checks final submitted work and matching committed origin frontiers,
coherent public/non-Subscribe/internal Apply activity, direct pending/native
state, and both GC returns. All locks are released before GC. Maintained native
Subscribe streams remain alive. These observations bound relevant committed
work; they do not assert global fanout, transport or background-goroutine idle.
Drift, missing proof or deadline expiry is INCOMPLETE, with no repeated cut or
fallback and with existing owned shutdown/custody still required.

Before native security initialization, every measurement original binds its
canonical private host directory to its actual Docker request/effective paths.
Literal paths use an identity-bound fast path; differing representations require
one bounded read-only directory-stat probe per original, with at most two fixed
pairs, before any `init-security`. No prefix stripping or operator path mapping
is accepted. Clones may use only that original's frozen same-directory set;
a new representation fails without another probe. Host device/inode/type,
UID/GID and mode remain fixed through session writes and both cuts; directory
mtime and contents are expected to change. Case-local byte-hashed watched
mount observations and raw stat evidence let the collector, Go evaluator and
pair consumer independently verify the premise. Serving authentication Env is
checked in memory and is excluded from these retained mount observations.
Historical literal-path refusals remain failures under their original contract.

Whole-matrix acceptance requires the normal twelve cases and all four retained
pairs, with matched source/tree/scenario/workload/TTL/retention/capacity/runtime
bindings and separate image identities. Tagged performance does not qualify
normal-image performance, and tagged measurements do not assert binary equality
or directly measure normal-binary retained heap. Baseline GC also changes pool
and cache state before the tagged steady phase, so this is a prospective protocol
change. Historical `natural_gc_single` FAILs retain their original meaning and
raw evidence. OFF snapshots keep their existing outside-steady GC protocol.
Peak process/container RSS and CPU comparisons remain separate #1610 evidence.

The same receipt scrapes also retain bounded aggregate context: GC count,
last-GC time and next-GC target; receipt entries, logical bytes and oldest
deadline; and mutation-log capacity, fill ratio and evictions. These observations
do not enter the verdict or measure post-GC live heap. Receipt logical bytes
exclude Go container overhead, and retained graph/index/history state may grow
with successful writes. No additional scrape or forced GC is used for this
context.

The legacy `LANTERN_BENCH_NATURAL_GC_OBSERVE=1` diagnostic is scoped only to
a historical `natural_gc_single` run; the new paired protocol refuses this
opt-in. Its existing saved-record contract is retained below. It applies only to
a fresh protected Edge Add cohort with `KEEP_UP=0` and `LEAK_GATE_ONLY=1`.
It requires an explicit harness commit in `LANTERN_BENCH_DIAGNOSTIC_SOURCE_ID`
and the original campaign's Unix UTC and monotonic deadlines in
`LANTERN_BENCH_DIAGNOSTIC_DEADLINE_UTC` and
`LANTERN_BENCH_DIAGNOSTIC_DEADLINE_MONOTONIC`. The driver must enforce a total
600-second window including preparation and retain 120 seconds for cleanup.
After the ordinary pre/steady/post samples and gates, `formal_result.json`
seals their hashes and original exit status, including a frozen
`formal_report.md` copy; ordinary cleanup may still annotate `report.md`.
Only a formal PASS or the known post-cooldown 32 MiB heap failure admits the
diagnostic. It retains all 18 three-node observations at fixed 10-second offsets
over 180 monotonic seconds, with at most 54 two-second metrics requests, no added workload
RPCs and no explicit GC. `natural_gc_observation.json` records completion or
missingness separately. A diagnostic failure cannot change the sealed formal
verdict; it does make the overall execution incomplete. No later value or
minimum replaces the formal post sample. Cleanup follows the bounded
observation even when no GC occurs. This does not qualify one-hour receipt
expiration, full-capacity memory, or a leak/no-leak claim.

The steady metrics sampler owns a separate HTTP transport, closes its idle
connections and requests cancellation of unused pending dials when sampling ends;
the per-request timeout remains 2s. For the owned IPv4 operator bridge only,
`-metrics-operator-loopback` pins each configured `localhost:<port>` socket dial
to `127.0.0.1:<same port>`. The URL, HTTP Host, report identity, proxy selection,
node ordering and authenticated workload transport remain unchanged. Generic
standalone collectors do not opt in, preserving their ordinary resolver and
IPv6 behavior. This removes unnecessary name resolution from this known local
route; it does not establish the cause of a previous timeout.
At phase completion, stderr retains at most
the first failed admission and first failed lookup as fixed categories for status,
source and message class, even if metrics or coverage fails first. It excludes
raw errors and request/response content. A wire-error category or exact message
class does not prove a server
execution branch or whether the mutation committed. These diagnostics do not
change the paired summaries, verdicts or retries.

Five preliminary **Edge Delete-only** Compose runs on the synthetic-parent stack (Apple M3 Max,
`darwin/arm64`), recorded in
[local scenario evidence](evidence/issue-1399/scenario.txt), sustained
98.05-99.97 operations/s per producer with zero non-OK results. Admission p99
ranged from 5.24-171.02 ms and lookup p99 from 2.85-62.59 ms; the fourth run
captured substantial local container-host contention without losing
throughput or semantic correctness. The producer floors of 75 operations/s
leave 25% offered-rate headroom. The 500 ms admission and 200 ms lookup
ceilings retain 2.9x and 3.2x headroom over those worst local p99s, following
the harness's ≥2x shared-runner rule. These are step-change gates, not
production capacity claims.

Paired direct service benchmarks for all four families compare receipt-less
and admitted receipt writes in the same durable FileWAL runtime, with request
construction outside timed work. Vertex Put uses unique conditional live
keys; both Delete families use absent identities; Add uses explicit finite
contributions on unique edges. Four matching confirmed-status lookup
sub-benchmarks measure each typed result. Historical Edge Delete medians in
[local benchmark evidence](evidence/issue-1399/direct.txt) were 9.494 ms/op for
receipt-less Edge Delete, 14.271 ms/op for admitted receipt Edge Delete
(+4.777 ms, +50.3%), and 5.065 ms/op for confirmed receipt lookup. These
host-only numbers quantify only that synthetic-parent Edge path; the
verified real-wire
manual qualification scenarios own enforceable thresholds. **No final four-family quiet-host
measurements exist yet.** Neither the historical direct timings nor Compose
runs qualify the final integrated receipt/SDK stack; fresh uncontended
exact-image measurements are required for #1399.

```bash
(cd server && go test ./service -run '^$' \
  -bench 'Benchmark(PublicReceipt(VertexPut|VertexDelete|EdgeDelete|EdgeAdd)Admission|ReceiptStatusLookup)$' \
  -benchmem -benchtime=500ms -count=5)
```

## Scenarios

| File | What it stresses |
| --- | --- |
| `write_heavy.yaml` | sustained `PutVertex` from many clients |
| `read_heavy.yaml`  | sustained `GetVertex` on pre-warmed keys |
| `mixed_rw.yaml`    | 50/50 `PutVertex`/`GetVertex` (two parallel ghz runs) |
| `batch_put_vertices.yaml` | bulk write path via `PutVertices` |
| `addedge_contention.yaml` | repeated `AddEdge` on the same (tail, head) — block/mutex hotspot probe |
| `illuminate.yaml`  | graph traversal under moderate RPS |
| `scan_prefix.yaml` | paginated prefix scans |
| `ttl_churn.yaml`   | bounded born-expired Put/Delete churn over a 4096-key ring; proves causal-floor retention, slot reuse, and heap stability under the explicit causal-metadata budget (#1204) |
| `replication_soak.yaml`   | producer on r0, `Subscribe` consumers on r1/r2 for 10m |
| `chaos_kill_replica.yaml` | mid-load `docker kill` + restart of one replica |
| `many_subscribers.yaml`   | N concurrent `Subscribe` streams across replicas (#240 pubsub probe) |
| `search_churn.yaml`       | PutVertex (short TTL) + all advanced SearchVertices families; per-producer perf plus semantic/index-decay gates (#1048) |
| `search_qualification.yaml` | short fresh-cluster tag-SHA gate; deterministic Search semantics, TTL cleanup, metric, leak, and producer perf contracts (#1063) |
| `prefix_surface.yaml`     | ScanVertexKeys + ScanEdges + CountVerticesByPrefix + DeleteVerticesByPrefix fan-out (#704) |
| `edge_contrib_idempotent.yaml` | Bounded contribution-keyed Add and targeted Delete (singular/plural), including D4 tombstone churn (#706, #1528) |
| `replication_apply_churn.yaml` | replicated write churn; asserts `lantern_vertex_hlc_entries` returns to baseline (#700, #705) |
| `mixed_edge_reset_add.yaml` | on-demand three-replica Put/Delete/Add churn on bounded edge identities; measures reset-aware contribution cost (#1203) |
| `receipt_vertex_put_admission_lookup.yaml` | on-demand conditional Vertex Put receipt admission plus same-operation typed lookup |
| `receipt_vertex_delete_admission_lookup.yaml` | on-demand absent Vertex Delete receipt admission plus same-operation typed lookup |
| `receipt_admission_lookup.yaml` | on-demand absent Edge Delete receipt admission plus same-operation typed lookup (#1442) |
| `receipt_edge_add_admission_lookup.yaml` | on-demand finite-source, contribution-keyed Edge Add receipt admission plus bit-exact typed lookup |
| `backup_under_load.yaml`  | BackupSnapshot concurrent with sustained writes — on-demand only, not in release sweep (#707) |
| `shared_ranking.yaml` | Diagnostic shared-corpus Put/Delete/Search plus BM25 BFS/PPR/community; outside the release sweep; [component evidence](evidence/issue-1612/shared-ranking/README.md) does not qualify production OIDC |
| `broad_illuminate.yaml` | Six named traversal producers over a verified 64-way/3-hop walk and planted dense communities; preflight rejects a collapsed topology (#994) |

Each YAML declares the phases (`warmup`, `steady`, `cooldown`), the load
target (`call` + `data_template` for ghz, or an allow-listed custom driver),
optional `subscribe` and `chaos`
blocks, and the leak-gate thresholds (`goroutine_max_delta`,
`heap_alloc_max_delta_mb`; receipt scenarios also declare
`steady_heap_alloc_max_delta_mb`). For OFF snapshots, the post-cooldown gate
evaluates against `heap_alloc`
(post-GC live bytes), forcing a `runtime.GC()` via
`/debug/pprof/heap?gc=1` before each snapshot so the reading reflects
allocator memory after GC rather than span-level allocator headroom. Protected
receipt scenarios use the paired normal-reference and symmetric completed-GC
protocols described above. Natural POST observations and the +32 MiB retained
decision remain distinct. The legacy field
name `heap_inuse_max_delta_mb` is still accepted for backward
compatibility but produced false-positive verdicts under sustained
churn — see issue #248.

For fan-out targets, each `target.calls[]` entry may declare its own positive
integer `rps`. Otherwise the phase RPS is divided equally. Use explicit RPS
when one producer must not borrow throughput from another; `search_churn` does
this so a fast `PutVertex` cannot conceal a slow Search family.

RPC benchmark payloads that omit `Vertex.expiration` / `Edge.expiration`
write permanent entries. `cluster.default_ttl_seconds` configures the server's
default TTL for cache-default paths; it does not rewrite explicit RPC payloads.
Scenarios that need decay through `ghz` should include an `expiration` template
in `data_template`.

Every ghz `data_template` is schema-checked at ordinary `go test ./...` time by
`testbed/bench/scenarios_gate_test.go`: it renders each template with
ghz-style data and protojson-unmarshals the result against the request
message resolved from the `call` name, so a proto change that orphans a
scenario fails the schema PR instead of a later manual benchmark (#934). If you
retire or rename a wire field, migrate every scenario that sends it in the
same PR. The receipt driver's template-less calls still resolve against the
wire descriptors, and a dedicated contract test pins its driver, two RPCs,
capacity, bounded phases, perf gates, fresh-cluster restriction, manual-workflow
wiring, image provenance, sequential fresh-WAL scheduling, and release-list
exclusion for all four families.

`broad_illuminate` additionally has a semantic topology gate. Before warmup,
the harness seeds a deterministic graph, then verifies the requested 64-way
three-hop BFS shape, tuned PPR touch count, and a 32-member weak-bridge
community reduced to a directed arborescence. Its six named producers are
gated independently as well as in aggregate, so a cheap BFS cannot hide a
PPR or community regression.

### Perf gate (`perf_gate:` block)

A scenario MAY additionally declare throughput/latency floors over its
steady-phase producers (#935):

```yaml
perf_gate:
  min_steady_rps_total: 1500   # floor on SUM of producer rps
  max_p99_ms: 500              # ceiling on the WORST producer p99
  max_non_ok_ratio: 0.02       # ceiling on Σnon-OK / Σcount
```

Every key is individually optional — gate only the metrics that are stable
for the scenario. The aggregation matches the release summary table
(`testbed/bench/release`), the verdict lands in `perf_gate.json`, and a
`fail` folds into run.sh's exit code exactly like the leak gate. Local
pre-tag qualification enforces it; optional hosted diagnostics use the
same thresholds but do not decide release acceptance.

For a fan-out scenario, `perf_gate.producers` may define the same thresholds
per named `target.calls[]` producer:

```yaml
perf_gate:
  producers:
    ppr_tuned:
      min_steady_rps: 50
      max_p99_ms: 1000
      max_non_ok_ratio: 0.02
```

All named producer gates are conjunctive with the aggregate gate and appear
as separate rows in `perf_gate.json` and the rendered report. A nonempty
producer summary without p99, or with duplicate or invalid latency percentiles,
fails closed rather than treating missing latency as zero. A reported throughput
must also agree with whether the producer recorded any calls. Null status counts
and duplicate JSON keys (including nested percentile fields) are rejected
before status and latency gates are evaluated.

#### Typed recovery lifecycle (`lifecycle_gate:` block)

A scenario that intentionally drives a bounded fail-closed recovery may split
one typed Search lifecycle reason from the generic error budget without
weakening `max_non_ok_ratio`. `search_churn` uses this for derived-index gap
recovery:

```yaml
lifecycle_gate:
  reason: SEARCH_INDEX_INCOMPLETE
  max_ratio: 0.10
  producers:
    broad_posting:
      metric_labels:
        mode: server
        phrase: "no"
        fuzziness: "0"
        prefix_terms: "no"
        prefix_present: "no"
```

The harness snapshots the bounded `lantern_search_calls_total` counter
immediately before and after steady load. For each configured producer, its
request-option labels and assigned replica select the
`outcome="failed_precondition",reason="index_incomplete"` delta. That typed
count is accepted only when it fits inside the producer's ghz
`FailedPrecondition` count; missing snapshots, decreasing counters, invalid
selectors, or an overlarge join fail closed. No human-readable error message is
parsed.

The verified typed count is checked against `max_ratio` both per producer and
in aggregate over lifecycle-eligible producers. It does not consume
`max_non_ok_ratio`; every unmatched `FailedPrecondition`, every other status,
and end-of-run transport error remains in that unchanged unexpected-error
budget. `perf_gate.json` and `report.md` retain raw, expected-lifecycle, and
unexpected counts so a green verdict remains diagnosable.

### Lifecycle metric gate (`metric_gate:` block)

`metric_gate.metrics` compares each replica's unlabeled gauge in the pre/post
runtime snapshots. Thresholds on a metric are conjunctive:

```yaml
metric_gate:
  metrics:
    lantern_search_index_docs:
      max_increase: 256
      max_ratio: 1.25
    lantern_search_index_expiration_purged:
      min_increase: 1
    lantern_search_index_retained_ratio:
      max_post: 3
    lantern_search_index_healthy:
      min_post: 1
      max_post: 1
```

Available comparisons are `max_increase`, `max_ratio`, `min_increase`,
`min_post`, and `max_post`. Missing or non-finite samples fail closed. Ratio
growth from a zero baseline also fails when post is positive; pair ratio and
absolute limits intentionally rather than using ratio as a noise filter. The
verdict is written to `metric_gate.json` and folds into `run.sh`'s exit code.

### Search semantic gate (`semantic_gate:` block)

`semantic_gate.kind: search` seeds a deterministic fixture before warmup and
checks every replica both before and after steady load. It proves a persistent
hit, deleted/expired absence, stable ordering, key-prefix filtering, phrase,
fuzzy-1/2, prefix-term, ALL, and MIN_SHOULD behavior. It also walks a five-page
endpoint-sticky cursor chain with FULL_VERTEX snapshots, rejecting duplicate,
gap, truncated-tail, or projection races under concurrent index churn. An
HTTP-OK response with empty hits therefore fails. The bounded `semantic_pre.json` and
`semantic_post.json` artifacts contain only phase, replica endpoint, check
count, and verdict—never query text, prefixes, keys, or values.

Sizing rules (all eight release scenarios carry a block sized this way):

- The existing floors have **≥2x headroom** over their historical hosted
  baseline — they catch step-change regressions (accidental O(n²), lock
  convoy, a dropped fan-out), NOT single-digit-% drift. Keep them unchanged
  for the first declared local profile; do not transplant a single local
  result to another platform or tighten a floor to "just below last run".
- **Gate p99 only where it is stable.** Scenarios whose offered load
  deliberately saturates the runner (`broad_rw`, `broad_mutate`) have
  queue-depth p99 — observed varying 7x night-over-night — so they gate rps
  and non-OK only.
- **Re-baseline after changing a scenario's mix.** Adding/removing a
  `target.calls` entry re-splits RPS for entries without an explicit `rps` and
  always shifts the offered mix; drop the affected ceilings in the same PR and
  restore them from multiple controlled local measurements on the
  declared platform, with headroom and a reviewed comparison.
- When a perf floor legitimately moves (accepted throughput/latency
  trade-off), adjust it **in the same PR** and say so in the PR body — same
  contract as the coverage-floor ratchet in CONTRIBUTING.md.

## Output layout

```
testbed/bench/out/<scenario>/<ts>/
├── report.md                       # rendered summary (start here)
├── leak_gate.json                  # verdict + per-replica deltas + thresholds
├── metric_gate.json                # per-replica pre/post gauge contracts (when declared)
├── semantic_{pre,post}.json        # bounded Search semantic verdicts (when declared)
├── perf_gate.json                  # perf verdict + thresholds + observed (only when perf_gate: declared)
├── image_provenance_{pre,post}.json # receipt runs: pinned image and containers
├── peer_tls_{pre,post}.json          # receipt runs: public TLS fingerprints (no keys)
├── runtime_pre.json                # runtime values + unlabeled lifecycle gauges, after warmup
├── runtime_post.json               # same, after cooldown
├── ghz_warmup_<endpoint>.json      # raw ghz results, one per invocation
├── ghz_steady_<...>.json         # ghz or compatible custom-driver summaries
├── ghz_sub*_<n>.json               # if subscribers were launched
├── prom/
│   ├── _index.ndjson               # {query, file} pairs
│   └── q_NN.json                   # raw Prom /api/v1/query_range output
└── pprof/
    └── <endpoint>__{pre,post}__{heap,goroutine,allocs,mutex,block[,profile]}.pb.gz
```

## Drill-down workflow

1. **Verdict & deltas.** Open `report.md`. The leak-gate table shows the
   pre/post goroutines + heap_alloc + heap_inuse + heap_objects per
   replica with the Δ vs the scenario threshold. The verdict is driven
   by `heap_alloc` (post-GC live bytes); `heap_inuse` and `heap_objects`
   are shown for context. Any cell exceeding the threshold is the first
   place to look.
2. **Allocation source.** For a leaking replica, diff its heap profiles:

   ```bash
   go tool pprof -http=:0 \
     -base testbed/bench/out/<sc>/<ts>/pprof/<ep>__pre__heap.pb.gz \
            testbed/bench/out/<sc>/<ts>/pprof/<ep>__post__heap.pb.gz
   ```

3. **Stuck goroutines.** Open `<ep>__post__goroutine.pb.gz` and look for
   blocked stacks (e.g. on channel send/recv, on a sync.Mutex). For
   `addedge_contention` the mutex and block profiles (`<ep>__post__mutex.pb.gz`,
   `<ep>__post__block.pb.gz`) are the primary signal.
4. **Tail latency.** Cross-reference the ghz p99 column in `report.md`
   with the matching Prom histogram queries listed under
   "Prometheus range queries" (raw data in `prom/q_NN.json`).
5. **Pubsub (#240).** `many_subscribers` and `replication_soak` exercise
   the subscription queue. Inspect the per-policy
   `lantern_subscription_dropped_total` and
   `lantern_subscription_queue_depth_bucket` queries in `prom/`.

## Why CI keeps only the short Search gate

The full sweep needs multi-minute steady phases, a healthy Docker daemon and
stable host conditions. Qualify it locally before tagging, with no truncation;
the root tag workflow does not run it. Root tags still run the short
`search_qualification` scenario: deterministic semantic and lifecycle
failures block, while loose throughput/p99 ratchets reject only step changes
and tolerate normal hosted-runner jitter. Four receipt admission/lookup
families stay separate from the eight canonical scenarios and are qualified
locally on the same exact image.

[ghz]: https://ghz.sh/
[yq]: https://github.com/mikefarah/yq
[#237]: https://github.com/anaregdesign/lantern/issues/237

### Standalone conditional Create diagnostics (#1626)

`./testbed/bench/run.sh edge_create` uses an owned native standalone OIDC fixture,
with a narrow `bench:source:` -> `bench:target:` Create pair and the real
production Server. It bypasses the HA Compose route because Create is disabled
in HA pending cluster-wide absence/arbitration. No production security guard is
weakened. The fixture also grants ordinary data operations for setup/cleanup;
least-privilege Creator behavior is covered separately over the real Connect wire.

One compiled Server/fixture/probe set is reused for fresh plain and receipt
families. The frozen scenario has 10s warmup, 60s steady, 100 sequential cycles/s
and 5s cooldown. Each cycle verifies created/collision/missing/expired outcomes
and deletes the accepted Edge; receipt mode additionally verifies all original
status results. Cycle RPS is distinct from aggregate RPC RPS. First Create after
endpoint/capability setup is separate from steady latency; it is not a cold
Principal/JWT measure. Receipt Create latency includes SDK capability preflight.

Reports retain binary/source/scenario hashes, first/steady latency, sampled
Server peak RSS after readiness and natural pre/post runtime metrics. No forced
GC or release floors are introduced. Teardown/source drift fails the diagnostic;
failed private state/logs remain outside public artifacts. These local results
cannot qualify final merged source, a production clock/provider, HA Create or
physical devices. Existing broad search/traversal and CDC/receipt HA families
remain separate qualification requirements.
