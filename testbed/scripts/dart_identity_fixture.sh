#!/usr/bin/env bash
# Start/stop the host-only h2c identity CDC fixtures from one built server.
set -euo pipefail

action="${1:-}"
directory="${3:-${2:-}}"
case "$action" in
  start)
    binary="${2:?server binary required}"
    directory="${3:?state directory required}"
    mkdir -p "$directory"
    ;;
  stop)
    directory="${2:?state directory required}"
    for name in gap a b c; do
      pid_file="$directory/lantern-identity-$name.pid"
      if [[ -f "$pid_file" ]]; then
        kill "$(cat "$pid_file")" 2>/dev/null || true
        rm -f "$pid_file"
      fi
    done
    exit 0
    ;;
  *)
    echo "usage: $0 start <server-binary> <state-dir> | stop <state-dir>" >&2
    exit 2
    ;;
esac

base="${IDENTITY_FIXTURE_BASE_PORT:-6400}"
gap=$((base - 1))
port_a=$base
port_b=$((base + 1))
port_c=$((base + 2))

start() {
  local name="$1" port="$2" metrics="$3" node="$4" peers="$5" capacity="$6"
  env \
    LANTERN_PORT="$port" \
    LANTERN_METRICS_ADDR=":$metrics" \
    LANTERN_LOG_LEVEL=warn \
    LANTERN_NODE_ID="$node" \
    LANTERN_PEERS="$peers" \
    LANTERN_MUTATION_LOG_CAPACITY="$capacity" \
    LANTERN_PUMP_BACKOFF_MIN_MS=50 \
    LANTERN_PUMP_BACKOFF_MAX_MS=500 \
    "$binary" > "$directory/lantern-identity-$name.log" 2>&1 &
  echo $! > "$directory/lantern-identity-$name.pid"
}

start gap "$gap" $((base + 100)) 000000000000000000000000000000d0 '' 2
start a "$port_a" $((base + 101)) 000000000000000000000000000000a1 "127.0.0.1:$port_b,127.0.0.1:$port_c" 10000
start b "$port_b" $((base + 102)) 000000000000000000000000000000b2 "127.0.0.1:$port_a,127.0.0.1:$port_c" 10000
start c "$port_c" $((base + 103)) 000000000000000000000000000000c3 "127.0.0.1:$port_a,127.0.0.1:$port_b" 10000

for port in "$gap" "$port_a" "$port_b" "$port_c"; do
  ready=false
  for _ in $(seq 1 30); do
    if curl --fail --silent --show-error --max-time 2 \
      -H 'Content-Type: application/json' \
      -H 'Connect-Protocol-Version: 1' \
      --data '{}' \
      "http://127.0.0.1:$port/grpc.health.v1.Health/Check" \
      2>/dev/null | grep -q 'SERVING'; then
      ready=true
      break
    fi
    sleep 1
  done
  if [[ "$ready" != true ]]; then
    for name in gap a b c; do
      cat "$directory/lantern-identity-$name.log" >&2 || true
    done
    "$0" stop "$directory"
    echo "identity fixture on port $port failed health check" >&2
    exit 1
  fi
done
