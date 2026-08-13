#!/usr/bin/env bash
# run.sh — entrypoint for the lantern bench harness.
#
# Usage:
#   ./testbed/bench/run.sh <scenario>
#
# Argument is the scenario stem under testbed/bench/scenarios/, without the
# .yaml extension (e.g. `write_heavy`).
#
# Environment knobs:
#   KEEP_UP=1         do not `compose down` at end (handy for debugging)
#   SKIP_UP=1         assume the cluster is already running; skip compose up
#   PPROF_CPU=1       also capture a 30s CPU profile per replica post-steady
#   PROM_URL          override Prometheus URL (default http://localhost:9091)
#   LEAK_GATE_ONLY=1  skip the pprof captures + Prometheus range queries and keep
#                     only the runtime snapshots that feed the leak-gate verdict.
#                     Those artifacts (24 pprof + 12 prom curls per scenario, up
#                     to 60s/30s each) are throughput/diagnostic extras the gate
#                     never reads, yet they dominate per-scenario wall-time. The
#                     nightly leak gate sets this so the un-truncated sweep fits a
#                     sane CI timeout; the advisory release bench leaves it unset.
#
# Exits 0 if the leak gate verdict is "pass" AND declared metric, semantic,
# and perf gates pass; exits 1
# otherwise. Exits 2 on misuse.
#
# Prerequisites: bash 4+, docker, docker compose v2, ghz, yq (v4+), jq, curl,
#                go (for the report renderer).

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
SCENARIO_DIR="$HERE/scenarios"
CAPTURE_DIR="$HERE/capture"
COMPOSE_FILES=(
  -f "$REPO_ROOT/deploy/compose/docker-compose.yml"
  -f "$HERE/compose.override.yml"
)
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-lantern-bench}"
PROM_URL="${PROM_URL:-http://localhost:9091}"
COMPOSE_STARTED=0

REPLICA_METRICS_PORTS=(9390 9391 9392)
REPLICA_GRPC_PORTS=(6380 6381 6382)

# Always include the search derived-index lifecycle in the lightweight runtime
# snapshots, including LEAK_GATE_ONLY runs. These are unlabeled gauges, so this
# list has fixed cardinality and cannot capture request/user data. Scenario
# metric_gate keys are added below after the YAML has been resolved.
RUNTIME_METRIC_NAMES=(
  lantern_search_index_docs
  lantern_search_index_terms
  lantern_search_index_physical_documents
  lantern_search_index_expired_documents
  lantern_search_index_expiration_queue_entries
  lantern_search_index_expiration_purged
  lantern_search_index_last_expiration_purge_duration_seconds
  lantern_search_index_retained_term_slots
  lantern_search_index_retained_ordinals
  lantern_search_index_postings
  lantern_search_index_position_entries
  lantern_search_index_estimated_live_bytes
  lantern_search_index_estimated_retained_bytes
  lantern_search_index_retained_ratio
  lantern_search_index_rebuild_count
  lantern_search_index_last_rebuild_duration_seconds
  lantern_search_index_healthy
)

die() { echo "run.sh: $*" >&2; exit 1; }
log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

cleanup() {
  if [[ "$COMPOSE_STARTED" == "1" && "${KEEP_UP:-0}" != "1" ]]; then
    log "compose down -v"
    docker compose "${COMPOSE_FILES[@]}" down -v >/dev/null 2>&1 || true
    COMPOSE_STARTED=0
  fi
}
trap cleanup EXIT

need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }
need docker
need ghz
need yq
need jq
need curl
need go

