#!/usr/bin/env bash
# release.sh — release-time bench driver.
#
# Runs every scenario listed in testbed/bench/release-scenarios.txt against
# a single shared Compose stack and produces ONE aggregated bench-report.md
# in the fixed format expected by the release workflow.
#
# Usage:
#   ./testbed/bench/release.sh [--out PATH]
#
# Environment knobs:
#   TAG                          release tag (default: `git describe --tags --abbrev=0`)
#   COMMIT                       release commit SHA (default: `git rev-parse HEAD`)
#   RUNNER                       runner platform string (default: `uname -s/uname -m`, lowercased)
#   LANTERN_IMAGE                image to run (default: lantern:local — must exist locally)
#   KEEP_OUT=1                   do not delete the per-scenario `out/` tree afterwards
#   RELEASE_BENCH_BUDGET_SECONDS wall-clock budget for the scenario sweep (default: 5400).
#                                Once exhausted, the remaining scenarios are skipped, the
#                                report is aggregated from whatever ran (with a `## Truncated`
#                                note appended), and the script still exits 0. Set to 0 to
#                                disable the budget and always run every scenario.
#
# Exit codes:
#   0  every scenario that RAN passed its leak gate (the sweep may have been
#      truncated by the wall-clock budget — truncation alone is not a failure)
#   1  the aggregator ran but at least one scenario that ran failed or fail-gated
#   2  setup error (missing tools, missing image, etc.)
#
# Design notes:
#   - We DO bring up Compose ourselves once (not inside run.sh) and pass
#     SKIP_UP=1/KEEP_UP=1 to run.sh for every scenario, so all scenarios
#     hit the same 3-replica cluster. This keeps wall-time bounded and
#     ensures every scenario observes the same baseline.
#   - We DO NOT abort on a single scenario failure — the aggregator marks
#     missing scenarios as `(failed)` rows and the workflow surfaces the
#     overall verdict via this script's exit code.
#   - We self-bound the sweep via RELEASE_BENCH_BUDGET_SECONDS so the report is
#     always aggregated and uploaded *before* the CI job's hard `timeout-minutes`
#     can cancel us mid-loop (which would discard the report entirely). The job
#     timeout is the backstop; this budget is the graceful cutoff.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
COMPOSE_FILES=(
  -f "$REPO_ROOT/deploy/compose/docker-compose.yml"
  -f "$HERE/compose.override.yml"
)
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-lantern-bench-release}"

OUT_PATH="bench-report.md"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT_PATH="$2"; shift 2 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "release.sh: unknown arg $1" >&2; exit 2 ;;
  esac
done

die() { echo "release.sh: $*" >&2; exit 2; }
log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }
need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }

need docker; need ghz; need yq; need jq; need curl; need go; need git

TAG="${TAG:-$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null || echo unknown)}"
COMMIT="${COMMIT:-$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo unknown)}"
COMMIT_SHORT="${COMMIT:0:7}"
RUNNER="${RUNNER:-$(uname -s | tr '[:upper:]' '[:lower:]')/$(uname -m)}"
CAPTURED="$(date -u +%Y%m%dT%H%M%SZ)"

SCENARIO_LIST="$HERE/release-scenarios.txt"
[[ -f "$SCENARIO_LIST" ]] || die "missing $SCENARIO_LIST"

scenarios=()
while IFS= read -r line; do
  line="${line%%#*}"
  line="${line//[[:space:]]/}"
  [[ -z "$line" ]] && continue
  scenarios+=("$line")
done < "$SCENARIO_LIST"

