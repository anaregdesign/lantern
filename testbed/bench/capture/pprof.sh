#!/usr/bin/env bash
# pprof.sh — snapshot pprof profiles from every replica.
#
# Args:
#   $1  output directory                (e.g. out/write_heavy/20250101T000000Z/pprof)
#   $2  phase tag                       ("pre" | "post")
#   $@  remaining: replica host:port    (default: localhost:9390 9391 9392)
#
# Per replica + per phase we capture:
#   - heap        (always, cheap)
#   - goroutine   (always, cheap)
#   - allocs      (always, cheap)
#
# Per replica + post-phase only we additionally capture:
#   - mutex       (requires LANTERN_MUTEX_PROFILE_FRACTION != 0)
#   - block       (requires LANTERN_BLOCK_PROFILE_RATE != 0)
#   - profile?seconds=30   (CPU, 30s — opt in via PPROF_CPU=1)
#
# All profiles are .pb.gz files named:
#   <replica>__<phase>__<profile>.pb.gz
#
# Quiet on success; loud on failure. Returns non-zero if any required
# profile fetch fails — but missing mutex/block from a misconfigured run
# is reported as a warning, not a hard error, so the bench can still
# proceed and emit a partial report.

set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: pprof.sh <outdir> <pre|post> [host:port ...]" >&2
  exit 2
fi

outdir="$1"; shift
phase="$1"; shift
if [[ $# -eq 0 ]]; then
  set -- localhost:9390 localhost:9391 localhost:9392
fi

mkdir -p "$outdir"

fetch() {
  local replica="$1" name="$2" url="$3" required="$4"
  local safe="${replica//[:\/]/_}"
  local out="$outdir/${safe}__${phase}__${name}.pb.gz"
  if curl -fsS --max-time 60 "$url" -o "$out"; then
    return 0
  fi
  if [[ "$required" == "true" ]]; then
    echo "pprof.sh: required profile $name from $replica failed: $url" >&2
    return 1
  fi
  echo "pprof.sh: optional profile $name from $replica unavailable ($url) — skipping" >&2
  rm -f "$out"
  return 0
}

for replica in "$@"; do
  base="http://${replica}/debug/pprof"
  fetch "$replica" heap      "$base/heap"      true
  fetch "$replica" goroutine "$base/goroutine" true
  fetch "$replica" allocs    "$base/allocs"    true
  if [[ "$phase" == "post" ]]; then
    fetch "$replica" mutex "$base/mutex" false
    fetch "$replica" block "$base/block" false
    if [[ "${PPROF_CPU:-0}" == "1" ]]; then
      fetch "$replica" cpu "$base/profile?seconds=30" false
    fi
  fi
done
