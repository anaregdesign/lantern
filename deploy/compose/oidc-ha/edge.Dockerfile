# Build from the repository root. Curl supplies verified, bounded TLS health
# checks; BusyBox wget in the stock image cannot select this private CA.
FROM caddy:2.10-alpine
RUN apk add --no-cache curl
COPY --chmod=0555 deploy/compose/oidc-ha/edge-health.sh /usr/local/bin/lantern-edge-health