[[ $# -eq 1 ]] || { echo "usage: $0 <scenario>" >&2; exit 2; }
SCENARIO_NAME="$1"
SCENARIO_FILE="$SCENARIO_DIR/${SCENARIO_NAME}.yaml"
[[ -f "$SCENARIO_FILE" ]] || die "no such scenario: $SCENARIO_FILE"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUTDIR="$HERE/out/${SCENARIO_NAME}/${TS}"
mkdir -p "$OUTDIR/prom" "$OUTDIR/pprof"

render_report() {
  (
    cd "$REPO_ROOT"
    go run ./testbed/bench/report -dir "$OUTDIR" -scenario "$SCENARIO_NAME" -timestamp "$TS"
  ) > "$OUTDIR/report.md"
  log "report: $OUTDIR/report.md"
}

# Parse common scenario fields up-front.
warmup_duration="$(yq -r '.phases.warmup.duration' "$SCENARIO_FILE")"
warmup_conc="$(yq -r '.phases.warmup.concurrency' "$SCENARIO_FILE")"
warmup_rps="$(yq -r '.phases.warmup.rps' "$SCENARIO_FILE")"
steady_duration="$(yq -r '.phases.steady.duration' "$SCENARIO_FILE")"
steady_conc="$(yq -r '.phases.steady.concurrency' "$SCENARIO_FILE")"
steady_rps="$(yq -r '.phases.steady.rps' "$SCENARIO_FILE")"
cooldown="$(yq -r '.phases.cooldown' "$SCENARIO_FILE")"
endpoints=( $(yq -r '.target.endpoints[]' "$SCENARIO_FILE") )
# ghz drives load over gRPC wire, which the server's Connect-Go handlers
# accept natively on the same h2c socket (per #347 — the primary :6380
# port serves Connect + gRPC + gRPC-Web simultaneously). ghz uses gRPC
# reflection to resolve method/message descriptors; the server mounts
# `connectrpc.com/grpcreflect` unconditionally (see
# server/provider/lantern_listener.go), so no .proto file is needed and
# the harness keeps working unchanged against the Connect-only server.
# See #383 for the empirical verification log.

# ----- optional per-scenario cluster env overrides --------------------------
# A scenario may shorten the cluster's default TTL and/or GC tick (via a
# top-level `cluster:` block). The values are exported as LANTERN_BENCH_* host
# vars that compose.override.yml reads with a 3600/60 fallback, so scenarios
# without a `cluster:` block are unaffected. Note that RPC payloads with no
# Vertex.expiration / Edge.expiration are permanent by wire contract; TTL-churn
# scenarios that drive Put* through ghz must include an explicit future
# expiration in their data_template. Only meaningful on a fresh `compose up`
# (ignored under SKIP_UP, where the cluster TTL/GC were fixed at the earlier
# up). See issue #711.
cluster_ttl="$(yq -r '.cluster.default_ttl_seconds // ""' "$SCENARIO_FILE")"
cluster_gc="$(yq -r '.cluster.gc_interval_seconds // ""' "$SCENARIO_FILE")"
cluster_vertex_causal="$(yq -r '.cluster.max_vertex_causal_entries // ""' "$SCENARIO_FILE")"
cluster_edge_causal="$(yq -r '.cluster.max_edge_causal_entries // ""' "$SCENARIO_FILE")"
if [[ -n "$cluster_ttl" && "$cluster_ttl" != "null" ]]; then
  export LANTERN_BENCH_DEFAULT_TTL_SECONDS="$cluster_ttl"
  log "cluster override: LANTERN_DEFAULT_TTL_SECONDS=${cluster_ttl}"
fi
if [[ -n "$cluster_gc" && "$cluster_gc" != "null" ]]; then
  export LANTERN_BENCH_GC_INTERVAL_SECONDS="$cluster_gc"
  log "cluster override: LANTERN_GC_INTERVAL_SECONDS=${cluster_gc}"
fi
if [[ -n "$cluster_vertex_causal" && "$cluster_vertex_causal" != "null" ]]; then
  export LANTERN_BENCH_MAX_VERTEX_CAUSAL_ENTRIES="$cluster_vertex_causal"
  log "cluster override: LANTERN_MAX_VERTEX_CAUSAL_ENTRIES=${cluster_vertex_causal}"
fi
if [[ -n "$cluster_edge_causal" && "$cluster_edge_causal" != "null" ]]; then
  export LANTERN_BENCH_MAX_EDGE_CAUSAL_ENTRIES="$cluster_edge_causal"
  log "cluster override: LANTERN_MAX_EDGE_CAUSAL_ENTRIES=${cluster_edge_causal}"
fi
# NOTE: do not use `// ""` here — yq/jq treat a boolean `false` as empty, so
# `.cluster.search_enabled // ""` would swallow an explicit `false`. Read the
# raw value and test for the literal "null" (absent) instead.
cluster_search="$(yq -r '.cluster.search_enabled' "$SCENARIO_FILE")"
if [[ -n "$cluster_search" && "$cluster_search" != "null" ]]; then
  export LANTERN_BENCH_SEARCH_ENABLED="$cluster_search"
  log "cluster override: LANTERN_SEARCH_ENABLED=${cluster_search}"
fi

# ----- compose up ------------------------------------------------------------
if [[ "${SKIP_UP:-0}" != "1" ]]; then
  log "compose up (project=$COMPOSE_PROJECT_NAME)"
  # Since #435 the canonical compose declares three explicit lantern-{0,1,2}
  # services with pinned host ports, so `--scale lantern=3` is no longer
  # needed (and would in fact fail — there is no `lantern` service).
  COMPOSE_STARTED=1
  docker compose "${COMPOSE_FILES[@]}" up -d --wait
fi

# ----- discover actual published ports ---------------------------------------
# Ports are pinned in the canonical compose (6380-6382 / 9390-9392) since
# #435, but we keep the discovery step as a belt-and-suspenders check that
# the harness keeps working if a future override hops back to dynamic
# ranges. Query the live containers and rewrite REPLICA_*_PORTS + scenario
# endpoints from the live mapping.
discover_ports() {
  local ps_json grpc metrics
  ps_json="$(docker compose "${COMPOSE_FILES[@]}" ps --format json 2>/dev/null)"
  # Sort by service name so lantern-0/1/2 map to deterministic slots.
  grpc=( $(jq -rs 'sort_by(.Service) | .[] | select(.Service | test("^lantern-[0-9]+$")) | .Publishers[] | select(.TargetPort==6380 and .URL=="0.0.0.0") | .PublishedPort' <<<"$ps_json") )
  metrics=( $(jq -rs 'sort_by(.Service) | .[] | select(.Service | test("^lantern-[0-9]+$")) | .Publishers[] | select(.TargetPort==9090 and .URL=="0.0.0.0") | .PublishedPort' <<<"$ps_json") )
  [[ ${#grpc[@]} -eq 3 && ${#metrics[@]} -eq 3 ]] || die "could not discover 3 grpc+metrics ports (grpc=${grpc[*]} metrics=${metrics[*]})"
  REPLICA_GRPC_PORTS=( "${grpc[@]}" )
  REPLICA_METRICS_PORTS=( "${metrics[@]}" )
  log "discovered grpc=${REPLICA_GRPC_PORTS[*]} metrics=${REPLICA_METRICS_PORTS[*]}"
}
discover_ports

# Rewrite scenario file with discovered ports so every `localhost:6380/81/82`
# reference (target.endpoints, subscribe.endpoints, subscribe.consumers[].endpoint, ...)
# points at the actually-published host port. Operate on a copy under OUTDIR
# so the source-of-truth YAML stays clean and the resolved scenario is
# preserved alongside the report for forensics.
SCENARIO_RESOLVED="$OUTDIR/scenario.resolved.yaml"
sed \
  -e "s/localhost:6380/localhost:${REPLICA_GRPC_PORTS[0]}/g" \
  -e "s/localhost:6381/localhost:${REPLICA_GRPC_PORTS[1]}/g" \
  -e "s/localhost:6382/localhost:${REPLICA_GRPC_PORTS[2]}/g" \
  "$SCENARIO_FILE" > "$SCENARIO_RESOLVED"
SCENARIO_FILE="$SCENARIO_RESOLVED"

# Re-parse fields that depend on endpoints after substitution.
endpoints=( $(yq -r '.target.endpoints[]' "$SCENARIO_FILE") )
lifecycle_reason="$(yq -r '.lifecycle_gate.reason // ""' "$SCENARIO_FILE")"
lifecycle_metrics_endpoints=""
for port in "${REPLICA_METRICS_PORTS[@]}"; do
  if [[ -n "$lifecycle_metrics_endpoints" ]]; then lifecycle_metrics_endpoints+=","; fi
  lifecycle_metrics_endpoints+="http://localhost:${port}/metrics"
done

# A generic metric_gate may name another unlabeled gauge. Add it once while
# retaining the fixed built-in search catalogue above.
while IFS= read -r metric_name; do
  [[ -z "$metric_name" ]] && continue
  if [[ " ${RUNTIME_METRIC_NAMES[*]} " != *" ${metric_name} "* ]]; then
    RUNTIME_METRIC_NAMES+=( "$metric_name" )
  fi
done < <(yq -r '.metric_gate.metrics // {} | keys | .[]' "$SCENARIO_FILE")

wait_ready() {
  local p
  for p in "${REPLICA_METRICS_PORTS[@]}"; do
    for _ in $(seq 1 60); do
      if curl -fsS --max-time 2 "http://localhost:${p}/readyz" >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
  done
}
wait_ready

semantic_kind="$(yq -r '.semantic_gate.kind // ""' "$SCENARIO_FILE")"
semantic_endpoints=""
for port in "${REPLICA_GRPC_PORTS[@]}"; do
  if [[ -n "$semantic_endpoints" ]]; then semantic_endpoints+=","; fi
  semantic_endpoints+="http://localhost:${port}"
done

run_search_probe() {
  local mode="$1" phase="${2:-}"
  local report="$OUTDIR/semantic_${mode}.json"
  local args=( -mode "$mode" )
  if [[ "$mode" == "verify" ]]; then
    report="$OUTDIR/semantic_${phase}.json"
    args+=( -phase "$phase" )
  fi
  args+=(
    -endpoints "$semantic_endpoints"
    -report "$report"
    -timeout "${SEARCH_PROBE_TIMEOUT:-90s}"
  )
  (
    cd "$REPO_ROOT"
    go run ./testbed/bench/searchprobe "${args[@]}"
  )
}

case "$semantic_kind" in
  "") ;;
  search)
    log "semantic gate: seed deterministic search fixture"
    run_search_probe seed
    ;;
  *) die "unknown semantic_gate.kind $semantic_kind in $SCENARIO_FILE" ;;
esac

# ----- optional semantic topology preflight ---------------------------------
# A schema-valid ghz template can still benchmark an empty or accidentally
# linear graph. Scenario-owned preflight kinds seed and verify live data after
# every replica is ready but before warmup/snapshots. Kinds are deliberately
# allow-listed here rather than evaluated as arbitrary YAML shell so a scenario
# remains declarative and reviewable.
preflight_kind="$(yq -r '.preflight.kind // ""' "$SCENARIO_FILE")"
case "$preflight_kind" in
  "") ;;
  broad_illuminate)
    preflight_endpoints=""
    for port in "${REPLICA_GRPC_PORTS[@]}"; do
      if [[ -n "$preflight_endpoints" ]]; then preflight_endpoints+=","; fi
      preflight_endpoints+="http://localhost:${port}"
    done
    log "preflight: broad_illuminate topology"
    (
      cd "$REPO_ROOT"
      go run ./testbed/bench/preflight \
        -endpoints "$preflight_endpoints" \
        -report "$OUTDIR/topology_preflight.json"
    )
    ;;
  *) die "unknown preflight.kind $preflight_kind in $SCENARIO_FILE" ;;