[[ ${#scenarios[@]} -gt 0 ]] || die "no scenarios in $SCENARIO_LIST"

# Wall-clock budget for the scenario sweep. The release workflow caps the whole
# job via `timeout-minutes`; this budget is the *graceful* cutoff that lets us
# aggregate a partial report and exit cleanly BEFORE the runner hard-kills the
# job (which would discard the report entirely, leaving the release notes with a
# placeholder). The default (5400s = 90 min) leaves the full canonical sweep
# (~83 min) room to finish, so it only trips when a runner is abnormally slow or
# the scenario set grows. Set RELEASE_BENCH_BUDGET_SECONDS=0 to disable.
BUDGET_SECONDS="${RELEASE_BENCH_BUDGET_SECONDS:-5400}"

log "release-bench: tag=$TAG commit=$COMMIT_SHORT runner=$RUNNER captured=$CAPTURED"
log "release-bench: scenarios=${scenarios[*]}"
log "release-bench: sweep budget=${BUDGET_SECONDS}s (0 = unbounded)"

# Verify the image exists locally so we fail fast.
img="${LANTERN_IMAGE:-lantern:local}"
if ! docker image inspect "$img" >/dev/null 2>&1; then
  die "docker image $img not found; build it first (e.g. \`docker build -t lantern:local .\`)"
fi
export LANTERN_IMAGE="$img"

# Bring up the shared cluster once.
log "compose up (project=$COMPOSE_PROJECT_NAME, image=$img)"
# Since #435 the canonical compose declares three explicit lantern-{0,1,2}
# services, so `--scale lantern=3` is no longer needed.
docker compose "${COMPOSE_FILES[@]}" up -d --wait

cleanup() {
  log "compose down"
  docker compose "${COMPOSE_FILES[@]}" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Track each scenario's output directory and overall verdict.
scenario_args=()
skipped=()
fail_count=0
sweep_start="$(date +%s)"
budget_hit=0

for s in "${scenarios[@]}"; do
  # Graceful wall-clock cutoff: once the budget is spent, skip the rest of the
  # sweep instead of letting the CI job's hard timeout cancel us mid-scenario.
  if [[ "$budget_hit" -eq 0 && "$BUDGET_SECONDS" -gt 0 ]]; then
    elapsed=$(( $(date +%s) - sweep_start ))
    if [[ "$elapsed" -ge "$BUDGET_SECONDS" ]]; then
      log "sweep budget ${BUDGET_SECONDS}s reached after ${elapsed}s — skipping remaining scenarios"
      budget_hit=1
    fi
  fi
  if [[ "$budget_hit" -eq 1 ]]; then
    log "scenario $s: SKIPPED (sweep budget exhausted)"
    skipped+=("$s")
    continue
  fi

  log "scenario: $s"
  before_dir="$HERE/out/$s"
  mkdir -p "$before_dir"
  # Snapshot existing run directories so we can identify the new one.
  before_listing="$(ls -1 "$before_dir" 2>/dev/null | sort || true)"

  # SKIP_UP=1 + KEEP_UP=1 → run.sh reuses our cluster and leaves it alone.
  if SKIP_UP=1 KEEP_UP=1 "$HERE/run.sh" "$s"; then
    log "scenario $s: PASS"
  else
    log "scenario $s: FAIL (leak gate or harness error)"
    fail_count=$((fail_count + 1))
  fi

  after_listing="$(ls -1 "$before_dir" 2>/dev/null | sort || true)"
  new_dir=""
  while IFS= read -r d; do
    if ! grep -Fxq "$d" <<<"$before_listing"; then
      new_dir="$d"
    fi
  done <<<"$after_listing"

  if [[ -n "$new_dir" ]]; then
    scenario_args+=("$s=$before_dir/$new_dir")
  else
    log "scenario $s: no new output directory found — recording as failed"
    scenario_args+=("$s=")
    fail_count=$((fail_count + 1))
  fi
done

log "aggregating into $OUT_PATH"
go run "$HERE/release" \
  -tag "$TAG" \
  -commit "$COMMIT_SHORT" \
  -runner "$RUNNER" \
  -captured "$CAPTURED" \
  -out "$OUT_PATH" \
  "${scenario_args[@]}"

# If the budget truncated the sweep, the aggregated report only covers the
# scenarios that ran. Append an honest note listing what was skipped so readers
# of the release notes know the sweep was partial (and why).
if [[ ${#skipped[@]} -gt 0 ]]; then
  log "sweep truncated: ${#skipped[@]} scenario(s) skipped: ${skipped[*]}"
  {
    printf '\n## Truncated\n\n'
    printf 'The release bench wall-clock budget (%ss) was reached before the full ' "$BUDGET_SECONDS"
    printf 'scenario sweep completed. %d of %d scenario(s) were skipped and are ' \
      "${#skipped[@]}" "${#scenarios[@]}"
    printf 'absent from the summary above:\n\n'
    for s in "${skipped[@]}"; do
      printf -- '- `%s`\n' "$s"
    done
  } >> "$OUT_PATH"
fi

log "done: $OUT_PATH (failures=$fail_count, skipped=${#skipped[@]})"
if [[ "${KEEP_OUT:-0}" != "1" ]]; then
  rm -rf "$HERE/out"
fi

if [[ "$fail_count" -gt 0 ]]; then
  exit 1
fi
exit 0
