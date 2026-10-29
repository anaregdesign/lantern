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
#   LANTERN_BENCH_CPUSET=0-3  pin the three replicas to one shared local CPU set;
#                     the load generator must be pinned separately on Linux
#   PPROF_CPU=1       also capture a 30s CPU profile per replica post-steady
#   PROM_URL          override Prometheus URL (default http://localhost:9091)
#   LANTERN_BENCH_NATURAL_GC_OBSERVE=1  after sealing the receipt Edge Add
#                     formal result, observe 180s of idle natural GC. Requires
#                     explicit source identity and UTC/monotonic campaign
#                     deadlines; retains the original verdict and always cleans up.
#   LEAK_GATE_ONLY=1  skip the pprof captures + Prometheus range queries and keep
#                     only the runtime snapshots that feed the leak-gate verdict.
#                     Those artifacts (24 pprof + 12 prom curls per scenario, up
#                     to 60s/30s each) are throughput/diagnostic extras the gate
#                     never reads, yet they dominate per-scenario wall-time.
#                     Pre-tag qualification uses this for an untruncated sweep;
#                     omit it when profiling interactively.
#
# Exits 0 if the leak gate verdict is "pass" AND declared metric, semantic,
# and perf gates pass; exits 1
# otherwise. Exits 2 on misuse.
#
# Prerequisites: bash 4+, docker, docker compose v2, yq (v4+), jq, curl,
#                go (for the report renderer and custom drivers), plus ghz for
#                the default driver.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
SCENARIO_DIR="$HERE/scenarios"
CAPTURE_DIR="$HERE/capture"
COMPOSE_FILES=(
  -f "$HERE/compose.native.yml"
  -f "$HERE/compose.override.yml"
)
if [[ -n "${LANTERN_BENCH_CPUSET:-}" ]]; then
  COMPOSE_FILES+=(-f "$HERE/compose.cpuset.yml")
fi
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-lantern-bench}"
PROM_URL="${PROM_URL:-http://localhost:${LANTERN_BENCH_PROM_PORT:-9091}}"
COMPOSE_STARTED=0
PEER_TLS_DIR=""
NATIVE_WORK_DIR=""
MEMBERSHIP_PID=""
DIAGNOSTICS_PID=""
NATURAL_GC_PID=""
NATURAL_GC_ARGS=()
AUTHFIXTURE=""
CHANGEPROBE=""
retained_heap="${LANTERN_BENCH_RETAINED_HEAP:-0}"
[[ "$retained_heap" == "0" || "$retained_heap" == "1" ]] || { echo "invalid retained heap mode" >&2; exit 2; }
sub_pids=()
prod_pids=()
ghz_capture_pids=()
GHZ_DIAGNOSTICS_DIR=""
native_spool_provenance=()
chaos_pid=""
source "$HERE/owned_processes.sh"
source "$HERE/owned_compose.sh"
source "$HERE/compose_provisioner.sh"

REPLICA_METRICS_PORTS=("${LANTERN_BENCH_METRICS_PORT_0:-9390}" "${LANTERN_BENCH_METRICS_PORT_1:-9391}" "${LANTERN_BENCH_METRICS_PORT_2:-9392}")
REPLICA_GRPC_PORTS=("${LANTERN_BENCH_PUBLIC_PORT_0:-6380}" "${LANTERN_BENCH_PUBLIC_PORT_1:-6381}" "${LANTERN_BENCH_PUBLIC_PORT_2:-6382}")

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

# This opt-in diagnostic is outside the formal workload and memory gate.
# Its fixed deadline includes preparation and reserves 120 seconds for cleanup.
validate_natural_gc_observation() {
  [[ "${LANTERN_BENCH_NATURAL_GC_OBSERVE:-0}" != "0" ]] || return 0
  die "the historical natural_gc_single diagnostic is not a fresh paired-protocol run"
  [[ "${LANTERN_BENCH_NATURAL_GC_OBSERVE}" == "1" &&
     "$SCENARIO_NAME" == "receipt_edge_add_admission_lookup" &&
     "$target_driver" == "receipt_edge_add" &&
     "${KEEP_UP:-0}" == "0" && "${SKIP_UP:-0}" == "0" &&
     "${LEAK_GATE_ONLY:-0}" == "1" ]] ||
    die "idle GC observation requires the fresh, owned receipt Edge Add diagnostic"
  [[ -f "$CAPTURE_DIR/natural_gc_observation.py" &&
     ! -L "$CAPTURE_DIR/natural_gc_observation.py" ]] ||
    die "idle GC observation helper unavailable"
  local metrics_urls="" port
  for port in "${REPLICA_METRICS_PORTS[@]}"; do
    [[ -z "$metrics_urls" ]] || metrics_urls+=","
    metrics_urls+="http://127.0.0.1:${port}/metrics"
  done
  NATURAL_GC_ARGS=(
    --source-id "${LANTERN_BENCH_DIAGNOSTIC_SOURCE_ID:-}"
    --run-id "$COMPOSE_PROJECT_NAME"
    --deadline-utc "${LANTERN_BENCH_DIAGNOSTIC_DEADLINE_UTC:-}"
    --deadline-monotonic "${LANTERN_BENCH_DIAGNOSTIC_DEADLINE_MONOTONIC:-}"
    --endpoints "$metrics_urls"
  )
  python3 -B "$CAPTURE_DIR/natural_gc_observation.py" --check-config "${NATURAL_GC_ARGS[@]}" ||
    die "idle GC observation deadline or configuration rejected"
  # Interrupting a wait must enter the ordinary owned cleanup path too.
  trap 'exit 143' TERM
  trap 'exit 130' INT
}

finish_with_natural_gc_observation() {
  local formal_status="$1" diagnostic_status=0
  [[ "${LANTERN_BENCH_NATURAL_GC_OBSERVE:-0}" == "1" ]] || return "$formal_status"
  # Freeze the formal report separately: ordinary cleanup may append an
  # unqualified-run note to report.md, but never changes the sealed result.
  if ! python3 -B "$CAPTURE_DIR/natural_gc_observation.py" \
      --create-seal "$OUTDIR/formal_result.json" \
      --source-id "$LANTERN_BENCH_DIAGNOSTIC_SOURCE_ID" --run-id "$COMPOSE_PROJECT_NAME" \
      --formal-exit-status "$formal_status" \
      --gate-verdicts "$verdict,$metric_verdict,$semantic_verdict,$perf_verdict,$workload_verdict"; then
    log "idle GC observation: formal result could not be sealed; observation not started"
    return 1
  fi
  log "idle GC observation: formal result sealed; fixed 180s diagnostic only"
  python3 -B "$CAPTURE_DIR/natural_gc_observation.py" \
    --output "$OUTDIR/natural_gc_observation.json" --seal "$OUTDIR/formal_result.json" \
    "${NATURAL_GC_ARGS[@]}" &
  NATURAL_GC_PID=$!
  if wait "$NATURAL_GC_PID"; then diagnostic_status=0; else diagnostic_status=$?; fi
  NATURAL_GC_PID=""
  if (( diagnostic_status != 0 )); then
    log "idle GC observation: incomplete or refused; original formal result retained"
    return 1
  fi
  log "idle GC observation: complete; original formal result retained"
  return "$formal_status"
}

