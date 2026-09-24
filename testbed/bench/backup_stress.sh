#!/usr/bin/env bash
# Host-memory periodic Backupper + named real-h2c read producers (#1183).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
if ! git diff --quiet HEAD; then
  echo 'Commit measured source before running backup_stress.sh; SHA must identify exact code.' >&2
  exit 2
fi

sha="$(git rev-parse HEAD)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output_dir="${BACKUP_OUTPUT_DIR:-$repo_root/testbed/bench/out/periodic_backup/$timestamp}"
vertices="${BACKUP_VERTICES:-100000}"
degree="${BACKUP_DEGREE:-32}"
interval_ms="${BACKUP_INTERVAL_MS:-30000}"
read_rps="${BACKUP_READ_RPS:-200}"
write_rps="${BACKUP_WRITE_RPS:-50}"
mkdir -p "$output_dir"

{
  printf 'sha=%s\nmeasured_at_utc=%s\n' "$sha" "$timestamp"
  printf 'uname=%s\ngo_version=%s\n' "$(uname -srm)" "$(go version)"
  printf 'goos=%s\ngoarch=%s\n' "$(go env GOOS)" "$(go env GOARCH)"
  printf 'vertices=%s\ndegree=%s\ninterval_ms=%s\nread_rps_per_producer=%s\nwrite_rps=%s\n' \
    "$vertices" "$degree" "$interval_ms" "$read_rps" "$write_rps"
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

output="$output_dir/backup_periodic.json"
LANTERN_BACKUP_STRESS_OUT="$output" \
LANTERN_BACKUP_STRESS_SHA="$sha" \
LANTERN_BACKUP_STRESS_VERTICES="$vertices" \
LANTERN_BACKUP_STRESS_DEGREE="$degree" \
LANTERN_BACKUP_STRESS_INTERVAL_MS="$interval_ms" \
LANTERN_BACKUP_STRESS_READ_RPS="$read_rps" \
LANTERN_BACKUP_STRESS_WRITE_RPS="$write_rps" \
go test ./tests/integration -run '^TestPeriodicBackupStress$' -count=1 -timeout=30m -v \
  | tee "$output_dir/backup_test.log"

go run ./testbed/bench/report -dir "$output_dir" -scenario periodic_backup -timestamp "$timestamp" > "$output_dir/report.md"
printf 'Completed %s\n' "$output_dir/report.md"
