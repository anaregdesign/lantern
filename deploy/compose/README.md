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

For fixed three-node OIDC with Admin, Prometheus, MCP, verified HTTPS and a
separate renewable membership publisher, use the
[authenticated HA example](oidc-ha/README.md). Its explicit native/application
initialization and retained restart sequence are separate from the small
standalone examples below.

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

[`docker-compose.oidc.yml`](docker-compose.oidc.yml) runs one fixed OIDC node.
The same complete human `LANTERN_SECURITY_CONFIG_JSON` is forwarded to
initialization and every normal start. No generated configuration replaces it.
Unmodified defaults lack valid JSON/material and refuse startup.

Complete the [one configuration](../examples/auth/README.md#environment-json-startup)
privately: exact public origin, explicit administrator issuer/subject, current
provider profiles and secrets, and public TLS/proxy boundary. For this example
select `target_platform` `linux`, `persistence_root` `/state`, and one named
`node-1` entry with control origin `https://lantern-0:6381`, data origin
`https://lantern-0:6382` and matching listen ports. Set `material_root` to a new
private absolute host directory. Both private ports remain unpublished.

With the same JSON injected by the approved secret manager, provision once from
the repository root:

```sh
export LANTERN_AUTH_MODE=oidc
# This mount selector must equal material_root in the human JSON.
export LANTERN_MATERIAL_DIRECTORY=/absolute/private/lantern-material
export LANTERN_OPERATOR_DIRECTORY=/absolute/private/lantern-operator
go run ./server/cmd provision-security --operator-directory "$LANTERN_OPERATOR_DIRECTORY"
```

Generation can run on a Mac while explicitly targeting Linux; it opens no
listeners, native state or trusted time. It writes independent identity keys and
fixed historical trust, never a saved setup/current-provider configuration.
Preserve the generated absolute paths: Compose binds only
`material_root/nodes/node-1` at that same target read-only. Ensure private
ownership is valid for the dedicated runtime UID. Operator/CA signing custody
and other nodes' keys stay outside the serving mount.

Before short memberships expire, run the separate publisher on the material
host using its generated paths:

```sh
go run ./server/cmd/membershippublisher --config "$LANTERN_OPERATOR_DIRECTORY/publisher.json" \
  --state "$LANTERN_OPERATOR_DIRECTORY/state.json" --mode init
```

It renews both control and data memberships even for one node. After orderly
publisher stop use `--mode resume` with the same journal. Certificate/window
maintenance uses [reviewed same-identity renewal](../../docs/live-membership-renewal.md#renew-generated-workload-certificates),
CLEAN node stop, original paths and retained state; it does not regenerate trust
or load saved provider settings.

In a shell with the same complete private JSON and mount selector supplied:

```sh
cd deploy/compose
docker compose -f docker-compose.oidc.yml build
# Once on the actual target, with new native state:
docker compose -f docker-compose.oidc.yml --profile init run --rm --no-deps init-security
# Every normal start uses the same human input and resumes intact state:
docker compose -f docker-compose.oidc.yml up -d lantern-0
docker compose -f docker-compose.oidc.yml ps
```

The state volume contains native history/custody and the read-only material
mount contains durable keys/trust plus renewable signed inputs. Neither can
replace omitted current JSON. Missing/partial/altered state or material fails
closed; ordinary startup never initializes or resets it. `initial_admin` asserts
the original identity on resume and does not restore grants or undo suspension.
Keep the state volume across orderly stops/recreations and confirm CLEAN
shutdown. The small example keeps graph data in memory; security persistence is
separate. Do not share rendered Compose output containing real JSON secrets.

To add an Issuer later, update the same human `issuers` list and restart with
that input. Native certified reconciliation must match the local desired set
before public readiness; different desired intent leaves a node unavailable.
Adding trust creates no Principals, Roles or assignments. Browser-only Google
needs no API audience; absent API configuration rejects external API JWTs.
Register callbacks from the supplied origin and canonical Issuer using the
[callback guide](../examples/auth/README.md#browser-callbacks).

The example does not publish Admin. A protected SPA needs trusted same-origin
HTTPS routes, verified upstream TLS or exact trusted proxy addresses, and
login-attempt affinity. Native time/quorum and external membership renewal
remain required. See [OIDC operations](../../docs/oidc-operations.md#browser-and-diagnostics-boundary).


## Explicit HA

Use `docker-compose.ha.yml` with private per-node env files under
`LANTERN_HA_CONFIG_DIR`. OFF files explicitly configure the independent generic
private peer plane, including `LANTERN_PEER_LISTEN_ADDR`; the overlay does not
supply a mode-specific listener. OIDC files instead supply the same complete
human JSON used on provision/init/start, with `node` equal to that workload's
fixed name. Its private planes are derived, so do not add generic OFF peer
settings to OIDC JSON startup.

For OIDC the existing mount contract is exact:

- JSON `material_root` is `/run/lantern-config/material`; `persistence_root` is
  `/state`; the node entry is `lantern-0`, `lantern-1` or `lantern-2`.
- Generate material inside an isolated Linux container at that exact material
  path, with a new writable host-backed parent at `/run/lantern-config` and a
  separate new operator mount. Supply the same human JSON and run
  `provision-security --operator-directory ...` there. Do not generate at a Mac
  host path and then silently rebase it into this container path.
- Install only `material/nodes/lantern-N/` and required private node files into
  that workload's host root `${LANTERN_HA_CONFIG_DIR}/lantern-N`. The existing
  read-only mount exposes them at the original container path. Other nodes'
  private keys are never present in that serving root.
- The external publisher must see every installed renewable node directory at
  its original generated `/run/lantern-config/material/nodes/lantern-N` path.
  Map the installed per-node leaves into its separate private namespace; merely
  publishing into an abandoned staging copy would not renew running nodes.
  Operator/CA signers and the journal stay outside all Server mounts.

Generic OFF uses its existing per-node file layout. Both modes retain distinct
workload certificates, exact signed HTTPS private origins and homogeneous
mode/domain/namespace. DNS resolves only approved origins; it grants no
membership. The data and OIDC control origins must use their separately declared
ports. Preserve every node's state and orderly CLEAN custody across restart.

Each Linux HA Server, including public OFF, requires its own declared read-only
`<node>/ntp.conf`, mounted at `/etc/ntp.conf`. The existing admitted profile uses
exactly `server time.asia.apple.com`; arbitrary NTP sources are not supported.
This is application input, not an instruction to alter host time services. The
Server requires fresh qualification before private membership/listeners and
refuses missing/invalid input. OFF owns one clock; OIDC borrows its existing
authority clock. Standalone OFF has no time-source prerequisite.


For the generic OFF graph-only HA profile, add the dedicated staging overlay:

```sh
export LANTERN_HA_CONFIG_DIR=/absolute/operator/lantern
export LANTERN_HA_ADMIN_UPSTREAM=https://lantern-0:6380
docker compose -f docker-compose.yml -f docker-compose.ha.yml \
  -f docker-compose.graph-snapshot.yml config
docker compose -f docker-compose.yml -f docker-compose.ha.yml \
  -f docker-compose.graph-snapshot.yml up -d
```

Each generic OFF graph-only node's env file must explicitly set
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

OIDC graph-only uses its retained finite profile and the derived
`persistence_root/<node>/snapshot-spool`. With `persistence_root=/state`, that
directory is inside **that node's exclusive writable `/state` volume**, separate
from its read-only identity/input mount. Native initialization creates the
private derived leaf; keep its owner, mode and native state through restart.
Do not apply either generic OFF graph-snapshot overlay to OIDC: their
`/var/lib/lantern-snapshot-spool` mount would be unused. Separate
`LANTERN_GRAPH_SNAPSHOT_SPOOL_*` variables are rejected even when empty. OIDC
receipt-WAL mode retains validated spool metadata but has no active graph spool.

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

For the generic OFF custom-UID, read-only-root membership profile, prepare new dedicated
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
protections. Receipt-mode and all OIDC membership profiles omit both graph
staging overlays; OIDC graph-only uses the private derived path described above.

OIDC uses the same human JSON for provisioning, initialization and normal
startup. Each fixed node selects its own `node` entry, reads the current desired
Issuers from JSON, and mounts only its retained identity material plus separate
native state. Material is not a configuration source. See the
[configuration guide](../examples/auth/README.md#environment-json-startup) for
fixed quorum, exact seed identity, certified reconciliation and native time.
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
