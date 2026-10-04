#!/usr/bin/env bash
# Dart CI compatibility wrapper around the shared native conformance supervisor.
set -euo pipefail
script_directory="$(cd "$(dirname "$0")" && pwd)"
case "${1:-}" in
  start)
    exec bash "$script_directory/native_receipt_fixture.sh" start "${2:?Server binary required}" \
      "${6:-${2}-authfixture}" "${3:?state directory required}" "${4:?public port required}"
    ;;
  stop) exec bash "$script_directory/native_receipt_fixture.sh" stop "${2:?state directory required}" ;;
  *) echo 'usage: dart_receipt_fixture.sh start <Server> <state> <port> <unused-metrics-port> [authfixture] | stop <state>' >&2; exit 2 ;;
esac
