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

Each Linux HA Server, including public OFF, requires its own declared read-only
`<node>/ntp.conf`, mounted at `/etc/ntp.conf`. The existing admitted profile uses
exactly `server time.asia.apple.com`; arbitrary NTP sources are not supported.
This is application input, not an instruction to alter host time services. The
Server requires fresh qualification before private membership/listeners and
refuses missing/invalid input. OFF owns one clock; OIDC borrows its existing
authority clock. Standalone OFF has no time-source prerequisite.


For graph-only HA, in either public mode, add the dedicated staging overlay:

```sh
export LANTERN_HA_CONFIG_DIR=/absolute/operator/lantern
export LANTERN_HA_ADMIN_UPSTREAM=https://lantern-0:6380
docker compose -f docker-compose.yml -f docker-compose.ha.yml \
  -f docker-compose.graph-snapshot.yml config
docker compose -f docker-compose.yml -f docker-compose.ha.yml \
  -f docker-compose.graph-snapshot.yml up -d
```

Each graph-only node's env file must explicitly set
`LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR=/var/lib/lantern-snapshot-spool`,
`LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES` and
`LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES`. Choose positive byte limits for
the workload; the frame limit cannot exceed the total allowance. The overlay
sets no capacity defaults. The total covers the complete frame spool, identity
indexes, merge files and charged metadata; it is a logical allowance, not a
filesystem reservation or whole-process memory limit. See the
[staging contract](../../docs/ha-runbook.md#92-subscribe-ring-buffer-overflow).

`docker-compose.graph-snapshot.yml` gives each node its own new project-scoped
named volume. A matching image prepares the dedicated target with its default
`lantern` owner and mode 0700; a fresh empty volume copies up that metadata.
Do not share volumes between nodes or substitute an existing state, backup or
runtime volume. A custom runtime UID needs explicitly prepared storage instead
of the image's default owner. Wrong ownership, unsafe modes, unknown contents
or unavailable disk capacity fail closed. Keep these volumes through container
restart; the stable owner file remains between attempts. `down -v` also removes
these staging volumes. They are transient working storage and provide no graph
or receipt continuity.

Receipt-WAL HA omits the graph staging overlay and all three graph spool keys;
its existing state and collection configuration remains separate. The two-file
`docker-compose.yml` plus `docker-compose.ha.yml` stack continues to apply to
that mode.

Env files reference certificate/CA/manifest files under `/run/lantern-config`
and writable sys:/membership state under `/state`. Choose `fresh` only for a
new domain/enrollment, then `restart`/`resume` for existing durable state.
Renew the signed membership manifest atomically before its expiry (at most
ten minutes), including OFF HA. Lost renewal fails closed. Public credentials
never authorize a private peer. The private port has no host publication.

For the custom-UID, read-only-root membership profile, prepare new dedicated
`${LANTERN_MEMBERSHIP_ROOT}/node-0/snapshot-spool` through
`node-2/snapshot-spool` directories, each mode 0700 and owned by the configured
`LANTERN_SERVICE_UID:LANTERN_SERVICE_GID`. Set the same container spool path and
explicit byte limits in the graph-only per-node env files. Apply the bind
overlay **last**, after the membership overlay's replacement mount lists:

```sh
docker compose -f docker-compose.yml -f docker-compose.ha.yml \
  -f docker-compose.membership.yml \
  -f docker-compose.graph-snapshot.membership.yml config
```

Use this bind overlay instead of `docker-compose.graph-snapshot.yml`.
`create_host_path: false` refuses missing host directories; no privileged
production initializer changes their owner or contents. The staging bind is
writable while the workload root and private inputs retain their existing
protections. Receipt-mode membership omits both graph staging overlays.

OIDC uses independently provisioned fixed-membership quorum authority. Configure
each node's original `LANTERN_SECURITY_CONFIG_FILE` and explicit fresh/resume
mode through the [env contract](../../docs/env.md). Original provisioning binds
Issuer/administrator subjects, machine Roles, the full authority profile,
per-node custody and native time. Mount only that workload's private material.
Pin `/auth/*`, `/browser/*` and control operations to a configured eligible
origin and preserve per-attempt login affinity. The HA Admin
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
