#!/usr/bin/env bash
# Snapshot key Prometheus series for Lantern.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
TESTBED="$(cd "$HERE/.." && pwd)"
OUT="$TESTBED/out/metrics"
mkdir -p "$OUT"

PROM="http://localhost:9091"

q() {
  local name="$1" query="$2"
  curl -sG --data-urlencode "query=$query" "$PROM/api/v1/query" \
    | python3 -m json.tool > "$OUT/$name.json"
  echo "==> $name"
  python3 -c '
import json,sys
d=json.load(open("'"$OUT/$name.json"'"))
for r in d.get("data",{}).get("result",[]):
    print(" ", r["metric"], "=", r["value"][1])
'
}

q vertices            'lantern_vertices'
q edges               'lantern_edges'
q ttl_expirations     'sum by (kind) (lantern_ttl_expirations_total)'
q gc_p95              'histogram_quantile(0.95, sum by (le) (rate(lantern_gc_duration_seconds_bucket[5m])))'
q gc_count            'sum(lantern_gc_duration_seconds_count)'
q build_info          'lantern_build_info'
q go_routines         'go_goroutines'
q process_rss         'process_resident_memory_bytes'

echo
echo "Snapshots in $OUT/"