esac

# ----- helpers ---------------------------------------------------------------

# Extract a single Prom metric value from a /metrics text exposition.
# $1 metric name, $2 raw text -> echoes the numeric value or nothing.
prom_scalar() {
  local name="$1" text="$2"
  awk -v n="$name" '$1 == n { print $2; exit }' <<<"$text"
}

snapshot_runtime() {
  # Force a GC on every replica before sampling so heap_alloc_bytes reflects
  # live (post-GC) memory rather than transient allocation between cycles.
  # /debug/pprof/heap?gc=1 calls runtime.GC() before returning the profile —
  # we discard the body and just use the side effect.
  #
  # A SINGLE post-GC sample can still read a transiently elevated heap (or
  # goroutine count): allocations racing the scrape, or the GC not fully settled
  # under CI-runner contention, briefly inflate the gauge. The leak gate's tight
  # deltas (goroutine 15, heap 16 MiB) then trip where a calm local run is
  # comfortably green (#727). So sample K rounds per replica — each its own
  # forced GC + scrape — and keep the per-metric MINIMUM: the live-set floor.
  # Transient spikes drop out, while a genuine leak raises the floor on every
  # round and is still caught. K defaults to 3; override via SNAPSHOT_ROUNDS.
  local out="$1" port round
  local rounds="${SNAPSHOT_ROUNDS:-3}"
  [[ "$rounds" -ge 1 ]] 2>/dev/null || rounds=1
  local -a samples=()
  for port in "${REPLICA_METRICS_PORTS[@]}"; do
    local g="" h_inuse="" h_alloc="" h_objs="" vhe="" vhw=""
    for (( round = 1; round <= rounds; round++ )); do
      curl -fsS --max-time 10 "http://localhost:${port}/debug/pprof/heap?gc=1" \
        -o /dev/null || true
      local text
      text="$(curl -fsS --max-time 5 "http://localhost:${port}/metrics" || true)"
      local rg rhi rha rho rvhe rvhw
      # Prom client formats large gauges in scientific notation (e.g. 1.949696e+07).
      # Coerce to integer so downstream JSON consumers (jq + Go int64) don't choke.
      rg="$(printf '%.0f' "$(prom_scalar go_goroutines "$text")" 2>/dev/null)"; rg="${rg:-0}"
      rhi="$(printf '%.0f' "$(prom_scalar go_memstats_heap_inuse_bytes "$text")" 2>/dev/null)"; rhi="${rhi:-0}"
      rha="$(printf '%.0f' "$(prom_scalar go_memstats_heap_alloc_bytes "$text")" 2>/dev/null)"; rha="${rha:-0}"
      rho="$(printf '%.0f'  "$(prom_scalar go_memstats_heap_objects     "$text")" 2>/dev/null)"; rho="${rho:-0}"
      # vertexHLC LWW watermark map (#727): the instantaneous entry count
      # (lantern_vertex_hlc_entries — drained low right after a GC sweep) and
      # its sticky per-cycle peak (lantern_vertex_hlc_entries_high_water).
      # prom_scalar matches the full metric name, so the "_entries" query does
      # NOT collide with the "_entries_high_water" line. A low entries floor
      # paired with a large high-water is the born-expired ttl_churn retention
      # fingerprint (Go never shrinks the map's bucket array after delete).
      rvhe="$(printf '%.0f' "$(prom_scalar lantern_vertex_hlc_entries "$text")" 2>/dev/null)"; rvhe="${rvhe:-0}"
      rvhw="$(printf '%.0f' "$(prom_scalar lantern_vertex_hlc_entries_high_water "$text")" 2>/dev/null)"; rvhw="${rvhw:-0}"
      # Keep the running minimum (live-set floor) across rounds for the runtime
      # gauges and the instantaneous HLC count; take the MAXIMUM for the
      # high-water, which is monotonic and whose peak is the quantity of
      # interest (a mid-snapshot sweep can only raise it).
      if [[ -z "$g"       || "$rg"  -lt "$g"       ]]; then g="$rg"; fi
      if [[ -z "$h_inuse" || "$rhi" -lt "$h_inuse" ]]; then h_inuse="$rhi"; fi
      if [[ -z "$h_alloc" || "$rha" -lt "$h_alloc" ]]; then h_alloc="$rha"; fi
      if [[ -z "$h_objs"  || "$rho" -lt "$h_objs"  ]]; then h_objs="$rho"; fi
      if [[ -z "$vhe"     || "$rvhe" -lt "$vhe"    ]]; then vhe="$rvhe"; fi
      if [[ -z "$vhw"     || "$rvhw" -gt "$vhw"    ]]; then vhw="$rvhw"; fi
    done
    local metric_samples='{}' metric_name metric_value
    for metric_name in "${RUNTIME_METRIC_NAMES[@]}"; do
      metric_value="$(prom_scalar "$metric_name" "$text")"
      if [[ -z "$metric_value" ]]; then
        metric_samples="$(jq -c --arg name "$metric_name" '.[$name] = null' <<<"$metric_samples")"
      else
        metric_samples="$(jq -c --arg name "$metric_name" --arg value "$metric_value" \
          '.[$name] = ($value | tonumber)' <<<"$metric_samples")"
      fi
    done
    samples+=( "$(jq -nc --arg ep "localhost:${port}" \
        --argjson g "$g" \
        --argjson hi "$h_inuse" --argjson ha "$h_alloc" --argjson ho "$h_objs" \
        --argjson vhe "$vhe" --argjson vhw "$vhw" \
        --argjson metrics "$metric_samples" \
      '{endpoint: $ep, goroutines: $g, heap_inuse_bytes: $hi, heap_alloc_bytes: $ha, heap_objects: $ho, vertex_hlc_entries: $vhe, vertex_hlc_high_water: $vhw, metrics: $metrics}')" )
  done
  printf '%s\n' "${samples[@]}" | jq -s '.' > "$out"
}

