#!/usr/bin/env bash
# Reproducible host-memory GC stress for issue #1183. The default matrix runs
# three 100-tick repetitions per shape and budget; it is intentionally opt-in.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
if ! git diff --quiet HEAD; then
  echo 'Commit measured source before running gc_stress.sh; SHA must identify exact code.' >&2
  exit 2
fi

sha="$(git rev-parse HEAD)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output_dir="${GC_OUTPUT_DIR:-$repo_root/testbed/bench/out/edge_ttl_churn/$timestamp}"
vertices="${GC_VERTICES:-100000}"
degree="${GC_DEGREE:-32}"
ticks="${GC_TICKS:-100}"
interval_ms="${GC_INTERVAL_MS:-1000}"
repetitions="${GC_REPETITIONS:-3}"
budgets="${GC_BUDGETS:-0 5000}"
shapes="${GC_SHAPES:-uniform hub}"
resource_trace="${GC_RESOURCE_TRACE:-0}"
mkdir -p "$output_dir"

{
  printf 'sha=%s\n' "$sha"
  printf 'measured_at_utc=%s\n' "$timestamp"
  printf 'uname=%s\n' "$(uname -srm)"
  printf 'go_version=%s\n' "$(go version)"
  printf 'goos=%s\n' "$(go env GOOS)"
  printf 'goarch=%s\n' "$(go env GOARCH)"
  printf 'vertices=%s\ndegree=%s\nticks=%s\ninterval_ms=%s\n' "$vertices" "$degree" "$ticks" "$interval_ms"
  printf 'repetitions=%s\nbudgets=%s\nshapes=%s\n' "$repetitions" "$budgets" "$shapes"
  printf 'resource_trace=%s\n' "$resource_trace"
  for property in machdep.cpu.brand_string hw.ncpu hw.memsize; do
    if value="$(sysctl -n "$property" 2>/dev/null)"; then
      printf '%s=%s\n' "$property" "$value"
    fi
  done
  if command -v docker >/dev/null 2>&1; then
    if value="$(docker info --format '{{.MemTotal}}' 2>/dev/null)"; then
      printf 'docker_vm_mem_bytes=%s\n' "$value"
    fi
  fi
} > "$output_dir/host.txt"

for shape in $shapes; do
  for budget in $budgets; do
    for ((run = 1; run <= repetitions; run++)); do
      output="$output_dir/gc_${shape}_b${budget}_r${run}.json"
      printf 'GC %s budget=%s repetition=%s/%s -> %s\n' "$shape" "$budget" "$run" "$repetitions" "$output"
      stress_env=(
        "LANTERN_GC_STRESS_OUT=$output"
        "LANTERN_GC_STRESS_SHA=$sha"
        "LANTERN_GC_STRESS_VERTICES=$vertices"
        "LANTERN_GC_STRESS_DEGREE=$degree"
        "LANTERN_GC_STRESS_TICKS=$ticks"
        "LANTERN_GC_STRESS_INTERVAL_MS=$interval_ms"
        "LANTERN_GC_STRESS_BUDGET=$budget"
        "LANTERN_GC_STRESS_SHAPE=$shape"
      )
      if [[ "$resource_trace" == 1 ]]; then
        if [[ "$(uname -s)" == Darwin ]]; then
          time_args=(-l)
        else
          time_args=(-v)
        fi
        (
          cd core
          /usr/bin/time "${time_args[@]}" /usr/bin/env "${stress_env[@]}" \
            go test ./graphcache -run '^TestGraphCache_GCStress$' -count=1 -timeout=30m -v
        ) 2> "$output_dir/gc_${shape}_b${budget}_r${run}.resource.txt" \
          | tee "$output_dir/gc_${shape}_b${budget}_r${run}.log"
      else
        (
          cd core
          /usr/bin/env "${stress_env[@]}" \
            go test ./graphcache -run '^TestGraphCache_GCStress$' -count=1 -timeout=30m -v
        ) | tee "$output_dir/gc_${shape}_b${budget}_r${run}.log"
      fi
    done
  done
done

if [[ "${GC_SKIP_BENCHMARKS:-0}" != 1 ]]; then
  printf 'Running ten target-scale samples of full and incremental GC benchmarks\n'
  (
    cd core
    go test ./graphcache -run '^$' \
      -bench '^(BenchmarkGCFlush|BenchmarkGCFlushIncremental)$/^v100k_d32_k80$' \
      -benchtime=1x -count=10 -timeout=60m
  ) | tee "$output_dir/benchmark.txt"
  benchstat "$output_dir/benchmark.txt" > "$output_dir/benchstat.txt"
fi

go run ./testbed/bench/report -dir "$output_dir" -scenario edge_ttl_churn -timestamp "$timestamp" > "$output_dir/report.md"
printf 'Completed %s\n' "$output_dir/report.md"
