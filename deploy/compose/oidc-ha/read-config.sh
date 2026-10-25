#!/bin/sh
# Input adapter only: supplied HUMAN JSON is mandatory on every invocation.
# This never searches identity material or state for configuration.
set -eu
input=/input/startup.json
[ -f "$input" ] && [ -s "$input" ] && [ ! -L "$input" ]
[ "$(wc -c < "$input")" -le 122880 ]
LANTERN_SECURITY_CONFIG_JSON="$(cat "$input")"
export LANTERN_SECURITY_CONFIG_JSON
exec /app/lantern "$@"
