#!/bin/sh
# Container entrypoint for lantern-admin.
#
# Generates the optional Prometheus reverse-proxy snippet imported by the
# Caddyfile, then hands off to the image's command (caddy). The proxy is
# OPT-IN: it is only wired up when LANTERN_ADMIN_PROMETHEUS_UPSTREAM is set
# (e.g. "http://prometheus:9090"). When unset, an EMPTY snippet is written
# so the Caddyfile's glob `import` resolves to a no-op and the SPA host
# behaves exactly as before.
#
# Proxying Prometheus same-origin (under /api/prom) is how the admin
# Metrics page avoids a cross-origin browser call to Prometheus, which has
# no CORS headers of its own.
#
# Writes are best-effort: the Caddyfile uses a glob import, so if the
# snippet cannot be written (e.g. read-only filesystem with no writable
# conf.d mount) the container still serves the SPA — only the Prometheus
# proxy is skipped. We log loudly in that case rather than crash-looping.
set -eu

CONF_DIR=/etc/caddy/conf.d
PROM_CONF="$CONF_DIR/prom.caddy"

mkdir -p "$CONF_DIR" 2>/dev/null || true

if [ -n "${LANTERN_ADMIN_PROMETHEUS_UPSTREAM:-}" ]; then
	SNIPPET="handle_path /api/prom/* {
	reverse_proxy ${LANTERN_ADMIN_PROMETHEUS_UPSTREAM}
}"
	if printf '%s\n' "$SNIPPET" >"$PROM_CONF" 2>/dev/null; then
		echo "lantern-admin: Prometheus proxy enabled -> ${LANTERN_ADMIN_PROMETHEUS_UPSTREAM}" >&2
	else
		echo "lantern-admin: WARNING could not write ${PROM_CONF}; Prometheus proxy NOT enabled (is the filesystem read-only?)" >&2
	fi
else
	# Empty snippet keeps the glob import a no-op. Tolerated if unwritable.
	: >"$PROM_CONF" 2>/dev/null || true
	echo "lantern-admin: Prometheus proxy disabled (set LANTERN_ADMIN_PROMETHEUS_UPSTREAM to enable)" >&2
fi

exec "$@"
