#!/bin/sh
# Fixed local HTTPS probe: explicit trust, no proxy/redirect/config overrides.
set -eu
status="$(/usr/bin/curl --disable --proto '=https' --noproxy '*' \
    --cacert /edge-tls/ca.pem --max-time 2 --connect-timeout 2 \
    --max-redirs 0 --fail --silent --show-error \
    --output /dev/null --write-out '%{http_code}' \
    https://localhost:8443/healthz)" || exit 1
[ "$status" = 200 ]
