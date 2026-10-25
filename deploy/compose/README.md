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

## Separate OFF and OIDC examples

The standalone examples build the Server from this checkout. Use a matching
Server build: older published images do not understand the environment JSON
or `init-security` command. Run only one example at a time because both publish
the loopback Server port 6380.

### OFF

```sh
cd deploy/compose
docker compose -f docker-compose.off.yml up -d --build
docker compose -f docker-compose.off.yml ps
```

Open <http://localhost:8080> for Admin. The Server at
<http://localhost:6380> accepts anonymous data operations. No IdP, credential,
security volume or initialization is required. This small example does not
configure graph persistence; see the default profile above for graph backups.

### OIDC

[`docker-compose.oidc.yml`](docker-compose.oidc.yml) is a Server-only HTTPS
example. Its defaults contain **deliberately invalid `FAKE_*` credentials**,
`.invalid` public hostname and empty original-authority objects. It must refuse
startup unchanged. The readable counterpart is
[`security-config.json.example`](../examples/auth/security-config.json.example).
These are authoring examples, not issued certificates or a configured provider.

Prepare one complete private JSON using the
[configuration guide](../examples/auth/README.md#environment-json-startup).
Inject it into `LANTERN_SECURITY_CONFIG_JSON` through the cloud secret integration
or your approved local credential manager; Compose forwards the supplied value
to both initialization and normal startup. Do not save real values in this
checkout, command arguments, an image, logs or shared rendered Compose output.
`docker compose config` expands the entire secret value: render only the fake
example when sharing validation evidence.

Replace the inline original-authority objects with independently provisioned
node/genesis/profile/member/material inputs for this exact workload. OAuth
Client ID and Client Secret alone are insufficient. For the standalone example,
provision a one-member control authority whose private origin and certificate
match `lantern-0:6381`; this port is not published. Keep control TLS/voting/origin
keys distinct from the public TLS identity and from operator signing custody.
The public certificate must cover the actual HTTPS hostname, and clients must
trust its chain normally. No insecure TLS override is provided.

The one remaining configuration mount is a directory containing the nonsecret,
renewable signed control membership as `control.json`. Point
`LANTERN_MEMBERSHIP_DIRECTORY` at its existing absolute private directory.
The original operator signing key stays outside every Server container. Renew
the signed manifest atomically before expiry through the existing membership
procedure; static environment variables do not replace that runtime protocol.
The Compose `configs` entry supplies the admitted nonsecret native time-source
input at `/etc/ntp.conf` without changing host time services. The normal native
time source/path and current quorum must be available before readiness.

With the private JSON and renewable membership prepared:

```sh
cd deploy/compose
export LANTERN_MEMBERSHIP_DIRECTORY=/absolute/private/lantern-membership
docker compose -f docker-compose.oidc.yml build
# Once, for a new security state only:
docker compose -f docker-compose.oidc.yml --profile init run --rm --no-deps init-security
# Normal startup always reopens existing state:
docker compose -f docker-compose.oidc.yml up -d lantern-0
docker compose -f docker-compose.oidc.yml ps
```

`init-security` refuses retained or interrupted security state. Normal startup
never initializes missing state or resets existing Roles, revocations or sessions.
Keep `lantern-security-state` across normal stops and recreations. Stop with
`docker compose -f docker-compose.oidc.yml down` and check the successful orderly
shutdown; `down -v` destroys the security volume and is not a restart or recovery
procedure. Configuration and secret rotation take effect after an orderly stop
and restart with the updated injected JSON. The application graph remains
in-memory in this small example; security persistence is not graph persistence.

The browser callback is derived from `public_origin` and the exact Issuer.
Register that exact URL with the IdP; for Google's canonical Issuer the suffix is
`/auth/callback/89a8000a68d759c68bfaeab5056d67342e97643511923e63702da58a9aac8f38`.
The original administrator is the verified `(issuer, subject)` Principal with
explicit management Roles, not an email address or an implicit data grant.

This example does not publish Admin. To use the SPA with OIDC, provide the
existing trusted same-origin HTTPS frontend for Admin and the fixed
`/auth/*`, `/browser/*` and RPC routes, with verified upstream TLS or the exact
trusted-proxy boundary. Select that same public origin in Admin's Gateway
picker. The OFF example's separate HTTP Admin is not this protected topology.
See [OIDC operations](../../docs/oidc-operations.md#browser-and-diagnostics-boundary).

The advanced HA examples below retain their explicit original file-backed
provisioning interface. They are separate from these environment-JSON examples.

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