run_ghz() {
  # $1 phase tag, $2 endpoint, $3 call, $4 data_template literal, $5 conc, $6 rps, $7 duration
  local phase="$1" ep="$2" call="$3" data="$4" conc="$5" rps="$6" dur="$7"
  local jsonout="$OUTDIR/ghz_${phase}_${ep//[:.]/_}.json"
  ghz \
    --insecure \
    --call "$call" \
    -c "$conc" --rps "$rps" -z "$dur" \
    -d "$data" \
    --format json \
    -o "$jsonout" \
    "$ep" \
    >/dev/null || return 1
  echo "$jsonout"
}

snapshot_search_lifecycle() {
  local out="$1"
  (
    cd "$REPO_ROOT"
    go run ./testbed/bench/perfgate snapshot \
      -endpoints "$lifecycle_metrics_endpoints" \
      -out "$out"
  )
}

# ----- WARMUP ----------------------------------------------------------------
log "warmup: ${warmup_duration} @ c=${warmup_conc} rps=${warmup_rps}"
warm_call="$(yq -r '.target.call // .target.calls[0].call' "$SCENARIO_FILE")"
warm_data="$(yq -r '.target.data_template // .target.calls[0].data_template' "$SCENARIO_FILE")"
run_ghz warmup "${endpoints[0]}" "$warm_call" "$warm_data" "$warmup_conc" "$warmup_rps" "$warmup_duration" >/dev/null