cleanup() {
  local status=$?
  local tls_in_use=0
  local private_failure=""
  local compose_logs_status=0
  trap - EXIT
  # Cancel owned workload/chaos producers before deleting their Server state.
  # Collected children are unregistered immediately so PID reuse is harmless.
  if ! stop_owned_processes "${NATURAL_GC_PID:-}" "${chaos_pid:-}" "${prod_pids[@]}" "${sub_pids[@]}" "${MEMBERSHIP_PID:-}" "${DIAGNOSTICS_PID:-}"; then
    echo "run.sh: owned child teardown failed; run is unqualified" >&2
    if (( status == 0 )); then status=1; fi
  fi
  if [[ -n "${GHZ_DIAGNOSTICS_DIR:-}" ]] && (( ${#ghz_capture_pids[@]} != 0 )); then
    join_ghz_diagnostics
    if (( producer_diagnostics_failed != 0 && status == 0 )); then status=1; fi
  fi
  if [[ "${COMPOSE_STARTED:-0}" == "1" ]]; then
    # Raw diagnostics stay outside public artifact globs and trust directories.
    # They may contain sensitive runtime context; publish hashes/verdicts only.
    # Capture once after traffic/owned children stop and before teardown, even
    # on success. Best-effort observation never changes the workload status.
    if private_failure="$(mktemp -d "${TMPDIR:-/tmp}/lantern-native-failure.XXXXXXXX")" &&
        chmod 700 "$private_failure"; then
      if (umask 077; docker compose "${COMPOSE_FILES[@]}" logs --no-color > "$private_failure/server.log" 2>&1); then
        compose_logs_status=0
      else
        compose_logs_status=$?
        echo "run.sh: failed to capture Compose logs (exit=$compose_logs_status); workload status preserved" >&2
      fi
      if ! chmod 600 "$private_failure/server.log"; then
        echo "run.sh: failed to secure private Compose log capture; workload status preserved" >&2
      fi
      # This records the capture exit only; zero does not prove complete logs.
      if ! (umask 077; printf '%s\n' "$compose_logs_status" > "$private_failure/server.log.exit-code"); then
        echo "run.sh: failed to retain Compose log capture exit code; workload status preserved" >&2
      fi
      echo "run.sh: private startup/runtime evidence retained at $private_failure" >&2
    else
      private_failure=""
      echo "run.sh: failed to create private startup/runtime evidence; workload status preserved" >&2
    fi
  fi
  if [[ "$COMPOSE_STARTED" == "1" && "${KEEP_UP:-0}" != "1" ]]; then
    log "compose down -v (project=$COMPOSE_PROJECT_NAME)"
    if ! down_owned_compose "$COMPOSE_PROJECT_NAME" "${COMPOSE_FILES[@]}"; then
      echo "run.sh: compose teardown failed (project=$COMPOSE_PROJECT_NAME); run is unqualified" >&2
      if [[ -n "${OUTDIR:-}" && -f "$OUTDIR/report.md" ]] &&
        ! printf '\n**Bench run:** unqualified (named Compose teardown failed).\n' >> "$OUTDIR/report.md"; then
        echo "run.sh: could not mark report as unqualified" >&2
      fi
      if (( status == 0 )); then status=1; fi
      tls_in_use=1
    fi
  fi
  # Keep only the owned bridge log before removing native trust inputs. This
  # also covers a successful workload whose Compose teardown subsequently fails.
  if (( status != 0 )) && [[ -n "${NATIVE_WORK_DIR:-}" &&
      "$NATIVE_WORK_DIR" == "${HERE:-}/out/.peer-tls."* &&
      ! -L "$NATIVE_WORK_DIR" && -f "$NATIVE_WORK_DIR/diagnostics.log" &&
      ! -L "$NATIVE_WORK_DIR/diagnostics.log" ]]; then
    if [[ -z "$private_failure" && "${COMPOSE_STARTED:-0}" != "1" ]]; then
      if private_failure="$(mktemp -d "${TMPDIR:-/tmp}/lantern-native-failure.XXXXXXXX")" &&
          chmod 700 "$private_failure"; then
        echo "run.sh: private bridge evidence retained at $private_failure" >&2
      else
        private_failure=""
        echo "run.sh: failed to create private bridge evidence; run remains unqualified" >&2
      fi
    fi
    if [[ -n "$private_failure" ]] &&
        ! (umask 077; cat -- "$NATIVE_WORK_DIR/diagnostics.log" > "$private_failure/diagnostics.log"); then
      echo "run.sh: failed to retain private bridge evidence; run remains unqualified" >&2
    fi
  fi
  if (( status != 0 )) && [[ -n "${OUTDIR:-}" && -f "$OUTDIR/report.md" ]]; then
    printf '\n**Bench run:** unqualified (workload or teardown failed).\n' >> "$OUTDIR/report.md" || true
  fi
  if [[ -n "${PEER_TLS_DIR:-}" && -d "$PEER_TLS_DIR" ]]; then
    if [[ "$PEER_TLS_DIR" != "$HERE"/out/.peer-tls.* ]]; then
      echo "run.sh: refusing to clean unexpected TLS material path" >&2
      if (( status == 0 )); then status=1; fi
    elif [[ "${KEEP_UP:-0}" == "1" && "$COMPOSE_STARTED" == "1" ]] || (( tls_in_use != 0 )); then
      log "receipt TLS material retained at $PEER_TLS_DIR while the named Compose project may be running"
    elif ! rm -r -- "${NATIVE_WORK_DIR:-$PEER_TLS_DIR}"; then
      echo "run.sh: failed to remove ephemeral receipt TLS material" >&2
      if (( status == 0 )); then status=1; fi
    fi
  fi
  if [[ -n "${GHZ_DIAGNOSTICS_DIR:-}" ]]; then
    if (( status != 0 )); then
      echo "run.sh: private ghz producer diagnostics retained at $GHZ_DIAGNOSTICS_DIR" >&2
    elif ! rm -r -- "$GHZ_DIAGNOSTICS_DIR"; then
      echo "run.sh: failed to remove private ghz diagnostics; run is unqualified" >&2
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }
# The conditional family has its own owned native standalone topology. It
# must not enter the three-replica Compose route while HA Create is disabled.
if [[ $# -eq 1 && "$1" == "edge_create" ]]; then
  exec python3 "$HERE/createprobe/run.py" "$SCENARIO_DIR/edge_create.yaml"
fi
need docker
need yq
need jq
need curl
need go
need python3

[[ $# -eq 1 ]] || { echo "usage: $0 <scenario>" >&2; exit 2; }
SCENARIO_NAME="$1"
SCENARIO_FILE="$SCENARIO_DIR/${SCENARIO_NAME}.yaml"
[[ -f "$SCENARIO_FILE" ]] || die "no such scenario: $SCENARIO_FILE"

# Private authority inputs are replaced atomically by the host publisher.
# Use the same explicitly non-root UID/GID for this local Compose cohort.
runtime_owner="$(id -u):$(id -g)"
[[ "$runtime_owner" =~ ^[1-9][0-9]*:[0-9]+$ ]] || die "native benchmark driver must be non-root"
export LANTERN_BENCH_RUNTIME_UID="${runtime_owner%:*}"
export LANTERN_BENCH_RUNTIME_GID="${runtime_owner#*:}"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUTDIR="$HERE/out/${SCENARIO_NAME}/${TS}"
if [[ "$retained_heap" == "1" ]]; then OUTDIR="$HERE/out/retained/${SCENARIO_NAME}/${TS}"; fi
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
target_driver="$(yq -r '.target.driver // "ghz"' "$SCENARIO_FILE")"
case "$target_driver" in
  ghz) receipt_driver=0 ;;
  receipt_vertex_put | receipt_vertex_delete | receipt_edge_delete | receipt_edge_add)
    receipt_driver=1 ;;
  *) die "unknown target.driver $target_driver in $SCENARIO_FILE" ;;
esac
if [[ "$retained_heap" == "1" ]]; then
  [[ "$receipt_driver" == "1" && "${KEEP_UP:-0}" == "0" && "${SKIP_UP:-0}" == "0" && "${LEAK_GATE_ONLY:-0}" == "1" ]] || die "retained measurement requires a fresh owned receipt cohort"
  export LANTERN_BENCH_HEAP_CONTROL_ROOT="$OUTDIR/heap-control-evidence"
  python3 -B "$CAPTURE_DIR/heap_control.py" init --directory "$LANTERN_BENCH_HEAP_CONTROL_ROOT" || die "cannot create private retained control inputs"
  COMPOSE_FILES+=(-f "$HERE/compose.retained-heap.yml")
fi
validate_natural_gc_observation
if [[ "$target_driver" == "ghz" ]]; then
  need ghz
fi
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

receipt_wal_enabled="$(yq -r '.cluster.receipt_wal.enabled // false' "$SCENARIO_FILE")"
if [[ "$receipt_driver" == "1" && "$receipt_wal_enabled" != "true" ]]; then
  die "$target_driver requires cluster.receipt_wal.enabled=true"
fi
if [[ "$receipt_wal_enabled" == "true" ]]; then
  [[ "$receipt_driver" == "1" ]] ||
    die "cluster.receipt_wal is reserved for a receipt target.driver"
  [[ "${SKIP_UP:-0}" != "1" ]] ||
    die "receipt driver requires a fresh Compose lifecycle; unset SKIP_UP"

  receipt_retention="$(yq -r '.cluster.receipt_wal.retention // ""' "$SCENARIO_FILE")"
  receipt_max_entries="$(yq -r '.cluster.receipt_wal.max_entries // 0' "$SCENARIO_FILE")"
  receipt_max_bytes="$(yq -r '.cluster.receipt_wal.max_bytes // 0' "$SCENARIO_FILE")"
  [[ -n "$receipt_retention" ]] || die "cluster.receipt_wal.retention is required"
  [[ "$receipt_max_entries" =~ ^[1-9][0-9]*$ ]] ||
    die "cluster.receipt_wal.max_entries must be a positive integer"
  [[ "$receipt_max_bytes" =~ ^[1-9][0-9]*$ ]] ||
    die "cluster.receipt_wal.max_bytes must be a positive integer"

  [[ "${LEAK_GATE_ONLY:-0}" == "1" && "${PPROF_CPU:-0}" != "1" ]] ||
    die "protected receipt benchmarks require LEAK_GATE_ONLY=1 and prohibit pprof"
  COMPOSE_FILES+=( -f "$HERE/compose.receipt-tls.yml" )
  export LANTERN_BENCH_BACKUP_RESTORE_ON_START="false"
  export LANTERN_BENCH_RECEIPT_MAX_BYTES="$receipt_max_bytes"
  export LANTERN_BENCH_RECEIPT_MAX_ENTRIES="$receipt_max_entries"
  export LANTERN_BENCH_RECEIPT_RETENTION="$receipt_retention"
  log "cluster override: fresh durable receipt WAL with bounded capacity"
else
  COMPOSE_FILES+=( -f "$HERE/compose.graph-snapshot.yml" )
fi

# Every fresh bench cohort uses native private workload admission, including OFF.
[[ "${SKIP_UP:-0}" != "1" ]] ||
  die "native benchmark trust/state requires a fresh lifecycle; unset SKIP_UP"
NATIVE_WORK_DIR="$(mktemp -d "$HERE/out/.peer-tls.XXXXXXXX")" || die "cannot create private native fixture workspace"
PEER_TLS_DIR="$NATIVE_WORK_DIR/trust"
AUTHFIXTURE="$NATIVE_WORK_DIR/authfixture"
export LANTERN_BENCH_PEER_TLS_DIR="$PEER_TLS_DIR"
go -C "$REPO_ROOT/server" build -o "$AUTHFIXTURE" ./cmd/authfixture || die "cannot build native fixture helper"
native_public_ports="${REPLICA_GRPC_PORTS[0]},${REPLICA_GRPC_PORTS[1]},${REPLICA_GRPC_PORTS[2]}"
fixture_args=(-compose -directory "$PEER_TLS_DIR" -public-ports "$native_public_ports" -mode off)
authority_image_id=""
if [[ "$receipt_driver" == "1" ]]; then
  [[ -n "${LANTERN_IMAGE:-}" ]] || die "receipt driver requires an explicit LANTERN_IMAGE"
  authority_image_id="$(docker image inspect --format '{{.Id}}' "$LANTERN_IMAGE")" || die "cannot pin original authority image"
  authority_exporter="$(prepare_compose_authority_exporter "$REPO_ROOT" "$NATIVE_WORK_DIR" "$authority_image_id")" || die "cannot prepare Linux original authority exporter"
  fixture_args=(-compose -directory "$PEER_TLS_DIR" -public-ports "$native_public_ports" -mode oidc -receipt -authority-provisioner "$authority_exporter")
fi
CHANGEPROBE="$NATIVE_WORK_DIR/changeprobe"
go -C "$REPO_ROOT" build -o "$CHANGEPROBE" ./testbed/bench/changeprobe || die "cannot build public CDC consumer"
"$AUTHFIXTURE" "${fixture_args[@]}" > "$NATIVE_WORK_DIR/generated.json" || die "cannot generate native benchmark workload trust"
write_compose_time_source "$PEER_TLS_DIR" || die "cannot prepare declared read-only time source"
if [[ "$receipt_driver" == "1" ]]; then
  export LANTERN_BENCH_AUTH_TOKEN="$(jq -er '.[0]' "$PEER_TLS_DIR/tokens.json")"
  [[ "$LANTERN_BENCH_AUTH_TOKEN" =~ ^lnt_m1_[A-Za-z0-9_-]{43}$ ]] || die "invalid native machine Role credential"
fi
"$AUTHFIXTURE" -compose -renew -renew-every 2m -directory "$PEER_TLS_DIR" > "$NATIVE_WORK_DIR/renewal.log" 2>&1 &
MEMBERSHIP_PID=$!

# A receipt run pins one content-addressed image before Compose starts, then
# proves all three running project services used exactly that image and source
# commit before warmup and again after the verdict. Manual qualification passes the
# same expected ID to all four separate fresh-WAL invocations.
receipt_image_ref="${LANTERN_IMAGE:-}"
receipt_image_id=""
receipt_image_commit=""
receipt_container_ids=()
pin_receipt_image() {
  [[ -n "$receipt_image_ref" ]] || die "receipt driver requires an explicit LANTERN_IMAGE"
  receipt_image_id="$(docker image inspect --format '{{.Id}}' "$receipt_image_ref")" ||
    die "cannot resolve receipt image $receipt_image_ref before Compose startup"
  [[ "$receipt_image_id" =~ ^sha256:[0-9a-f]{64}$ ]] ||
    die "receipt image $receipt_image_ref has no valid immutable image ID"
  if [[ -n "${EXPECTED_LANTERN_IMAGE_ID:-}" && "$receipt_image_id" != "$EXPECTED_LANTERN_IMAGE_ID" ]]; then
    die "receipt image $receipt_image_ref differs from pinned $EXPECTED_LANTERN_IMAGE_ID"
  fi
  receipt_image_commit="$(docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$receipt_image_ref" |
    sed -n 's/^LANTERN_COMMIT=//p')" ||
    die "cannot read receipt image commit for $receipt_image_ref"
  [[ "$receipt_image_commit" =~ ^[0-9a-f]{40}$ ]] ||
    die "receipt image $receipt_image_ref lacks a full source commit"
  if [[ -n "${EXPECTED_LANTERN_COMMIT:-}" && "$receipt_image_commit" != "$EXPECTED_LANTERN_COMMIT" ]]; then
    die "receipt image commit $receipt_image_commit differs from pinned $EXPECTED_LANTERN_COMMIT"
  fi
}

verify_receipt_image_provenance() {
  local out="$1" actual_image_id service container identity
  local actual_container_id actual_container_image actual_ref running project actual_service restarts actual_runtime_user actual_cpuset replica spool index=0 approved_recreation=""
  local -a replicas=()
  actual_image_id="$(docker image inspect --format '{{.Id}}' "$receipt_image_ref")" ||
    die "receipt image $receipt_image_ref disappeared"
  [[ "$actual_image_id" == "$receipt_image_id" ]] ||
    die "receipt image $receipt_image_ref changed since startup"
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$service")" ||
      die "cannot resolve receipt service $service"
    [[ "$container" =~ ^[0-9a-f]{12,64}$ ]] ||
      die "receipt service $service has no unique running container"
    identity="$(docker inspect --format \
      '{{.Id}}|{{.Image}}|{{.Config.Image}}|{{.State.Running}}|{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}|{{.RestartCount}}|{{.Config.User}}|{{.HostConfig.CpusetCpus}}' \
      "$container")" || die "cannot inspect receipt service $service"
    IFS='|' read -r actual_container_id actual_container_image actual_ref running project actual_service restarts actual_runtime_user actual_cpuset <<<"$identity"
    [[ "$actual_container_id" =~ ^[0-9a-f]{64}$ && "$running" == "true" &&
       "$project" == "$COMPOSE_PROJECT_NAME" && "$actual_service" == "$service" &&
       "$actual_container_image" == "$receipt_image_id" && "$actual_ref" == "$receipt_image_ref" &&
       "$restarts" == "0" ]] ||
      die "receipt service $service is not a running, restart-free $receipt_image_id from $receipt_image_ref in $COMPOSE_PROJECT_NAME"
    [[ "$actual_runtime_user" == "$runtime_owner" && "$actual_cpuset" == "${LANTERN_BENCH_CPUSET:-}" ]] ||
      die "receipt service $service has mismatched runtime user or CPU placement"
    if [[ -n "${receipt_container_ids[$index]:-}" &&
          "${receipt_container_ids[$index]}" != "$actual_container_id" ]]; then
      approved_recreation=""
      if [[ "${receipt_driver:-1}" == "0" && -f "$OUTDIR/native_chaos.json" ]]; then
        approved_recreation="$(jq -er --arg service "$service" --arg old "${receipt_container_ids[$index]}" '
          select(.service==$service and .previous_container==$old) | .restarted_container
        ' "$OUTDIR/native_chaos.json")" || die "undeclared native workload recreation"
      fi
      [[ "$actual_container_id" == "$approved_recreation" ]] || die "receipt service $service was recreated during the run"
    fi
    receipt_container_ids[$index]="$actual_container_id"
    spool="${native_spool_provenance[$index]:-null}"
    [[ "$receipt_driver" != "0" || "$spool" != null ]] || die "missing graph Snapshot spool provenance for $service"
    replica="$(jq -nc --arg service "$service" --arg container_id "$actual_container_id" \
      --arg image_id "$actual_container_image" --arg image_ref "$actual_ref" \
      --arg runtime_user "$actual_runtime_user" --arg cpuset "$actual_cpuset" \
      --argjson spool "$spool" \
      '{service:$service, container_id:$container_id, image_id:$image_id, image_ref:$image_ref, runtime_user:$runtime_user, cpuset:$cpuset} +
        (if $spool==null then {} else {graph_snapshot_spool:$spool} end)')" ||
      die "cannot record receipt image provenance for $service"
    replicas+=( "$replica" )
    index=$((index + 1))
  done
  printf '%s\n' "${replicas[@]}" |
    jq -s --arg ref "$receipt_image_ref" --arg id "$receipt_image_id" \
      --arg commit "$receipt_image_commit" --arg runtime_user "$runtime_owner" \
      '{image_ref:$ref, image_id:$id, source_commit:$commit, runtime_user:$runtime_user, replicas:.}' > "$out" ||
    die "cannot write receipt image provenance to $out"
  log "verified receipt image $receipt_image_id from $receipt_image_commit on all three replicas"
}

pin_receipt_image
[[ "$receipt_driver" != "1" || "$authority_image_id" == "$receipt_image_id" ]] || die "workload image changed after original authority provisioning"
heap_image_protocol="$(docker image inspect --format '{{if .Config.Labels}}{{index .Config.Labels "io.lantern.bench-heap.protocol"}}{{end}}' "$receipt_image_id")" || die "cannot inspect measurement image scope"
if [[ "$retained_heap" == "1" ]]; then
  [[ "$heap_image_protocol" == "symmetric_post_gc_2_v1" ]] || die "retained lane requires the distinct measurement image"
else
  [[ "$heap_image_protocol" == "" || "$heap_image_protocol" == "<no value>" ]] || die "normal lane cannot use a measurement image"
fi
# The release image retains its non-root default. This local fixture uses
# the publisher's non-root identity, preserving owner-only replacement files.
image_owner="$(docker run --rm --entrypoint /bin/sh "$receipt_image_id" -c 'printf "%s:%s" "$(id -u)" "$(id -g)"')" || die "cannot determine native image file owner"
[[ "$image_owner" =~ ^[1-9][0-9]*:[0-9]+$ ]] || die "native workload image must use a non-root user"

verify_receipt_peer_tls() {
  local out="$1" baseline="${2:-}" service container mounts env
  kill -0 "$MEMBERSHIP_PID" 2>/dev/null || die "owned membership renewal exited"
  kill -0 "$DIAGNOSTICS_PID" 2>/dev/null || die "owned diagnostics collector exited"
  local ports="${REPLICA_GRPC_PORTS[0]},${REPLICA_GRPC_PORTS[1]},${REPLICA_GRPC_PORTS[2]}"
  local -a args=(verify -dir "$PEER_TLS_DIR" -ports "$ports" -out "$out")
  [[ -z "$baseline" ]] || args+=(-baseline "$baseline")
  verify_native_workload_settings
  (cd "$REPO_ROOT" && go run ./testbed/bench/receipttls "${args[@]}") ||
    die "receipt peer TLS identity/provenance verification failed"
}

# Native provenance applies to OFF cohorts as well; public mode never disables
# private peer authentication. Compare generated material without publishing it.
verify_native_workload_settings() {
  local service container mounts env actual_user configuration layout expected_count spool volume label metadata index=0 seen="|"
  kill -0 "$MEMBERSHIP_PID" 2>/dev/null || die "owned membership renewal exited"
  configuration="$(docker compose "${COMPOSE_FILES[@]}" config --format json)" || die "cannot inspect native volume configuration"
  layout="$(owned_compose_volume_layout "$COMPOSE_PROJECT_NAME" "$runtime_owner" <<<"$configuration")" ||
    die "native workload has an incomplete or unknown owned volume set"
  if [[ "$receipt_driver" == "0" ]]; then expected_count=9; else expected_count=6; fi
  [[ "$(jq -er '.count' <<<"$layout")" == "$expected_count" ]] || die "native workload has the wrong graph/receipt volume set"
  native_spool_provenance=()
  for service in lantern-0 lantern-1 lantern-2; do
    container="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$service")" || die "missing native workload"
    actual_user="$(docker inspect --format '{{.Config.User}}' "$container")" || die "missing native runtime user"
    [[ "$actual_user" == "$runtime_owner" ]] || die "$service has a mismatched native runtime user"
    mounts="$(docker inspect --format '{{json .Mounts}}' "$container")" || die "missing native mounts"
    jq -e --arg source "$PEER_TLS_DIR/$service" '
      [.[] | select(.Type == "bind" and .Destination == "/run/lantern-config" and
        (.RW == false) and (.Source | endswith($source)))] | length == 1
    ' <<<"$mounts" >/dev/null || die "$service has no pinned, read-only native mount"
    jq -e --arg source "$PEER_TLS_DIR/$service/ntp.conf" '
        [.[] | select(.Type == "bind" and .Destination == "/etc/ntp.conf" and
          (.RW == false) and (.Source | endswith($source)))] | length == 1
      ' <<<"$mounts" >/dev/null || die "$service has no pinned, read-only declared time source"
    if [[ "$receipt_driver" == "0" ]]; then
      spool=/var/lib/lantern-snapshot-spool
      volume="$(jq -er --arg service "$service" --arg target "$spool" '
        .services[] | select(.service==$service) | .mounts[] | select(.target==$target) | .volume
      ' <<<"$layout")" || die "$service has no declared graph Snapshot spool volume"
      [[ "$volume" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]+$ && "$seen" != *"|$volume|"* ]] || die "$service has a shared graph Snapshot spool"
      jq -e --arg target "$spool" --arg volume "$volume" '
        [.[] | select(.Destination==$target)] |
        length==1 and all(.[]; .Type=="volume" and .Name==$volume and .RW==true)
      ' <<<"$mounts" >/dev/null || die "$service has a mismatched graph Snapshot spool mount"
      label="$(docker volume inspect --format '{{index .Labels "com.docker.compose.project"}}' "$volume")" || die "cannot inspect graph Snapshot spool ownership"
      [[ "$label" == "$COMPOSE_PROJECT_NAME" ]] || die "$service has a foreign graph Snapshot spool"
      metadata="$(docker exec "$container" /bin/sh -c 'test ! -L "$1" && test -d "$1" && stat -c "%u:%g:%a" "$1"' fixture "$spool")" ||
        die "cannot inspect graph Snapshot spool UID/GID/mode for $service"
      [[ "$metadata" == "$runtime_owner:700" ]] || die "$service has a graph Snapshot spool with the wrong UID/GID/mode"
      seen+="$volume|"
      native_spool_provenance[$index]="$(jq -nc --arg volume "$volume" --arg target "$spool" --arg owner "$runtime_owner" \
        '{volume:$volume,target:$target,runtime_user:$owner,mode:"0700",read_write:true,configured_nocopy:true}')" || die "cannot record graph Snapshot spool provenance"
    else
      jq -e '[.[] | select(.Destination=="/var/lib/lantern-snapshot-spool")] | length==0' <<<"$mounts" >/dev/null ||
        die "$service unexpectedly mounts a graph Snapshot spool in receipt mode"
    fi
    env="$(docker inspect --format '{{json .Config.Env}}' "$container")" || die "missing native environment"
    jq -e --arg service "$service" --argjson actual "$env" --arg driver "$receipt_driver" '
      (.nodes[] | select(.name == ("node-" + ($service | split("-")[1]))) | .environment |
        if $driver == "0" then .LANTERN_METRICS_ADDR = ":9090" else . end) as $expected |
      all($expected | to_entries[] |
        select(.key != "LANTERN_RECEIPT_MAX_BYTES" and .key != "LANTERN_RECEIPT_MAX_ENTRIES" and
          .key != "LANTERN_RECEIPT_RETENTION" and .key != "LANTERN_LOG_LEVEL");
        . as $entry | [$actual[] | select(. == ($entry.key + "=" + $entry.value))] | length == 1) and
      all($actual[]; startswith("LANTERN_AUTH_TOKENS=") or startswith("LANTERN_PEER_DISCOVERY=") or
        startswith("LANTERN_PEERS=") | not)
    ' "$PEER_TLS_DIR/fixture.json" >/dev/null || die "$service has missing or mismatched native settings"
    index=$((index + 1))
  done
}

# ----- compose up ------------------------------------------------------------
if [[ "${SKIP_UP:-0}" != "1" ]]; then
  owned_compose_project_absent "$COMPOSE_PROJECT_NAME" || die "fresh native Compose project is already present or its absence cannot be verified"
  log "compose up (project=$COMPOSE_PROJECT_NAME)"
  # Since #435 the canonical compose declares three explicit lantern-{0,1,2}
  # services with pinned host ports, so `--scale lantern=3` is no longer
  # needed (and would in fact fail — there is no `lantern` service).
  owned_configuration="$(docker compose "${COMPOSE_FILES[@]}" config --format json)" || die "cannot inspect fresh native volume configuration"
  owned_layout="$(owned_compose_volume_layout "$COMPOSE_PROJECT_NAME" "$runtime_owner" <<<"$owned_configuration")" ||
    die "fresh native volume layout is incomplete or unknown"
  if [[ "$receipt_driver" == "0" ]]; then owned_count=9; else owned_count=6; fi
  [[ "$(jq -er '.count' <<<"$owned_layout")" == "$owned_count" ]] || die "fresh native graph/receipt volume layout does not match the selected mode"
  COMPOSE_STARTED=1
  receipt_keep_up="${KEEP_UP:-0}"
  KEEP_UP=1
  if ! initialize_owned_compose_volumes "$COMPOSE_PROJECT_NAME" "$receipt_image_id" "$runtime_owner" "${COMPOSE_FILES[@]}"; then
    die "fresh volume initialization refused; named project and private inputs retained for review"
  fi
  if [[ "$receipt_driver" == "1" ]]; then
    if ! initialize_owned_compose_security "$COMPOSE_PROJECT_NAME" "$receipt_image_id" "$runtime_owner" "${COMPOSE_FILES[@]}"; then
      die "explicit security initialization failed; no serving started; named project and private inputs retained for review"
    fi
    KEEP_UP="$receipt_keep_up"
    # Start only the protected replicas; metrics stay on their loopback plane.
    if [[ "$retained_heap" == "1" ]]; then
      [[ "${EXPECTED_LANTERN_TREE:-}" =~ ^[0-9a-f]{40}$ ]] || die "tagged measurement requires the exact expected source tree"
      docker compose "${COMPOSE_FILES[@]}" create lantern-0 lantern-1 lantern-2 || die "cannot create exact measurement targets"
      heap_containers=""
      heap_endpoints=""
      for index in 0 1 2; do
        heap_container="$(docker compose "${COMPOSE_FILES[@]}" ps --all -q "lantern-$index")" || die "cannot bind created measurement target"
        [[ "$heap_container" =~ ^[0-9a-f]{64}$ ]] || die "measurement target is not one exact full container ID"
        [[ -z "$heap_containers" ]] || { heap_containers+=","; heap_endpoints+=","; }
        heap_containers+="$heap_container"
        heap_endpoints+="localhost:${REPLICA_METRICS_PORTS[$index]}"
      done
      sed -e "s/localhost:6380/localhost:${REPLICA_GRPC_PORTS[0]}/g" \
        -e "s/localhost:6381/localhost:${REPLICA_GRPC_PORTS[1]}/g" \
        -e "s/localhost:6382/localhost:${REPLICA_GRPC_PORTS[2]}/g" "$SCENARIO_FILE" |
        yq -o=json '.' > "$OUTDIR/scenario.heap-input.json"
      python3 -B "$CAPTURE_DIR/heap_control.py" prepare \
        --directory "$LANTERN_BENCH_HEAP_CONTROL_ROOT" --containers "$heap_containers" --endpoints "$heap_endpoints" \
        --source-commit "$receipt_image_commit" --source-tree "$EXPECTED_LANTERN_TREE" \
        --image-id "$receipt_image_id" --runtime-user "$runtime_owner" --run-id "$COMPOSE_PROJECT_NAME" \
        --cpuset "${LANTERN_BENCH_CPUSET:-}" --scenario-json "$OUTDIR/scenario.heap-input.json" || die "measurement target binding failed before start"
      docker compose "${COMPOSE_FILES[@]}" up -d --no-recreate --wait lantern-0 lantern-1 lantern-2
    else
      docker compose "${COMPOSE_FILES[@]}" up -d --wait lantern-0 lantern-1 lantern-2
    fi
  else
    KEEP_UP="$receipt_keep_up"
    docker compose "${COMPOSE_FILES[@]}" --profile off-diagnostics up -d --wait
  fi
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
  grpc=( $(jq -rs 'sort_by(.Service) | .[] | select(.Service | test("^lantern-[0-9]+$")) | .Publishers[] | select(.TargetPort==6380 and .URL=="127.0.0.1") | .PublishedPort' <<<"$ps_json") )
  metrics=( $(jq -rs 'sort_by(.Service) | .[] | select(.Service | test("^lantern-[0-9]+$")) | .Publishers[] | select(.TargetPort==9090 and .URL=="127.0.0.1") | .PublishedPort' <<<"$ps_json") )
  if [[ "$receipt_driver" == "1" ]]; then
    [[ ${#metrics[@]} -eq 0 ]] || die "protected metrics must not be container-published"
    metrics=("${REPLICA_METRICS_PORTS[@]}")
  fi
  [[ ${#grpc[@]} -eq 3 && ${#metrics[@]} -eq 3 ]] || die "could not discover 3 grpc+metrics ports (grpc=${grpc[*]} metrics=${metrics[*]})"
  REPLICA_GRPC_PORTS=( "${grpc[@]}" )
  REPLICA_METRICS_PORTS=( "${metrics[@]}" )
  log "discovered grpc=${REPLICA_GRPC_PORTS[*]} metrics=${REPLICA_METRICS_PORTS[*]}"
}
discover_ports
verify_native_workload_settings
verify_receipt_image_provenance "$OUTDIR/image_provenance_pre.json"
if [[ "$receipt_driver" == "1" ]]; then
  diagnostic_containers=()
  for service in lantern-0 lantern-1 lantern-2; do
    diagnostic_containers+=("$(docker compose "${COMPOSE_FILES[@]}" ps -q "$service")")
  done
  python3 "$CAPTURE_DIR/diagnostics_bridge.py" \
    --containers "${diagnostic_containers[0]},${diagnostic_containers[1]},${diagnostic_containers[2]}" \
    --ports "${REPLICA_METRICS_PORTS[0]},${REPLICA_METRICS_PORTS[1]},${REPLICA_METRICS_PORTS[2]}" \
    --ready-file "$NATIVE_WORK_DIR/diagnostics-ready.json" > "$NATIVE_WORK_DIR/diagnostics.log" 2>&1 &
  DIAGNOSTICS_PID=$!
  for _ in $(seq 1 50); do
    kill -0 "$DIAGNOSTICS_PID" 2>/dev/null || die "owned diagnostics collector exited"
    [[ ! -f "$NATIVE_WORK_DIR/diagnostics-ready.json" ]] || break
    sleep 0.1
  done
  [[ -f "$NATIVE_WORK_DIR/diagnostics-ready.json" ]] || die "diagnostics collector did not start"

  verify_receipt_peer_tls "$OUTDIR/peer_tls_pre.json"
fi

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
yq -o=json '.' "$SCENARIO_FILE" > "$OUTDIR/scenario.resolved.json"

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
  local p ready
  for p in "${REPLICA_METRICS_PORTS[@]}"; do
    ready=0
    for _ in $(seq 1 60); do
      if curl -fsS --max-time 2 "http://localhost:${p}/readyz" >/dev/null 2>&1; then
        ready=1
        break
      fi
      sleep 1
    done
    [[ "$ready" == 1 ]] || die "owned replica readiness did not succeed before warmup on port $p"
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

receipt_runtime_scalar() {
  local name="$1" text="$2" raw
  raw="$(awk -v n="$name" '$1 == n { if (NF != 2) exit 1; print $2 }' <<<"$text")" || return 1
  [[ "$raw" =~ ^([0-9]+([.][0-9]*)?|[.][0-9]+)([eE][+-]?[0-9]+)?$ ]] || return 1
  # Check decimal integrality before float parsing so rounding cannot hide a fraction.
  awk -v raw="$raw" '
    BEGIN {
      split(raw, notation, /[eE]/)
      mantissa = notation[1]
      dot = index(mantissa, ".")
      scale = (dot ? length(mantissa) - dot : 0) - (notation[2] + 0)
      digits = mantissa
      gsub(/\./, "", digits)
      if (digits ~ /[1-9]/ && scale > 0 &&
          (scale > length(digits) || substr(digits, length(digits) - scale + 1) ~ /[1-9]/)) {
        exit 1
      }
      value = raw + 0
      exit !(value >= 0 && value < 9007199254740992)
    }
  ' || return 1
  printf '%.0f' "$raw" 2>/dev/null
}

receipt_runtime_context() {
  # These fixed aggregate observations come from the same scrape as HeapAlloc.
  # They explain occupancy and GC timing; they do not change the leak verdict.
  local text="$1" name raw value ratio
  local -a args=()
  for name in go_gc_duration_seconds_count go_memstats_last_gc_time_seconds \
      go_memstats_next_gc_bytes lantern_receipt_entries lantern_receipt_bytes \
      lantern_receipt_oldest_deadline_seconds lantern_mutation_log_capacity \
      lantern_mutation_log_fill_ratio lantern_mutation_log_evicted_total; do
    raw="$(awk -v n="$name" '$1 == n { if (NF != 2) exit 1; print $2 }' <<<"$text")" || return 1
    case "$name" in
      go_memstats_last_gc_time_seconds | lantern_receipt_oldest_deadline_seconds | lantern_mutation_log_fill_ratio)
        [[ "$raw" =~ ^([0-9]+([.][0-9]*)?|[.][0-9]+)([eE][+-]?[0-9]+)?$ ]] || return 1
        value="$(jq -en --arg value "$raw" '$value | tonumber | select(. >= 0 and . < 9007199254740992)')" || return 1
        # Reject underflow and a ratio just above one even if float parsing
        # rounds the original decimal to zero or one.
        ratio=0
        if [[ "$name" == "lantern_mutation_log_fill_ratio" ]]; then ratio=1; fi
        awk -v raw="$raw" -v ratio="$ratio" '
          BEGIN {
            split(raw, notation, /[eE]/)
            dot = index(notation[1], ".")
            scale = (dot ? length(notation[1]) - dot : 0) - (notation[2] + 0)
            digits = notation[1]
            gsub(/\./, "", digits)
            sub(/^0+/, "", digits)
            if (digits != "" && raw + 0 == 0) exit 1
            if (ratio && digits != "" && (scale < length(digits) - 1 ||
                (scale == length(digits) - 1 && digits !~ /^10*$/))) exit 1
          }
        ' || return 1
        ;;
      *) value="$(receipt_runtime_scalar "$name" "$text")" || return 1 ;;
    esac
    args+=( --argjson "$name" "$value" )
  done
  jq -nc "${args[@]}" '$ARGS.named'
}

snapshot_runtime() {
  # OFF snapshots force a GC before sampling so heap_alloc_bytes reflects
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
  # Protected admission rejects pprof. Use one fixed natural-GC sample;
  # neither an inaccessible profiling route nor a minimum-of-samples fallback.
  if [[ "$receipt_driver" == "1" ]]; then rounds=1; fi
  [[ "$rounds" =~ ^[1-9][0-9]*$ ]] || die "runtime snapshot: invalid round count"
  [[ ${#REPLICA_METRICS_PORTS[@]} == 3 ]] || die "runtime snapshot: expected three replica metrics ports"
  local -a samples=()
  for port in "${REPLICA_METRICS_PORTS[@]}"; do
    local g="" h_inuse="" h_alloc="" h_objs="" vhe="" vhw=""
    for (( round = 1; round <= rounds; round++ )); do
      if [[ "$receipt_driver" != "1" ]]; then
        curl -fsS --max-time 10 "http://localhost:${port}/debug/pprof/heap?gc=1" -o /dev/null ||
          die "OFF runtime snapshot: forced GC failed for localhost:${port} (round ${round})"
      fi
      local text
      if [[ "$receipt_driver" == "1" ]]; then
        text="$(curl -fsS --max-time 5 "http://localhost:${port}/metrics")" ||
          die "receipt runtime snapshot: metrics scrape failed for localhost:${port} (round ${round})"
      else
        text="$(curl -fsS --max-time 5 "http://localhost:${port}/metrics")" ||
          die "OFF runtime snapshot: metrics scrape failed for localhost:${port} (round ${round})"
      fi
      local rg rhi rha rho rvhe rvhw
      # Prom client formats large gauges in scientific notation (e.g. 1.949696e+07).
      # Coerce to integer so downstream JSON consumers (jq + Go int64) don't choke.
      if [[ "$receipt_driver" == "1" ]]; then
        rg="$(receipt_runtime_scalar go_goroutines "$text")" ||
          die "receipt runtime snapshot: invalid go_goroutines for localhost:${port} (round ${round})"
        rhi="$(receipt_runtime_scalar go_memstats_heap_inuse_bytes "$text")" ||
          die "receipt runtime snapshot: invalid go_memstats_heap_inuse_bytes for localhost:${port} (round ${round})"
        rha="$(receipt_runtime_scalar go_memstats_heap_alloc_bytes "$text")" ||
          die "receipt runtime snapshot: invalid go_memstats_heap_alloc_bytes for localhost:${port} (round ${round})"
        rho="$(receipt_runtime_scalar go_memstats_heap_objects "$text")" ||
          die "receipt runtime snapshot: invalid go_memstats_heap_objects for localhost:${port} (round ${round})"
      else
        rg="$(receipt_runtime_scalar go_goroutines "$text")" && [[ "$rg" -gt 0 ]] ||
          die "OFF runtime snapshot: invalid go_goroutines for localhost:${port} (round ${round})"
        rhi="$(receipt_runtime_scalar go_memstats_heap_inuse_bytes "$text")" && [[ "$rhi" -gt 0 ]] ||
          die "OFF runtime snapshot: invalid go_memstats_heap_inuse_bytes for localhost:${port} (round ${round})"
        rha="$(receipt_runtime_scalar go_memstats_heap_alloc_bytes "$text")" && [[ "$rha" -gt 0 ]] ||
          die "OFF runtime snapshot: invalid go_memstats_heap_alloc_bytes for localhost:${port} (round ${round})"
        rho="$(receipt_runtime_scalar go_memstats_heap_objects "$text")" && [[ "$rho" -gt 0 ]] ||
          die "OFF runtime snapshot: invalid go_memstats_heap_objects for localhost:${port} (round ${round})"
      fi
      # vertexHLC LWW watermark map (#727): the instantaneous entry count
      # (lantern_vertex_hlc_entries — drained low right after a GC sweep) and
      # its sticky per-cycle peak (lantern_vertex_hlc_entries_high_water).
      # prom_scalar matches the full metric name, so the "_entries" query does
      # NOT collide with the "_entries_high_water" line. A low entries floor
      # paired with a large high-water is the born-expired ttl_churn retention
      # fingerprint (Go never shrinks the map's bucket array after delete).
      rvhe="$(receipt_runtime_scalar lantern_vertex_hlc_entries "$text")" ||
        die "runtime snapshot: invalid lantern_vertex_hlc_entries for localhost:${port} (round ${round})"
      rvhw="$(receipt_runtime_scalar lantern_vertex_hlc_entries_high_water "$text")" ||
        die "runtime snapshot: invalid lantern_vertex_hlc_entries_high_water for localhost:${port} (round ${round})"
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
    if [[ "$receipt_driver" == "1" ]]; then
      metric_samples="$(receipt_runtime_context "$text")" ||
        die "receipt runtime snapshot: invalid GC/receipt/log context for localhost:${port} (round ${round})"
    fi
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

prepare_ghz_diagnostics() {
  # Check imports and executable prerequisites before any ghz opens its pipe.
  [[ -f "$CAPTURE_DIR/ghz_stderr.py" && ! -L "$CAPTURE_DIR/ghz_stderr.py" ]] ||
    die "ghz stderr diagnostic helper missing"
  python3 -B "$CAPTURE_DIR/ghz_stderr.py" --check >/dev/null 2>&1 ||
    die "ghz stderr diagnostic helper startup failed"
  GHZ_BINARY="$(command -v ghz)"
  [[ -f "$GHZ_BINARY" && -r "$GHZ_BINARY" && -x "$GHZ_BINARY" ]] ||
    die "ghz stderr diagnostic binary unavailable"
  GHZ_DIAGNOSTICS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/lantern-ghz-producer.XXXXXXXX")"
  chmod 700 "$GHZ_DIAGNOSTICS_DIR"
}

save_ghz_exit() {
  # Numeric metadata only. Never persist the command, endpoint or request data.
  (umask 077; set -o noclobber
    printf '{"schema":1,"producer_index":%s,"ghz_pid":%s,"ghz_exit_status":%s}\n' "$1" "$2" "$3" > "$GHZ_DIAGNOSTICS_DIR/producer_$1.exit.json")
}

join_ghz_diagnostics() {
  # One shared diagnostic-only 3s wait, after all direct ghz children exited.
  # This can delay subsequent post-load observations by up to that wait.
  # The collectors drain beyond their capture cap, so ghz never sees cap SIGPIPE.
  local round pid job live=0 i
  for round in {1..30}; do
    live=0
    for pid in "${ghz_capture_pids[@]}"; do
      if ghz_diagnostic_job "$pid" >/dev/null; then live=1; fi
    done
    (( live != 0 )) || break
    sleep 0.1
  done
  for i in "${!ghz_capture_pids[@]}"; do
    pid="${ghz_capture_pids[$i]}"
    if job="$(ghz_diagnostic_job "$pid")"; then
      producer_diagnostics_failed=1
      # Resolve the registered live shell job, never a recycled OS PID.
      kill -KILL "$job" 2>/dev/null || true
    fi
    if ! wait "$pid"; then producer_diagnostics_failed=1; fi
    unset 'ghz_capture_pids[i]'
    rm -f -- "$GHZ_DIAGNOSTICS_DIR/producer_${i}.pipe" || producer_diagnostics_failed=1
  done
  if (( producer_diagnostics_failed != 0 )); then
    log "steady producer diagnostics incomplete (fixed classification only)"
  fi
}

ghz_diagnostic_job() {
  # Process substitutions are not jobs. Direct background collectors are;
  # a finished/reaped collector must not authorize signaling a reused PID.
  local spec leader state rest listing
  listing="$(LC_ALL=C jobs -l)"
  while read -r spec leader state rest; do
    if [[ "$spec" =~ ^\[([0-9]+)\][+-]?$ && "$leader" == "$1" &&
        ( "$state" == Running || "$state" == Stopped ) ]]; then
      printf '%%%s\n' "${BASH_REMATCH[1]}"
      return 0
    fi
  done <<< "$listing"
  return 1
}

prepare_receipt_edge_add() {
  # Seed both phases before warmup and the natural-GC PRE snapshot. Ordinary
  # authorized Put/Get preparation is outside the measured Add/lookup pair.
  [[ "$receipt_driver" == "1" && "$target_driver" == "receipt_edge_add" ]] || return 0
  (( warmup_rps % 2 == 0 && steady_rps % 2 == 0 )) ||
    die "receipt EdgeAdd aggregate RPS must divide evenly across its two producers"
  local endpoint_urls="" ep
  for ep in "${endpoints[@]}"; do
    [[ -z "$endpoint_urls" ]] || endpoint_urls+=","
    endpoint_urls+="https://${ep}"
  done
  (
    cd "$REPO_ROOT"
    go run ./testbed/bench/receiptprobe prepare-edge-add \
      -endpoints "$endpoint_urls" -ca-cert "$PEER_TLS_DIR/ca.pem" \
      -output "$OUTDIR/edge_add_fixture.json" \
      -warmup-duration "$warmup_duration" -warmup-pair-rps "$((warmup_rps / 2))" \
      -steady-duration "$steady_duration" -steady-pair-rps "$((steady_rps / 2))"
  )
}

run_receipt_probe() {
  # $1 phase, $2 concurrency, $3 aggregate RPS, $4 duration,
  # optional $5/$6 admission and lookup report paths.
  local phase="$1" conc="$2" aggregate_rps="$3" dur="$4"
  local admission_report="${5:-}" lookup_report="${6:-}"
  local calls_len pair_rps endpoint_urls="" ep expected_call
  case "$target_driver" in
    receipt_vertex_put) expected_call="PutVertex" ;;
    receipt_vertex_delete) expected_call="DeleteVertex" ;;
    receipt_edge_delete) expected_call="DeleteEdge" ;;
    receipt_edge_add) expected_call="AddEdge" ;;
    *) die "unsupported receipt driver $target_driver" ;;
  esac
  calls_len="$(yq -r '.target.calls | length' "$SCENARIO_FILE")"
  [[ "$calls_len" == "2" ]] ||
    die "$target_driver requires exactly two declared producers"
  (( aggregate_rps % calls_len == 0 )) ||
    die "receipt aggregate RPS ($aggregate_rps) must divide evenly across $calls_len producers"
  pair_rps=$((aggregate_rps / calls_len))
  [[ "$(yq -r '.target.calls[0].name' "$SCENARIO_FILE")" == "receipt_admission" &&
     "$(yq -r '.target.calls[0].call' "$SCENARIO_FILE")" == "graph.v1.LanternService/$expected_call" &&
     "$(yq -r '.target.calls[1].name' "$SCENARIO_FILE")" == "receipt_lookup" &&
     "$(yq -r '.target.calls[1].call' "$SCENARIO_FILE")" == "graph.v1.LanternService/GetReceiptStatus" ]] ||
    die "$target_driver has a missing or wrong admission/lookup RPC"
  for ep in "${endpoints[@]}"; do
    [[ -z "$endpoint_urls" ]] || endpoint_urls+=","
    endpoint_urls+="https://${ep}"
  done

  local args=(
    -endpoints "$endpoint_urls"
    -family "$target_driver"
    -ca-file "$PEER_TLS_DIR/ca.pem"
    -phase "$phase"
    -duration "$dur"
    -concurrency "$conc"
    -pair-rps "$pair_rps"
  )
  if [[ "$target_driver" == "receipt_edge_add" ]]; then
    args+=( -edge-add-fixture "$OUTDIR/edge_add_fixture.json" )
  fi
  if [[ -n "$admission_report" ]]; then
    args+=(
      -admission-report "$admission_report"
      -lookup-report "$lookup_report"
    )
  fi
  if [[ "$phase" == "steady" ]]; then
    local metrics_urls="" port
    for port in "${REPLICA_METRICS_PORTS[@]}"; do
      [[ -z "$metrics_urls" ]] || metrics_urls+=","
      metrics_urls+="http://localhost:${port}/metrics"
    done
    args+=(
      -metrics-endpoints "$metrics_urls"
      -metrics-operator-loopback
      -metrics-interval "$(yq -r '.leak_gate.steady_sample_interval' "$SCENARIO_FILE")"
      -metrics-report "$OUTDIR/runtime_steady.json"
    )
  fi
  (
    cd "$REPO_ROOT"
    go run ./testbed/bench/receiptprobe "${args[@]}"
  )
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

prepare_receipt_edge_add

capture_retained_heap() {
  [[ "$retained_heap" == "1" ]] || return 0
  local phase="$1" admission lookup steady_admission steady_lookup
  admission="$(jq -er '.count | select(type=="number" and .>0 and .<=8000 and floor==.)' "$OUTDIR/ghz_warmup_0_receipt_admission.json")" || die "missing completed warmup admission count"
  lookup="$(jq -er '.count | select(type=="number" and .>0 and .<=8000 and floor==.)' "$OUTDIR/ghz_warmup_1_receipt_lookup.json")" || die "missing completed warmup lookup count"
  if [[ "$phase" == "post" ]]; then
    steady_admission="$(jq -er '.count | select(type=="number" and .>0 and .<=8000 and floor==.)' "$OUTDIR/ghz_steady_0_receipt_admission.json")" || die "missing completed steady admission count"
    steady_lookup="$(jq -er '.count | select(type=="number" and .>0 and .<=8000 and floor==.)' "$OUTDIR/ghz_steady_1_receipt_lookup.json")" || die "missing completed steady lookup count"
    admission=$((admission + steady_admission))
    lookup=$((lookup + steady_lookup))
  fi
  python3 -B "$CAPTURE_DIR/heap_control.py" capture --directory "$LANTERN_BENCH_HEAP_CONTROL_ROOT" \
    --phase "$phase" --admission "$admission" --lookup "$lookup" || die "retained cut incomplete; owned cleanup remains mandatory"
}

# ----- WARMUP ----------------------------------------------------------------
log "warmup: ${warmup_duration} @ c=${warmup_conc} rps=${warmup_rps}"
if [[ "$receipt_driver" == "1" ]]; then
  run_receipt_probe warmup "$warmup_conc" "$warmup_rps" "$warmup_duration" \
    "$OUTDIR/ghz_warmup_0_receipt_admission.json" \
    "$OUTDIR/ghz_warmup_1_receipt_lookup.json" >/dev/null
else
  warm_call="$(yq -r '.target.call // .target.calls[0].call' "$SCENARIO_FILE")"
  warm_data="$(yq -r '.target.data_template // .target.calls[0].data_template' "$SCENARIO_FILE")"
  run_ghz warmup "${endpoints[0]}" "$warm_call" "$warm_data" "$warmup_conc" "$warmup_rps" "$warmup_duration" >/dev/null
fi

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
capture_retained_heap baseline
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
    [[ "$sub_call" == "graph.v1.LanternChangeService/WatchChanges" ]] || die "external consumer requires public WatchChanges"
    "$CHANGEPROBE" -endpoint "http://$ep" -request "$sub_data" -duration "$steady_duration" \
      -out "$OUTDIR/changes_sub_${i}.json" > "$OUTDIR/changes_sub_${i}.log" 2>&1 &
    sub_pids+=( "$!" )
  done
  log "launched $sub_count subscribe streams"
fi

if [[ "$sub_consumers_len" != "0" && "$sub_consumers_len" != "null" ]]; then
  for i in $(seq 0 $(( sub_consumers_len - 1 ))); do
    ep="$(yq -r ".subscribe.consumers[$i].endpoint" "$SCENARIO_FILE")"
    call="$(yq -r ".subscribe.consumers[$i].call" "$SCENARIO_FILE")"
    data="$(yq -r ".subscribe.consumers[$i].data_template" "$SCENARIO_FILE")"
    [[ "$call" == "graph.v1.LanternChangeService/WatchChanges" ]] || die "external consumer requires public WatchChanges"
    "$CHANGEPROBE" -endpoint "http://$ep" -request "$data" -duration "$steady_duration" \
      -out "$OUTDIR/changes_sub_consumer_${i}.json" > "$OUTDIR/changes_sub_consumer_${i}.log" 2>&1 &
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
    chaos_child=""
    trap 'stop_owned_processes "${chaos_child:-}"; exit 143' TERM INT
    chaos_command() {
      "$@" &
      chaos_child="$!"
      local command_status=0
      wait "$chaos_child" || command_status=$?
      chaos_child=""
      return "$command_status"
    }
    sleep_secs() { local v="$1"; chaos_command sleep "${v%s}"; }
    sleep_secs "$kill_at"
    log "chaos: docker kill $chaos_target"
    [[ "$chaos_target" =~ ^lantern-[012]$ ]] || die "unknown native chaos workload"
    previous_container="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$chaos_target")"
    docker exec "$previous_container" test -s /state/membership.state || die "native restart floor missing"
    chaos_command docker compose "${COMPOSE_FILES[@]}" kill "$chaos_target" >/dev/null
    sleep_secs "$restart_at"
    log "chaos: explicit native resume for $chaos_target"
    chaos_command "$AUTHFIXTURE" -compose -restart-node "$chaos_target" -directory "$PEER_TLS_DIR" || die "native restart preparation failed"
    chaos_command docker compose "${COMPOSE_FILES[@]}" up -d --no-deps --force-recreate --wait "$chaos_target" >/dev/null
    restarted_container="$(docker compose "${COMPOSE_FILES[@]}" ps -q "$chaos_target")"
    jq -nc --arg service "$chaos_target" --arg old "$previous_container" --arg new "$restarted_container" \
      '{service:$service,previous_container:$old,restarted_container:$new}' > "$OUTDIR/native_chaos.json"
  ) &
  chaos_pid="$!"
fi

# Steady producer(s). The receipt driver owns its coordinated pair. Otherwise,
# if `.target.calls` is a list, fork one ghz per call at floor(rps / N); a
# scalar target runs one ghz against the primary endpoint.
calls_len="$(yq -r '.target.calls | length // 0' "$SCENARIO_FILE")"
prod_pids=()
producer_failed=0
producer_diagnostics_failed=0
consumer_failed=0
chaos_failed=0
if [[ "$receipt_driver" == "1" ]]; then
  if ! run_receipt_probe \
    steady \
    "$steady_conc" \
    "$steady_rps" \
    "$steady_duration" \
    "$OUTDIR/ghz_steady_0_receipt_admission.json" \
    "$OUTDIR/ghz_steady_1_receipt_lookup.json"; then
    producer_failed=1
    log "steady receipt producer exited non-zero"
  fi
elif [[ "$calls_len" != "0" && "$calls_len" != "null" ]]; then
  prepare_ghz_diagnostics
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
    ghz_stderr_pipe="$GHZ_DIAGNOSTICS_DIR/producer_${i}.pipe"
    (umask 077; mkfifo "$ghz_stderr_pipe")
    python3 -B "$CAPTURE_DIR/ghz_stderr.py" "$GHZ_DIAGNOSTICS_DIR/producer_${i}.stderr.json" "$GHZ_BINARY" < "$ghz_stderr_pipe" &
    ghz_capture_pids[$i]=$!
    ghz --insecure \
      --call "$call" -c "$steady_conc" --rps "$producer_rps" -z "$steady_duration" \
      -d "$data" --format json -o "$f" "$ep" >/dev/null 2>"$ghz_stderr_pipe" &
    prod_pids+=( "$!" )
  done
  # `wait pid...` returns only one aggregate status and, under `set -e`, can
  # either hide an earlier producer failure or abort before lifecycle/report
  # diagnostics are captured. Reap every producer independently and carry a
  # sticky failure bit through the normal post-steady evidence path.
  for i in "${!prod_pids[@]}"; do
    producer_pid="${prod_pids[$i]}"
    if wait "${prod_pids[$i]}"; then
      producer_status=0
    else
      producer_status=$?
      producer_failed=1
      log "steady producer[$i] exited non-zero (status=$producer_status)"
    fi
    unset 'prod_pids[i]'
    if ! save_ghz_exit "$i" "$producer_pid" "$producer_status"; then
      producer_diagnostics_failed=1
    fi
  done
  join_ghz_diagnostics
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
for consumer_index in "${!sub_pids[@]}"; do
  consumer_pid="${sub_pids[$consumer_index]}"
  if ! wait "$consumer_pid"; then
    consumer_failed=$(( consumer_failed + 1 ))
    log "public CDC consumer failed; scenario is unqualified"
  fi
  unset 'sub_pids[consumer_index]'
done
if [[ -n "$chaos_pid" ]]; then
  if ! wait "$chaos_pid"; then chaos_failed=1; log "declared native chaos recovery failed"; fi
  chaos_pid=""
fi

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
capture_retained_heap post

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
if [[ "$receipt_driver" == "1" ]]; then
  steady_h_thresh_mb="$(yq -r '.leak_gate.steady_heap_alloc_max_delta_mb' "$SCENARIO_FILE")"
  # Receipt sampling runs inside the steady driver, including with
  # LEAK_GATE_ONLY=1. Require all three unforced /metrics series in addition
  # to the fixed post-warmup/post-cooldown natural-GC snapshots.
  receipt_heap_protocol="natural_reference_v1"
  retained_eval_args=()
  if [[ "$retained_heap" == "1" ]]; then
    receipt_heap_protocol="symmetric_post_gc_2_v1"
    retained_eval_args=(-retained-pre "$OUTDIR/retained_heap_pre.json" -retained-post "$OUTDIR/retained_heap_post.json")
  fi
  if ! (
    cd "$REPO_ROOT"
    go run ./testbed/bench/receiptprobe evaluate-leak \
      -pre "$OUTDIR/runtime_pre.json" \
      -post "$OUTDIR/runtime_post.json" \
      -steady "$OUTDIR/runtime_steady.json" \
      -duration "$steady_duration" \
      -interval "$(yq -r '.leak_gate.steady_sample_interval' "$SCENARIO_FILE")" \
      -max-goroutines "$g_thresh" \
      -max-heap-mb "$h_thresh_mb" \
      -max-steady-heap-mb "$steady_h_thresh_mb" \
      -snapshot-protocol "$receipt_heap_protocol" \
      "${retained_eval_args[@]}" \
      -out "$OUTDIR/leak_gate.json"
  ); then
    log "receipt leak gate reported failure"
  fi
else
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
fi
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
# the exit code for local qualification and optional hosted diagnostics.
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
verify_native_workload_settings
verify_receipt_image_provenance "$OUTDIR/image_provenance_post.json"
if [[ "$receipt_driver" == "1" ]]; then
  verify_receipt_peer_tls "$OUTDIR/peer_tls_post.json" "$OUTDIR/peer_tls_pre.json"
  cp "$PEER_TLS_DIR/renewal.json" "$OUTDIR/membership_renewal.json"
fi
workload_verdict="pass"
if (( producer_failed != 0 || consumer_failed != 0 || chaos_failed != 0 || producer_diagnostics_failed != 0 )); then workload_verdict="fail"; fi
jq -n --arg verdict "$workload_verdict" --argjson producer_failed "$producer_failed" \
  --argjson producer_diagnostics_failed "$producer_diagnostics_failed" \
  --argjson consumer_failed "$consumer_failed" --argjson chaos_failed "$chaos_failed" \
  '{verdict:$verdict,producer_failed:$producer_failed,producer_diagnostics_failed:$producer_diagnostics_failed,failed_consumers:$consumer_failed,chaos_failed:$chaos_failed}' > "$OUTDIR/workload_gate.json"
render_report
printf '\n**Workload gate verdict:** `%s` (producer failure: %s; producer diagnostics failure: %s; failed consumers: %s; chaos failure: %s).\n' \
  "$workload_verdict" "$producer_failed" "$producer_diagnostics_failed" "$consumer_failed" "$chaos_failed" >> "$OUTDIR/report.md"

formal_status=1
# Tagged load must still meet the existing perf/workload floors so an
# underloaded retained sample cannot pass. It does not qualify normal perf.
if [[ "$verdict" == "pass" && "$metric_verdict" != "fail" && "$semantic_verdict" != "fail" && "$perf_verdict" != "fail" && "$workload_verdict" == "pass" ]]; then formal_status=0; fi
if finish_with_natural_gc_observation "$formal_status"; then exit 0; else exit 1; fi
