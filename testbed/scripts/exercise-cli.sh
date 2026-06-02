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
run vertex-put-string  "${META[@]}" vertex put alice 'Alice Smith' --ttl 5m
run vertex-put-int     "${META[@]}" vertex put count 42 --ttl 5m
run vertex-put-float   "${META[@]}" vertex put price 19.99 --ttl 5m
run vertex-put-bool    "${META[@]}" vertex put alive true --ttl 5m
run vertex-put-json    "${META[@]}" vertex put bob '{"age":30}' --value-type=json --ttl 5m
run vertex-put-dt      "${META[@]}" vertex put epoch '2025-01-01T00:00:00Z' --value-type=datetime --ttl 5m
run vertex-put-dur     "${META[@]}" vertex put cooldown 30s --value-type=duration --ttl 5m
run vertex-put-zip     "${META[@]}" vertex put zip 01234 --value-type=string --ttl 5m
run vertex-put-shortlived "${META[@]}" vertex put ephem hi --ttl 2s

run vertex-get-string  "${META[@]}" vertex get alice
run vertex-get-int     "${META[@]}" vertex get count
run vertex-get-json    "${META[@]}" vertex get bob
run vertex-get-missing "${META[@]}" vertex get does-not-exist

# -- edge add/put/get/delete (single) --------------------------------
run edge-add-1         "${META[@]}" edge add alice bob 1.5 --ttl 5m
run edge-add-2         "${META[@]}" edge add alice bob 0.5 --ttl 5m
run edge-get-additive  "${META[@]}" edge get alice bob
run edge-put-replace   "${META[@]}" edge put alice bob 0.25 --ttl 5m
run edge-get-after-put "${META[@]}" edge get alice bob
run edge-get-missing   "${META[@]}" edge get alice nobody

# -- illuminate every optimization mode ------------------------------
run edge-add-carol      "${META[@]}" edge add alice carol 0.8 --ttl 5m
run edge-add-dave       "${META[@]}" edge add bob dave 0.6 --ttl 5m
run edge-add-carol-dave "${META[@]}" edge add carol dave 0.4 --ttl 5m

for mode in none mst max-st spt inverse-spt; do
  run "illuminate-$mode" "${META[@]}" illuminate alice --step 3 --k 10 --optimize "$mode"
done
run illuminate-tfidf    "${META[@]}" illuminate alice --step 2 --k 5 --tfidf

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
run vertex-delete-batch "${META[@]}" vertex delete bulk-v1 bulk-v2 bulk-v3 bulk-v4 bulk-v5
run edge-delete-batch   "${META[@]}" edge delete alice:bob alice:carol bob:dave carol:dave

# -- TTL expiry observation ------------------------------------------
echo "==> waiting 4s for 'ephem' (2s TTL) to expire ..."
sleep 4
run vertex-get-ephem-expired "${META[@]}" vertex get ephem

# -- gzip compression ------------------------------------------------
run vertex-put-gzip    "${META[@]}" --compression gzip vertex put gz hello --ttl 5m
run vertex-get-gzip    "${META[@]}" --compression gzip vertex get gz

echo "DONE. Logs in $OUT/"
