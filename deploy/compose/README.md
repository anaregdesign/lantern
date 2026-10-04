# Lantern Compose profiles

The default starts one OFF Server and Admin on loopback ports 6380 and 8080.
No authentication environment variables, IdP, peer credentials or external
storage service are needed. Server capabilities report OFF explicitly; security
management remains unavailable. The image tags must include the same source
and protocol revision as these configuration files.

```sh
cd deploy/compose
docker compose up -d
```

Each Server owns its graph backup volume. Graph-only snapshots restore a
baseline, not authentication state or receipt continuity. `down -v` removes
those volumes. The optional `tools` and `diagnostics` profiles are local OFF
examples. Prometheus is reachable on loopback 9091; enabling its Admin proxy
also requires the fixed Server upstream. See [Admin](../../admin/README.md).

## Explicit HA

Use `docker-compose.ha.yml` with operator-provisioned per-node env files and
read-only material in `LANTERN_HA_CONFIG_DIR`. All three members need distinct
workload certificates, exact signed HTTPS private origins on port 6381 and
homogeneous public mode/domain/namespace. The operator signing private key
stays outside every Server volume. DNS only resolves those approved origins.
Never share a peer private key across nodes.

```sh
export LANTERN_HA_CONFIG_DIR=/absolute/operator/lantern
export LANTERN_HA_ADMIN_UPSTREAM=https://lantern-0:6380
docker compose -f docker-compose.yml -f docker-compose.ha.yml config
docker compose -f docker-compose.yml -f docker-compose.ha.yml up -d
```

Env files reference certificate/CA/manifest files under `/run/lantern-config`
and writable sys:/membership state under `/state`. Choose `fresh` only for a
new domain/enrollment, then `restart`/`resume` for existing durable state.
Renew the signed membership manifest atomically before its expiry (at most
ten minutes), including OFF HA. Lost renewal fails closed. Public credentials
never authorize a private peer. The private port has no host publication.

OIDC uses one fixed security writer and leased replicas. Configure the complete
[env contract](../../docs/env.md), including required administrator Issuer and
subjects, generation, writer trust, browser public HTTPS origin and callback.
Pin `/auth/*`, `/browser/*` and control operations to that writer. The HA Admin
HTTP service is private and requires an operator HTTPS frontend; the Server
upstream uses verified TLS and `admin-trust/ca.pem`. Public listener certificates
must cover the configured public origins; peer certificates are separate.

Diagnostics remain on each Server's loopback. An operator-authenticated local
scraper/sidecar is required for protected production collection. The optional
OFF Prometheus/MCP profiles are not a protected HA deployment recipe. Machine
clients need explicit Role assignments and current credentials.

See the [HA runbook](../../docs/ha-runbook.md), [replication RFC](../../docs/replication.md),
and [OIDC operations](../../docs/oidc-operations.md) for fencing and recovery.
The `docker-compose.backup.yml` and `docker-compose.mcp.yml` files remain
standalone local OFF examples.
