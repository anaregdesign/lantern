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
    # Keep independent offsets for the supervisor and the failing caller.
    # The Server never gets the report path or either descriptor.
    exec 3>"$directory/failure-report" 4<"$directory/failure-report"
    rm "$directory/failure-report"
    "$fixture_binary" -directory "$directory/trust" -public-ports "$public_ports" \
      -peer-ports "$peer_ports" -receipt -serve "$binary" -failure-report-fd3 \
      </dev/null > "$directory/metadata.json" 2> "$directory/supervisor.log" 4<&- &
    echo $! > "$directory/supervisor.pid"
    exec 3>&-
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
    # Only the supervisor's typed writer owns this channel. Publish complete
    # records within the limit or a fixed diagnostic when capture is incomplete.
    python3 -c '
import json, sys
raw = sys.stdin.buffer.read(16385)
lines = raw.splitlines()
stages = (b"configuration", b"generation", b"spawn", b"readiness", b"publication", b"exit", b"private_input")
valid = (len(raw) <= 16384 and raw.endswith(b"\n") and len(lines) in (1, 2)
         and lines[0] in {b"authfixture_failure:" + stage for stage in stages})
if valid and len(lines) == 2:
    prefix = b"authfixture_readiness:"
    try:
        valid = lines[1].startswith(prefix) and isinstance(json.loads(lines[1][len(prefix):]), dict)
    except (ValueError, RecursionError, UnicodeError):
        valid = False
sys.stderr.buffer.write(raw if valid else b"authfixture_diagnostics:unavailable\n")
' <&4 4<&-
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