# ----- PRE snapshot (after warmup) -------------------------------------------
semantic_verdict="skipped"
if [[ "$semantic_kind" == "search" ]]; then
  log "semantic gate: verify every replica before steady load"
  if ! run_search_probe verify pre; then
    semantic_verdict="fail"
    render_report
    exit 1
  fi
  semantic_verdict="pass"
fi
snapshot_runtime "$OUTDIR/runtime_pre.json"
if [[ "${LEAK_GATE_ONLY:-0}" != "1" ]]; then
  "$CAPTURE_DIR/pprof.sh" "$OUTDIR/pprof" pre || log "pprof pre snapshot reported warnings"
fi
lifecycle_capture_failed=0
if [[ -n "$lifecycle_reason" ]]; then
  log "lifecycle gate: capture typed Search counters before steady load"
  if ! snapshot_search_lifecycle "$OUTDIR/lifecycle_pre.json"; then
    lifecycle_capture_failed=1
    log "lifecycle gate: pre snapshot failed"
  fi
fi

# ----- STEADY ----------------------------------------------------------------
steady_start_epoch="$(date -u +%s)"
log "steady: ${steady_duration} @ c=${steady_conc} rps=${steady_rps}"

# Subscribe consumers (run in background for the duration of the steady phase).
sub_pids=()
sub_count="$(yq -r '.subscribe.count // 0' "$SCENARIO_FILE")"
sub_consumers_len="$(yq -r '.subscribe.consumers | length // 0' "$SCENARIO_FILE")"

