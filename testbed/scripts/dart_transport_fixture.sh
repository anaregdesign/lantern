#!/usr/bin/env bash
# Local conformance only: supervise native OIDC with literal probe Roles.
set -euo pipefail
case "${1:-}" in
  start)
    binary="${2:?Server binary required}"
    fixture_binary="${3:?authfixture binary required}"
    directory="${4:?new private state directory required}"
    port="${5:?public port required}"
    umask 077
    mkdir -m 700 "$directory"
    "$fixture_binary" -directory "$directory/trust" -public-ports "$port" \
      -transport-probe -serve "$binary" \
      </dev/null > "$directory/metadata.json" 2> "$directory/supervisor.log" &
    echo $! > "$directory/supervisor.pid"
    trap 'bash "$0" stop "$directory"' ERR
    for _ in $(seq 1 75); do
      if [[ -s "$directory/metadata.json" ]]; then
        python3 - "$directory" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]); m=json.loads((p/'metadata.json').read_text())
token=json.loads(pathlib.Path(m['token_file']).read_text())[0]
(p/'token').write_text(token); (p/'token').chmod(0o600)
PY
        trap - ERR
        exit 0
      fi
      if ! kill -0 "$(cat "$directory/supervisor.pid")" 2>/dev/null; then break; fi
      sleep 1
    done
    echo 'native transport fixture failed certified readiness; inspect private diagnostics' >&2
    bash "$0" stop "$directory"
    exit 1
    ;;
  stop)
    directory="${2:?state directory required}"
    if [[ -f "$directory/supervisor.pid" ]]; then
      supervisor_pid="$(cat "$directory/supervisor.pid")"
      kill -INT "$supervisor_pid" 2>/dev/null || true
      for _ in $(seq 1 15); do
        kill -0 "$supervisor_pid" 2>/dev/null || break
        sleep 1
      done
      if kill -0 "$supervisor_pid" 2>/dev/null; then
        echo 'native transport fixture did not finish owned teardown' >&2
        exit 1
      fi
      rm -f "$directory/supervisor.pid"
    fi
    rm -f "$directory/token"
    ;;
  *) echo 'usage: dart_transport_fixture.sh start <Server> <authfixture> <state> <port> | stop <state>' >&2; exit 2 ;;
esac
