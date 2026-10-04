#!/usr/bin/env bash
# Host-only public CDC fixtures with an independent native private peer plane.
set -euo pipefail
umask 077
case "${1:-}" in
  stop)
    directory="${2:?state directory required}"
    for name in gap cluster; do
      file="$directory/$name.pid"
      if [[ -f "$file" ]]; then
        pid="$(cat "$file")"
        kill "$pid" 2>/dev/null || true
        for _ in $(seq 1 300); do
          kill -0 "$pid" 2>/dev/null || break
          sleep 0.1
        done
        if kill -0 "$pid" 2>/dev/null; then
          echo "owned identity supervisor failed to stop; private diagnostics: $directory" >&2
          exit 1
        fi
        rm -f "$file"
      fi
    done
    exit 0 ;;
  start)
    binary="${2:?Server binary required}"
    directory="${3:?new private state directory required}"
    helper="${4:-${binary}-authfixture}"
    [[ -x "$binary" && -x "$helper" && ! -e "$directory" ]] || exit 2
    mkdir -m 700 "$directory"
    ;;
  *) echo 'usage: dart_identity_fixture.sh start <Server> <new-state> [authfixture] | stop <state>' >&2; exit 2 ;;
esac
base="${IDENTITY_FIXTURE_BASE_PORT:-6400}"
peer="${IDENTITY_FIXTURE_PEER_BASE_PORT:-$((base + 200))}"
printf '%s\n' '[{"LANTERN_MUTATION_LOG_CAPACITY":"2"}]' > "$directory/gap-overrides.json"
printf '%s\n' '[{"LANTERN_MUTATION_LOG_CAPACITY":"10000"},{"LANTERN_MUTATION_LOG_CAPACITY":"10000"},{"LANTERN_MUTATION_LOG_CAPACITY":"10000"}]' > "$directory/cluster-overrides.json"
trap 'bash "$0" stop "$directory"' ERR
"$helper" -directory "$directory/gap" -mode off -public-ports "$((base - 1))"   -serve "$binary" -overrides-file "$directory/gap-overrides.json"   </dev/null > "$directory/gap.json" 2> "$directory/gap-supervisor.log" &
printf '%s\n' "$!" > "$directory/gap.pid"
"$helper" -directory "$directory/cluster" -mode off   -public-ports "$base,$((base + 1)),$((base + 2))"   -peer-ports "$peer,$((peer + 1)),$((peer + 2))"   -serve "$binary" -overrides-file "$directory/cluster-overrides.json"   </dev/null > "$directory/cluster.json" 2> "$directory/cluster-supervisor.log" &
printf '%s\n' "$!" > "$directory/cluster.pid"
for name in gap cluster; do
  ready=false
  for _ in $(seq 1 120); do
    kill -0 "$(cat "$directory/$name.pid")" 2>/dev/null || break
    if [[ -s "$directory/$name.json" ]]; then ready=true; break; fi
    sleep 0.5
  done
  if [[ "$ready" != true ]]; then
    echo "identity fixture failed certified readiness; private diagnostics: $directory" >&2
    bash "$0" stop "$directory"
    exit 1
  fi
done
trap - ERR
