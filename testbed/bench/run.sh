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
#
# Exits 0 if leak gate verdict == "pass", 1 otherwise. Exits 2 on misuse.
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

REPLICA_METRICS_PORTS=(9390 9391 9392)
REPLICA_GRPC_PORTS=(6380 6381 6382)

die() { echo "run.sh: $*" >&2; exit 1; }
log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

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

# ----- compose up ------------------------------------------------------------
if [[ "${SKIP_UP:-0}" != "1" ]]; then
  log "compose up (project=$COMPOSE_PROJECT_NAME)"
  docker compose "${COMPOSE_FILES[@]}" up -d --scale lantern=3 --wait
fi

# ----- discover actual published ports ---------------------------------------
# Compose's port-range allocation (`6380-6389`, `9390-9392`) is not stable
# across runs: when a previous bench leaves entries in the daemon's port
# bookkeeping, the next `up` skips them and we end up on e.g. 6386/6387/6388.
# Query the live containers and rewrite REPLICA_*_PORTS + scenario endpoints
# accordingly so the harness works regardless of what Compose assigned.
discover_ports() {
  local ps_json grpc metrics
  ps_json="$(docker compose "${COMPOSE_FILES[@]}" ps --format json 2>/dev/null)"
  # Sort by replica name so lantern-1/2/3 map to deterministic slots.
  grpc=( $(jq -rs 'sort_by(.Name) | .[] | select(.Name | test("lantern-[0-9]+$")) | .Publishers[] | select(.TargetPort==6380 and .URL=="0.0.0.0") | .PublishedPort' <<<"$ps_json") )
  metrics=( $(jq -rs 'sort_by(.Name) | .[] | select(.Name | test("lantern-[0-9]+$")) | .Publishers[] | select(.TargetPort==9090 and .URL=="0.0.0.0") | .PublishedPort' <<<"$ps_json") )
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

# ----- helpers ---------------------------------------------------------------

# Extract a single Prom metric value from a /metrics text exposition.
# $1 metric name, $2 raw text -> echoes numeric value or "0".
prom_scalar() {
  local name="$1" text="$2"
  awk -v n="$name" '$1 == n { print $2; exit }' <<<"$text"
}

snapshot_runtime() {
  # Force a GC on every replica before sampling so heap_alloc_bytes reflects
  # live (post-GC) memory rather than transient allocation between cycles.
  # /debug/pprof/heap?gc=1 calls runtime.GC() before returning the profile —
  # we discard the body and just use the side effect.
  local out="$1" port
  for port in "${REPLICA_METRICS_PORTS[@]}"; do
    curl -fsS --max-time 10 "http://localhost:${port}/debug/pprof/heap?gc=1" \
      -o /dev/null || true
  done
  local -a samples=()
  for port in "${REPLICA_METRICS_PORTS[@]}"; do
    local text
    text="$(curl -fsS --max-time 5 "http://localhost:${port}/metrics" || true)"
    local g h_inuse h_alloc h_objs
    # Prom client formats large gauges in scientific notation (e.g. 1.949696e+07).
    # Coerce to integer so downstream JSON consumers (jq + Go int64) don't choke.
    g="$(printf '%.0f' "$(prom_scalar go_goroutines "$text")" 2>/dev/null)"; g="${g:-0}"
    h_inuse="$(printf '%.0f' "$(prom_scalar go_memstats_heap_inuse_bytes "$text")" 2>/dev/null)"; h_inuse="${h_inuse:-0}"
    h_alloc="$(printf '%.0f' "$(prom_scalar go_memstats_heap_alloc_bytes "$text")" 2>/dev/null)"; h_alloc="${h_alloc:-0}"
    h_objs="$(printf '%.0f'  "$(prom_scalar go_memstats_heap_objects     "$text")" 2>/dev/null)"; h_objs="${h_objs:-0}"
    samples+=( "$(jq -nc --arg ep "localhost:${port}" \
        --argjson g "$g" \
        --argjson hi "$h_inuse" --argjson ha "$h_alloc" --argjson ho "$h_objs" \
      '{endpoint: $ep, goroutines: $g, heap_inuse_bytes: $hi, heap_alloc_bytes: $ha, heap_objects: $ho}')" )
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
    >/dev/null
  echo "$jsonout"
}

# ----- WARMUP ----------------------------------------------------------------
log "warmup: ${warmup_duration} @ c=${warmup_conc} rps=${warmup_rps}"
warm_call="$(yq -r '.target.call // .target.calls[0].call' "$SCENARIO_FILE")"
warm_data="$(yq -r '.target.data_template // .target.calls[0].data_template' "$SCENARIO_FILE")"
run_ghz warmup "${endpoints[0]}" "$warm_call" "$warm_data" "$warmup_conc" "$warmup_rps" "$warmup_duration" >/dev/null

# ----- PRE snapshot (after warmup) -------------------------------------------
snapshot_runtime "$OUTDIR/runtime_pre.json"
"$CAPTURE_DIR/pprof.sh" "$OUTDIR/pprof" pre || log "pprof pre snapshot reported warnings"

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
prod_files=()
if [[ "$calls_len" != "0" && "$calls_len" != "null" ]]; then
  per_rps=$(( steady_rps / calls_len ))
  for i in $(seq 0 $(( calls_len - 1 ))); do
    call="$(yq -r ".target.calls[$i].call" "$SCENARIO_FILE")"
    data="$(yq -r ".target.calls[$i].data_template" "$SCENARIO_FILE")"
    ep="${endpoints[$(( i % ${#endpoints[@]} ))]}"
    f="$OUTDIR/ghz_steady_${i}_${ep//[:.]/_}.json"
    ghz --insecure \
      --call "$call" -c "$steady_conc" --rps "$per_rps" -z "$steady_duration" \
      -d "$data" --format json -o "$f" "$ep" >/dev/null 2>&1 &
    prod_pids+=( "$!" )
    prod_files+=( "$f" )
  done
  wait "${prod_pids[@]}"
else
  call="$(yq -r '.target.call' "$SCENARIO_FILE")"
  data="$(yq -r '.target.data_template' "$SCENARIO_FILE")"
  ep="${endpoints[0]}"
  f="$(run_ghz steady "$ep" "$call" "$data" "$steady_conc" "$steady_rps" "$steady_duration")"
  prod_files+=( "$f" )
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
snapshot_runtime "$OUTDIR/runtime_post.json"
"$CAPTURE_DIR/pprof.sh" "$OUTDIR/pprof" post || log "pprof post snapshot reported warnings"

# ----- Prom range queries ----------------------------------------------------
log "capturing Prometheus range queries from $PROM_URL"
i=0
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
done < "$CAPTURE_DIR/prom_queries.txt"

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
      heap_objects_delta:   ($post[$i].heap_objects - $pre[$i].heap_objects)
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

# ----- Render report ---------------------------------------------------------
(
  cd "$REPO_ROOT"
  go run ./testbed/bench/report -dir "$OUTDIR" -scenario "$SCENARIO_NAME" -timestamp "$TS"
) > "$OUTDIR/report.md"
log "report: $OUTDIR/report.md"

# ----- Teardown --------------------------------------------------------------
if [[ "${KEEP_UP:-0}" != "1" ]]; then
  log "compose down -v"
  docker compose "${COMPOSE_FILES[@]}" down -v >/dev/null
fi

if [[ "$verdict" == "pass" ]]; then exit 0; else exit 1; fi
