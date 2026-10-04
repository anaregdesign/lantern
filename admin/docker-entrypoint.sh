#!/bin/sh
# Fixed same-origin Server/BFF routes and Role-checked Prometheus proxy.
set -eu
umask 077
CONF_DIR=${LANTERN_ADMIN_CONFIG_DIR:-/etc/caddy/conf.d}
case "$CONF_DIR" in /*) ;; *) echo 'lantern-admin: configuration directory must be absolute' >&2; exit 1 ;; esac
case "$CONF_DIR" in *[!a-zA-Z0-9_./-]*) echo 'lantern-admin: invalid configuration directory' >&2; exit 1 ;; esac
validate_origin() {
  # No credentials, paths, query, placeholders, whitespace or Caddy syntax.
  printf '%s\n' "$1" | LC_ALL=C grep -Eq '^(http|https|h2c)://[a-zA-Z0-9][a-zA-Z0-9.-]*:[0-9]{1,5}$' || {
    echo 'lantern-admin: upstream must be a fixed scheme://hostname:port origin' >&2; exit 1;
  }
  port=${1##*:}
  [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || { echo 'lantern-admin: invalid upstream port' >&2; exit 1; }
}
SERVER=${LANTERN_ADMIN_SERVER_UPSTREAM:-}
PROM=${LANTERN_ADMIN_PROMETHEUS_UPSTREAM:-}
CA=${LANTERN_ADMIN_SERVER_CA_FILE:-}
[ -z "$SERVER" ] || validate_origin "$SERVER"
[ -z "$PROM" ] || validate_origin "$PROM"
if [ -n "$PROM" ] && [ -z "$SERVER" ]; then
  echo 'lantern-admin: Prometheus requires the Server operations authorization route' >&2; exit 1
fi
if [ -n "$CA" ]; then
  case "$CA" in /*) ;; *) echo 'lantern-admin: CA file must be absolute' >&2; exit 1 ;; esac
  case "$CA" in *[!a-zA-Z0-9_./-]*) echo 'lantern-admin: invalid CA path' >&2; exit 1 ;; esac
  [ -r "$CA" ] || { echo 'lantern-admin: CA file is unreadable' >&2; exit 1; }
  case "$SERVER" in https://*) ;; *) echo 'lantern-admin: a Server CA requires HTTPS' >&2; exit 1 ;; esac
fi
mkdir -p "$CONF_DIR"
CONF="$CONF_DIR/routes.caddy"
TMP="$CONF_DIR/.routes.caddy.$$"
trap 'rm -f "$TMP"' EXIT HUP INT TERM
# Retire the previous unauthenticated metrics snippet, including reused volumes.
rm -f "$CONF_DIR/prom.caddy"
transport() {
  if [ -n "$CA" ]; then
    printf '\t\ttransport http {\n\t\t\ttls_trust_pool file %s\n\t\t}\n' "$CA"
  fi
}
{
  if [ -n "$SERVER" ]; then
    printf 'handle /auth/* {\n\treverse_proxy %s {\n\t\theader_up Host {hostport}\n\t\theader_up -Authorization\n' "$SERVER"
    transport
    printf '\t}\n}\n'
    printf 'handle /browser/* {\n\treverse_proxy %s {\n\t\theader_up Host {hostport}\n\t\theader_up -Authorization\n\t\tflush_interval -1\n' "$SERVER"
    transport
    printf '\t}\n}\n'
    printf 'handle /graph.v1.*/* {\n\treverse_proxy %s {\n\t\theader_up Host {hostport}\n\t\tflush_interval -1\n' "$SERVER"
    transport
    printf '\t}\n}\n'
  else
    printf 'handle /auth/* /browser/* /graph.v1.*/* {\n\trespond "Server upstream unavailable" 503\n}\n'
  fi
  if [ -n "$PROM" ]; then
    printf 'handle_path /api/prom/* {\n\troute {\n\t\t@write not method GET\n\t\trespond @write "read-only diagnostics" 405\n\t\tforward_auth %s {\n\t\t\turi /auth/operations?\n\t\t\theader_up Host {hostport}\n\t\t\theader_up -Authorization\n' "$SERVER"
    transport
    printf '\t\t}\n\t\treverse_proxy %s {\n\t\t\theader_up -Cookie\n\t\t\theader_up -Authorization\n\t\t}\n\t}\n}\n' "$PROM"
  else
    printf 'handle /api/prom/* {\n\trespond "diagnostics upstream unavailable" 503\n}\n'
  fi
} > "$TMP"
mv "$TMP" "$CONF"
exec "$@"
