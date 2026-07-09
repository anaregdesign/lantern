#!/usr/bin/env bash
# Exercise every CLI subcommand against the running testbed.
#
# Requires:
#   - testbed running (docker compose up -d)
#   - Go toolchain (uses `go run ./cli` so we don't need to install a binary)
#
# Writes a per-command log under testbed/out/cli/.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
TESTBED="$(cd "$HERE/.." && pwd)"
REPO="$(cd "$TESTBED/.." && pwd)"

OUT="$TESTBED/out/cli"
FIXTURES="$OUT/fixtures"
mkdir -p "$OUT" "$FIXTURES"

ADDR="localhost:6380"
META=(--address "$ADDR")

run() {
  local name="$1"; shift
  local log="$OUT/$name.log"
  echo "==> $name"
  {
    echo "\$ go run ./cli $*"
    ( cd "$REPO" && go run ./cli "$@" )
    echo "[rc=$?]"
  } >"$log" 2>&1
  tail -n1 "$log" | sed 's/^/    /'
}

# -- version / help --------------------------------------------------
run version            version
run help               --help

# -- vertex put/get/delete (single) ----------------------------------
run vertex-put-string  "${META[@]}" put vertex alice 'Alice Smith' 300
run vertex-put-int     "${META[@]}" put vertex count 42 300
run vertex-put-float   "${META[@]}" put vertex price 19.99 300
run vertex-put-bool    "${META[@]}" put vertex alive true 300
run vertex-put-json    "${META[@]}" put vertex bob '{"age":30}' 300 type=json
run vertex-put-dt      "${META[@]}" put vertex epoch '2025-01-01T00:00:00Z' 300 type=datetime
run vertex-put-dur     "${META[@]}" put vertex cooldown 30s 300 type=duration
run vertex-put-zip     "${META[@]}" put vertex zip 01234 300 type=string
run vertex-put-shortlived "${META[@]}" put vertex ephem hi 2

run vertex-get-string  "${META[@]}" get vertex alice
run vertex-get-int     "${META[@]}" get vertex count
run vertex-get-json    "${META[@]}" get vertex bob
run vertex-get-missing "${META[@]}" get vertex does-not-exist

# -- edge add/put/get/delete (single) --------------------------------
run edge-add-1         "${META[@]}" add edge alice bob 1.5 300
run edge-add-2         "${META[@]}" add edge alice bob 0.5 300
run edge-get-additive  "${META[@]}" get edge alice bob
run edge-put-replace   "${META[@]}" put edge alice bob 0.25 300
run edge-get-after-put "${META[@]}" get edge alice bob
run edge-get-missing   "${META[@]}" get edge alice nobody

# -- graph walk family verbs -----------------------------------------
run edge-add-carol      "${META[@]}" add edge alice carol 0.8 300
run edge-add-dave       "${META[@]}" add edge bob dave 0.6 300
run edge-add-carol-dave "${META[@]}" add edge carol dave 0.4 300

run bfs-none             "${META[@]}" bfs alice --step 3 --fan-out 10
run bfs-mst-min          "${META[@]}" bfs alice --step 3 --fan-out 10 --reduction mst --objective min
run bfs-spt-max          "${META[@]}" bfs alice --step 3 --fan-out 10 --reduction spt --objective max
run bfs-tfidf            "${META[@]}" bfs alice --step 2 --fan-out 5 --weighting tfidf
run pagerank-default     "${META[@]}" pagerank alice --top-n 8
run community-mst        "${META[@]}" community alice --max-size 30 --reduction mst --objective min

# -- bulk: NDJSON ----------------------------------------------------
{
  for i in 1 2 3 4 5; do
    printf '{"key":"bulk-v%d","value":%d,"ttl":"5m"}\n' "$i" "$i"
  done
} > "$FIXTURES/vertices.ndjson"
{
  echo '{"tail":"bulk-v1","head":"bulk-v2","weight":0.1,"ttl":"5m"}'
  echo '{"tail":"bulk-v2","head":"bulk-v3","weight":0.2,"ttl":"5m"}'
  echo '{"tail":"bulk-v3","head":"bulk-v4","weight":0.3,"ttl":"5m"}'
} > "$FIXTURES/edges.ndjson"

run bulk-vertices       "${META[@]}" bulk vertices "$FIXTURES/vertices.ndjson"
run bulk-edges-add      "${META[@]}" bulk edges add "$FIXTURES/edges.ndjson"
run bulk-edges-put      "${META[@]}" bulk edges put "$FIXTURES/edges.ndjson"

# bulk-vertices-stdin: separate helper because pipes don't fit run()
echo "==> bulk-vertices-stdin"
{
  echo "\$ cat ... | go run ./cli ${META[*]} bulk vertices -"
  ( cd "$REPO" && cat "$FIXTURES/vertices.ndjson" | go run ./cli "${META[@]}" bulk vertices - )
  echo "[rc=$?]"
} > "$OUT/bulk-vertices-stdin.log" 2>&1
tail -n1 "$OUT/bulk-vertices-stdin.log" | sed 's/^/    /'

# -- batch delete ----------------------------------------------------
run vertex-delete-batch "${META[@]}" delete vertex bulk-v1 bulk-v2 bulk-v3 bulk-v4 bulk-v5
run edge-delete-batch   "${META[@]}" delete edge alice bob alice carol bob dave carol dave

# -- TTL expiry observation ------------------------------------------
echo "==> waiting 4s for 'ephem' (2s TTL) to expire ..."
sleep 4
run vertex-get-ephem-expired "${META[@]}" get vertex ephem

# -- gzip compression ------------------------------------------------
run vertex-put-gzip    "${META[@]}" --compression gzip put vertex gz hello 300
run vertex-get-gzip    "${META[@]}" --compression gzip get vertex gz

echo "DONE. Logs in $OUT/"
