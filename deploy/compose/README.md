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

[`docker-compose.oidc.yml`](docker-compose.oidc.yml) runs one fixed OIDC node
after explicit provisioning. Unmodified defaults point at a nonexistent
`FAKE_PRIVATE_PROVISIONING_DIRECTORY` and cannot initialize or start. The
[setup template](../examples/auth/security-config.json.example) contains only
user intent and deliberately invalid provider/public TLS values; it has no
hand-authored authority, keys, profile digests or capacity knobs.

Follow the [provisioning guide](../examples/auth/README.md#environment-json-startup)
to privately select the exact administrator issuer/subject, provider profiles,
HTTPS public origin and TLS/proxy boundary. For this example use `target_platform`
`linux`, `persistence_root` `/state`, and one node named `node-1` with control
origin `https://lantern-0:6381`, data origin `https://lantern-0:6382` and matching
listen ports. Both private ports remain unpublished. The public certificate
must cover the actual HTTPS hostname; clients verify its chain normally.

From the repository root, inject the small setup JSON through an approved secret
manager, choose new private output directories outside the checkout, and run:

```sh
# These are illustrative absolute paths, not files supplied by this repository.
export LANTERN_PROVISIONING_DIRECTORY=/absolute/private/lantern-provisioned
export LANTERN_OPERATOR_DIRECTORY=/absolute/private/lantern-operator
export LANTERN_AUTH_MODE=oidc
# LANTERN_SECURITY_CONFIG_JSON contains the privately completed setup intent.
go run ./server/cmd provision-security --output "$LANTERN_PROVISIONING_DIRECTORY" \
  --operator-directory "$LANTERN_OPERATOR_DIRECTORY"
```

Generation may run on a Mac while explicitly targeting Linux. It creates no
listeners, native journals or trusted time. Preserve the generated absolute
paths: Compose mounts only this node directory at the same target path. Ensure
the container's dedicated runtime UID owns or can privately read the generated
files without making them public. Do not mount another node's private keys or
the separate operator signing directory into Server.

Run the external publisher on the provisioning host, using its generated
paths, before the short initial manifests expire:

```sh
go run ./server/cmd/membershippublisher --config "$LANTERN_OPERATOR_DIRECTORY/publisher.json" \
  --state "$LANTERN_OPERATOR_DIRECTORY/state.json" --mode init
```

Keep it running for both control and data renewal, including this standalone
node. After an orderly publisher stop, resume the same journal with `--mode resume`.
The default operator window is finite (30 days). Before the workload leaf/window
boundary, `lantern renew-security --operator-directory ... --output ...` prepares
same-identity certificate and next-window candidates in a new directory. Follow
the [reviewed stop/install/advance/resume sequence](../../docs/live-membership-renewal.md#renew-generated-workload-certificates)
with CLEAN node shutdown, unchanged original paths/state and retained publisher
journal. This command does not publish live files or hot-reload certificates.
The fixed ten-year root remains a hard capacity boundary. Server receives signed
renewable inputs and its own workload identity, never operator/CA signing keys.

In another shell with `LANTERN_PROVISIONING_DIRECTORY` set to the same absolute
path:

```sh
cd deploy/compose
docker compose -f docker-compose.oidc.yml build
# Once, on the actual Linux target with a new state volume:
docker compose -f docker-compose.oidc.yml --profile init run --rm --no-deps init-security
# Every normal startup resumes existing state:
docker compose -f docker-compose.oidc.yml up -d lantern-0
docker compose -f docker-compose.oidc.yml ps
```

Compose supplies only a `provisioned_file` reference to initialization/startup;
it does not forward setup JSON or regenerate trust. It mounts this node's
provisioning directory read-only, including the renewable membership directories,
and retains mutable state in `lantern-security-state`. Atomic replacement by the
external publisher remains visible. The nonsecret `/etc/ntp.conf` config supplies
the admitted native source without changing host time services. Native time and
current quorum remain readiness requirements.

`init-security` refuses retained/interrupted state. Normal startup never repairs
missing state or resets Roles, revocations, sessions or later managed Issuers.
Keep the volume across orderly stops/recreations. `docker compose ... down`
without volume removal stops this example; confirm successful orderly shutdown.
Deleting the security volume is data destruction, not restart or recovery.
The graph stays in-memory in this small example; security persistence is separate.

The browser callback is derived from `public_origin` and canonical Issuer.
Register the exact URL with the IdP. A browser-only Google profile needs no
API audience; external API JWTs stay disabled without an explicitly configured
API profile. Initial administrator identity is the verified issuer/subject pair,
not email, and grants management rather than implicit data access.

This example does not publish Admin. A protected SPA needs the existing trusted
same-origin HTTPS frontend and fixed `/auth/*`, `/browser/*` and RPC routes,
verified upstream TLS or exact trusted-proxy boundary, and login-attempt affinity.
The OFF example's separate HTTP Admin is not this topology. See
[OIDC operations](../../docs/oidc-operations.md#browser-and-diagnostics-boundary).

The advanced HA examples below use deliberate externally provisioned file-backed
inputs. They are separate from generated setup and its private node reference.

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
