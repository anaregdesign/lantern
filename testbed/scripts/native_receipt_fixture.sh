#!/usr/bin/env bash
# Local CI only: supervise the production Server with native Roles and TLS.
set -euo pipefail
case "${1:-}" in
  start)
    binary="${2:?Server binary required}"
    fixture_binary="${3:?authfixture binary required}"
    directory="${4:?new private state directory required}"
    public_ports="${5:?public ports required}"
    peer_ports="${6:-}"
    umask 077
    mkdir -m 700 "$directory"
    "$fixture_binary" -directory "$directory/trust" -public-ports "$public_ports" \
      -peer-ports "$peer_ports" -receipt -serve "$binary" \
      </dev/null > "$directory/metadata.json" 2> "$directory/supervisor.log" &
    echo $! > "$directory/supervisor.pid"
    for _ in $(seq 1 75); do
      if [[ -s "$directory/metadata.json" ]]; then
        python3 - "$directory" <<'PY'
import json,pathlib,sys
p=pathlib.Path(sys.argv[1]); m=json.loads((p/'metadata.json').read_text())
tokens=json.loads(pathlib.Path(m['token_file']).read_text())
(p/'token').write_text(tokens[0]); (p/'token').chmod(0o600)
PY
        exit 0
      fi
      if ! kill -0 "$(cat "$directory/supervisor.pid")" 2>/dev/null; then break; fi
      sleep 1
    done
    echo 'native receipt fixture failed certified readiness; inspect private diagnostics' >&2
    "$0" stop "$directory"
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
      rm -f "$directory/supervisor.pid"
    fi
    rm -f "$directory/token"
    ;;
  *) echo 'usage: native_receipt_fixture.sh start <Server> <authfixture> <state> <public-ports> [peer-ports] | stop <state>' >&2; exit 2 ;;
esac