if [[ "$sub_count" != "0" && "$sub_count" != "null" ]]; then
  sub_call="$(yq -r '.subscribe.call' "$SCENARIO_FILE")"
  sub_data="$(yq -r '.subscribe.data_template' "$SCENARIO_FILE")"
  sub_eps=( $(yq -r '.subscribe.endpoints[]' "$SCENARIO_FILE") )
  for i in $(seq 1 "$sub_count"); do
    ep="${sub_eps[$(( (i-1) % ${#sub_eps[@]} ))]}"
    ghz --insecure \
      --call "$sub_call" -c 1 --rps 0 -z "$steady_duration" \
      -d "$sub_data" --format json \
      -o "$OUTDIR/ghz_sub_${i}.json" "$ep" >/dev/null 2>&1 &
    sub_pids+=( "$!" )
  done
  log "launched $sub_count subscribe streams"
fi

if [[ "$sub_consumers_len" != "0" && "$sub_consumers_len" != "null" ]]; then
  for i in $(seq 0 $(( sub_consumers_len - 1 ))); do
    ep="$(yq -r ".subscribe.consumers[$i].endpoint" "$SCENARIO_FILE")"
    call="$(yq -r ".subscribe.consumers[$i].call" "$SCENARIO_FILE")"
    data="$(yq -r ".subscribe.consumers[$i].data_template" "$SCENARIO_FILE")"
    ghz --insecure \
      --call "$call" -c 1 --rps 0 -z "$steady_duration" \
      -d "$data" --format json \
      -o "$OUTDIR/ghz_sub_consumer_${i}.json" "$ep" >/dev/null 2>&1 &
    sub_pids+=( "$!" )
  done
  log "launched $sub_consumers_len explicit subscribe consumers"
fi

# Chaos schedule (background).
chaos_pid=""
chaos_target="$(yq -r '.chaos.kill_target // ""' "$SCENARIO_FILE")"
if [[ -n "$chaos_target" ]]; then
  kill_at="$(yq -r '.chaos.kill_at' "$SCENARIO_FILE")"
  restart_at="$(yq -r '.chaos.restart_at' "$SCENARIO_FILE")"
  (
    sleep_secs() { local v="$1"; sleep "${v%s}"; }
    sleep_secs "$kill_at"
    log "chaos: docker kill $chaos_target"
    docker kill "$chaos_target" >/dev/null || true
    sleep_secs "$restart_at"
    log "chaos: docker start $chaos_target"
    docker start "$chaos_target" >/dev/null || true
  ) &
  chaos_pid="$!"
fi

# Steady producer(s). If `.target.calls` is a list, fork one ghz per call
# at floor(rps / N); otherwise run a single ghz against the primary endpoint.
calls_len="$(yq -r '.target.calls | length // 0' "$SCENARIO_FILE")"
prod_pids=()
producer_failed=0
if [[ "$calls_len" != "0" && "$calls_len" != "null" ]]; then
  per_rps=$(( steady_rps / calls_len ))
  for i in $(seq 0 $(( calls_len - 1 ))); do
    call="$(yq -r ".target.calls[$i].call" "$SCENARIO_FILE")"
    data="$(yq -r ".target.calls[$i].data_template" "$SCENARIO_FILE")"
    producer_rps="$(yq -r ".target.calls[$i].rps // 0" "$SCENARIO_FILE")"
    if [[ "$producer_rps" == "0" || "$producer_rps" == "null" ]]; then
      producer_rps="$per_rps"
    fi
    [[ "$producer_rps" =~ ^[1-9][0-9]*$ ]] || die "target.calls[$i].rps must be a positive integer"
    ep="${endpoints[$(( i % ${#endpoints[@]} ))]}"
    f="$OUTDIR/ghz_steady_${i}_${ep//[:.]/_}.json"
    ghz --insecure \
      --call "$call" -c "$steady_conc" --rps "$producer_rps" -z "$steady_duration" \
      -d "$data" --format json -o "$f" "$ep" >/dev/null 2>&1 &
    prod_pids+=( "$!" )
  done
  # `wait pid...` returns only one aggregate status and, under `set -e`, can
  # either hide an earlier producer failure or abort before lifecycle/report
  # diagnostics are captured. Reap every producer independently and carry a
  # sticky failure bit through the normal post-steady evidence path.
  for i in "${!prod_pids[@]}"; do
    if wait "${prod_pids[$i]}"; then
      continue
    else
      producer_status=$?
      producer_failed=1
      log "steady producer[$i] exited non-zero (status=$producer_status)"
    fi
  done
else
  call="$(yq -r '.target.call' "$SCENARIO_FILE")"
  data="$(yq -r '.target.data_template' "$SCENARIO_FILE")"
  ep="${endpoints[0]}"
  if ! run_ghz steady "$ep" "$call" "$data" "$steady_conc" "$steady_rps" "$steady_duration" >/dev/null; then
    producer_failed=1
    log "steady primary producer exited non-zero"
  fi
fi

