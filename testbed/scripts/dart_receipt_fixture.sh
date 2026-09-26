#!/usr/bin/env bash
# Start/stop a host-only authenticated receipt-WAL fixture for Dart CI.
set -euo pipefail

action="${1:-}"
case "$action" in
  start)
    binary="${2:?server binary required}"
    directory="${3:?state directory required}"
    port="${4:?server port required}"
    metrics="${5:?metrics port required}"
    umask 077
    mkdir -p "$directory"
    token="$(openssl rand -hex 32)"
    printf '%s' "$token" > "$directory/token"
    echo "::add-mask::$token"
    env \
      LANTERN_PORT="$port" \
      LANTERN_METRICS_ADDR=":$metrics" \
      LANTERN_LOG_LEVEL=warn \
      LANTERN_AUTH_TOKENS="$token" \
      LANTERN_NODE_ID="$(openssl rand -hex 16)" \
      LANTERN_RECEIPT_WAL_MODE=fresh \
      LANTERN_RECEIPT_WAL_PATH="$directory/receipts.wal" \
      LANTERN_RECEIPT_EPOCH="$(openssl rand -hex 16)" \
      LANTERN_RECEIPT_RETENTION=1h \
      LANTERN_RECEIPT_MAX_ENTRIES=128 \
      LANTERN_RECEIPT_MAX_BYTES=1048576 \
      LANTERN_BACKUP_ENABLED=false \
      LANTERN_BACKUP_RESTORE_ON_START=false \
      "$binary" > "$directory/server.log" 2>&1 &
    server_pid=$!
    echo "$server_pid" > "$directory/server.pid"
    for _ in $(seq 1 60); do
      if ! kill -0 "$server_pid" 2>/dev/null; then
        break
      fi
      if curl --fail --silent --max-time 2 \
        -H 'Content-Type: application/json' \
        -H 'Connect-Protocol-Version: 1' \
        --data '{}' \
        "http://127.0.0.1:$port/grpc.health.v1.Health/Check" \
        2>/dev/null | grep -q 'SERVING'; then
        exit 0
      fi
      sleep 1
    done
    echo "receipt fixture failed health check" >&2
    "$0" stop "$directory"
    exit 1
    ;;
  stop)
    directory="${2:?state directory required}"
    if [[ -f "$directory/server.pid" ]]; then
      kill "$(cat "$directory/server.pid")" 2>/dev/null || true
      rm -f "$directory/server.pid"
    fi
    rm -f "$directory/token"
    ;;
  *)
    echo "usage: $0 start <server-binary> <state-dir> <port> <metrics-port> | stop <state-dir>" >&2
    exit 2
    ;;
esac
