#!/usr/bin/env bash
# Only unreaped direct children registered by the calling harness belong here.
# Signal every child first; bound shutdown before reaping each exact PID.
stop_owned_processes() {
  local owned_pid round any_live
  for owned_pid in "$@"; do
    [[ -z "$owned_pid" ]] && continue
    [[ "$owned_pid" =~ ^[1-9][0-9]*$ ]] || return 1
    kill -TERM "$owned_pid" 2>/dev/null || true
  done
  for round in {1..30}; do
    any_live=0
    for owned_pid in "$@"; do
      [[ -z "$owned_pid" ]] && continue
      if kill -0 "$owned_pid" 2>/dev/null; then
        any_live=1
      fi
    done
    (( any_live == 0 )) && break
    sleep 0.1
  done
  for owned_pid in "$@"; do
    [[ -z "$owned_pid" ]] && continue
    kill -KILL "$owned_pid" 2>/dev/null || true
    wait "$owned_pid" 2>/dev/null || true
  done
}