if [[ -n "$lifecycle_reason" ]]; then
  log "lifecycle gate: capture typed Search counters after steady load"
  if ! snapshot_search_lifecycle "$OUTDIR/lifecycle_post.json"; then
    lifecycle_capture_failed=1
    log "lifecycle gate: post snapshot failed"
  fi
fi

# Reap background helpers (subscribers run for steady_duration; they exit on
# their own). Chaos restart also self-completes.
if [[ ${#sub_pids[@]} -gt 0 ]]; then wait "${sub_pids[@]}" 2>/dev/null || true; fi
if [[ -n "$chaos_pid" ]]; then wait "$chaos_pid" 2>/dev/null || true; fi

steady_end_epoch="$(date -u +%s)"

# ----- COOLDOWN --------------------------------------------------------------
log "cooldown: ${cooldown}"
sleep "${cooldown%s}"

# ----- POST snapshot ---------------------------------------------------------
if [[ "$semantic_kind" == "search" ]]; then
  log "semantic gate: verify every replica after cooldown"
  if ! run_search_probe verify post; then
    semantic_verdict="fail"
  fi
fi
snapshot_runtime "$OUTDIR/runtime_post.json"

# pprof captures + Prometheus range queries are throughput/diagnostic extras the
# leak-gate verdict never consumes (it reads only runtime_pre/post.json), and
# they are the dominant per-scenario overhead. LEAK_GATE_ONLY skips both; the
# report renderer already degrades to "_no pprof profiles captured_" / "_no prom
# queries captured_" when the artifacts are absent.
if [[ "${LEAK_GATE_ONLY:-0}" == "1" ]]; then
  log "LEAK_GATE_ONLY=1 — skipping pprof captures + Prometheus range queries"
else
  "$CAPTURE_DIR/pprof.sh" "$OUTDIR/pprof" post || log "pprof post snapshot reported warnings"

  # ----- Prom range queries --------------------------------------------------
  log "capturing Prometheus range queries from $PROM_URL"
  prom_query_files=( "$CAPTURE_DIR/prom_queries.txt" )
  if [[ "$semantic_kind" == "search" ]]; then
    prom_query_files+=( "$CAPTURE_DIR/prom_queries_search.txt" )
  fi
  i=0
  for prom_query_file in "${prom_query_files[@]}"; do
    while IFS= read -r line; do
      q="${line%%#*}"; q="${q#"${q%%[![:space:]]*}"}"; q="${q%"${q##*[![:space:]]}"}"
      [[ -z "$q" ]] && continue
      i=$(( i + 1 ))
      out="$OUTDIR/prom/q_$(printf '%02d' "$i").json"
      curl -fsS --max-time 30 -G "$PROM_URL/api/v1/query_range" \
        --data-urlencode "query=$q" \
        --data-urlencode "start=$steady_start_epoch" \
        --data-urlencode "end=$steady_end_epoch" \
        --data-urlencode "step=5s" \
        > "$out" || log "prom query failed: $q"
      jq -nc --arg q "$q" --arg f "$(basename "$out")" '{query:$q, file:$f}' \
        >> "$OUTDIR/prom/_index.ndjson"
    done < "$prom_query_file"
  done
fi

# ----- Leak gate -------------------------------------------------------------
g_thresh="$(yq -r '.leak_gate.goroutine_max_delta' "$SCENARIO_FILE")"
# Prefer the new heap_alloc-based threshold; fall back to the legacy
# heap_inuse_max_delta_mb so scenarios that haven't been updated yet still
# evaluate. See issue #248 — heap_inuse is span-level and includes free
# slots, so it is unreliable as a leak signal under sustained churn.
h_thresh_mb="$(yq -r '.leak_gate.heap_alloc_max_delta_mb // .leak_gate.heap_inuse_max_delta_mb' "$SCENARIO_FILE")"
h_thresh_bytes=$(( h_thresh_mb * 1024 * 1024 ))

leak_json="$(jq -n \
  --slurpfile pre  "$OUTDIR/runtime_pre.json" \
  --slurpfile post "$OUTDIR/runtime_post.json" \
  --argjson g_thresh "$g_thresh" \
  --argjson h_thresh "$h_thresh_bytes" \
  --argjson h_thresh_mb "$h_thresh_mb" '
  ($pre[0])  as $pre  | ($post[0]) as $post |
  [range(0; ($pre|length)) as $i |
    {
      endpoint:           $pre[$i].endpoint,
      goroutines_pre:     $pre[$i].goroutines,
      goroutines_post:    $post[$i].goroutines,
      goroutine_delta:   ($post[$i].goroutines - $pre[$i].goroutines),
      heap_inuse_pre_bytes:  $pre[$i].heap_inuse_bytes,
      heap_inuse_post_bytes: $post[$i].heap_inuse_bytes,
      heap_inuse_delta_bytes:($post[$i].heap_inuse_bytes - $pre[$i].heap_inuse_bytes),
      heap_alloc_pre_bytes:  $pre[$i].heap_alloc_bytes,
      heap_alloc_post_bytes: $post[$i].heap_alloc_bytes,
      heap_alloc_delta_bytes:($post[$i].heap_alloc_bytes - $pre[$i].heap_alloc_bytes),
      heap_objects_pre:      $pre[$i].heap_objects,
      heap_objects_post:     $post[$i].heap_objects,
      heap_objects_delta:   ($post[$i].heap_objects - $pre[$i].heap_objects),
      vertex_hlc_entries_pre:     $pre[$i].vertex_hlc_entries,
      vertex_hlc_entries_post:    $post[$i].vertex_hlc_entries,
      vertex_hlc_high_water_pre:  $pre[$i].vertex_hlc_high_water,
      vertex_hlc_high_water_post: $post[$i].vertex_hlc_high_water
    }
  ] as $r |
  {
    thresholds: {goroutine_max_delta: $g_thresh, heap_alloc_max_delta_mb: $h_thresh_mb},
    replicas: $r,
    verdict: (if any($r[]; .goroutine_delta > $g_thresh or .heap_alloc_delta_bytes > $h_thresh)
              then "fail" else "pass" end)
  }')"
printf '%s\n' "$leak_json" > "$OUTDIR/leak_gate.json"
verdict="$(jq -r '.verdict' "$OUTDIR/leak_gate.json")"
log "leak gate verdict: $verdict"

# ----- Metric gate -----------------------------------------------------------
# Generic pre/post contracts over the unlabeled gauges captured in every
# runtime snapshot. The Go evaluator fails closed on missing/non-finite samples
# and is unit-tested with a deliberately broken cleanup fixture.
metric_count="$(yq -r '.metric_gate.metrics // {} | length' "$SCENARIO_FILE")"
metric_verdict="skipped"
if [[ "$metric_count" != "0" && "$metric_count" != "null" ]]; then
  if (
    cd "$REPO_ROOT"
    go run ./testbed/bench/metricgate \
      -scenario "$SCENARIO_FILE" \
      -pre "$OUTDIR/runtime_pre.json" \
      -post "$OUTDIR/runtime_post.json" \
      -out "$OUTDIR/metric_gate.json"
  ); then
    metric_verdict="pass"
  else
    metric_verdict="fail"
  fi
fi
log "metric gate verdict: $metric_verdict"

# ----- Perf gate --------------------------------------------------------------
# Optional per-scenario floors over the steady-phase producers (#935). Same
# enforcement model as the leak gate: a scenario without a `perf_gate:` block
# is skipped; when present, the verdict lands in perf_gate.json and folds into
# the exit code — the blocking nightly (bench-nightly.yml) thereby enforces it
# while the release-time bench stays advisory (continue-on-error, #256/#394).
#
# Aggregation matches the release summary table (testbed/bench/release): rps is
# the producer sum and p99 is the producer maximum. Ordinarily non-OK is the
# raw ghz total. A scenario may additionally declare a typed lifecycle gate;
# then only the exact server-side bounded reason-counter delta that fits inside
# the matching ghz status count is classified as expected. The existing
# max_non_ok_ratio continues to gate every remaining (unexpected) response,
# while max lifecycle frequency is a separate conjunctive ceiling.
perf_gate_count="$(yq -r '.perf_gate // {} | length' "$SCENARIO_FILE")"
lifecycle_gate_count="$(yq -r '.lifecycle_gate // {} | length' "$SCENARIO_FILE")"
perf_verdict="skipped"
if [[ "$perf_gate_count" != "0" || "$lifecycle_gate_count" != "0" ]]; then
  perf_args=(
    evaluate
    -scenario "$SCENARIO_FILE"
    -run-dir "$OUTDIR"
    -out "$OUTDIR/perf_gate.json"
  )
  if [[ -n "$lifecycle_reason" ]]; then
    perf_args+=(
      -lifecycle-pre "$OUTDIR/lifecycle_pre.json"
      -lifecycle-post "$OUTDIR/lifecycle_post.json"
    )
  fi
  if (( producer_failed != 0 )); then
    perf_args+=( -producer-failed )
  fi
  if (( lifecycle_capture_failed != 0 )); then
    perf_verdict="fail"
    (
      cd "$REPO_ROOT"
      go run ./testbed/bench/perfgate "${perf_args[@]}"
    ) || true
  elif (
    cd "$REPO_ROOT"
    go run ./testbed/bench/perfgate "${perf_args[@]}"
  ); then
    perf_verdict="pass"
  else
    perf_verdict="fail"
  fi
fi
log "perf gate verdict: $perf_verdict"

# ----- Render report ---------------------------------------------------------
render_report

# ----- Teardown --------------------------------------------------------------
cleanup

if [[ "$verdict" == "pass" && "$metric_verdict" != "fail" && "$semantic_verdict" != "fail" && "$perf_verdict" != "fail" && "$producer_failed" == "0" ]]; then exit 0; else exit 1; fi
