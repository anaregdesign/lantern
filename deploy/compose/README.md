# Complete local Compose stacks

The two canonical entrypoints both include **three HA Servers, Admin, MCP,
Prometheus and three independent mTLS metrics relays**, with no optional runtime
profiles. `docker-compose.yml` is local-only OFF; `docker-compose.oidc.yml` is
OIDC with one verified HTTPS origin and the existing shared
[`docker-compose.oidc-ha.yml`](docker-compose.oidc-ha.yml) implementation.
Preparation and initialization are explicit one-time actions. Ordinary `up`
requires intact private trust/native state and never initializes or repairs it.

## Select one compatible immutable image matrix

Server/Admin/MCP images are required explicit inputs (`LANTERN_IMAGE`,
`LANTERN_ADMIN_IMAGE`, `LANTERN_MCP_IMAGE`). Use local image IDs or registry
`@sha256:` references, selected for the same exact source revision. There is no
mutable `latest` fallback and no automatic source build of the product images.
Select the exact full commit for source-built helpers. Runtime helper references
`LANTERN_METRICS_RELAY_IMAGE` (both modes), `LANTERN_PUBLISHER_IMAGE` and
`LANTERN_EDGE_IMAGE` (OIDC) must also be immutable IDs/digests. No runtime
service has a build directive or a mutable shared helper tag.

The latest historical releases checked on 2026-11-08 were
[Server v0.37.1](https://github.com/anaregdesign/lantern/releases/tag/v0.37.1),
[Admin v0.25.0](https://github.com/anaregdesign/lantern/releases/tag/admin/v0.25.0)
and [MCP v0.15.2](https://github.com/anaregdesign/lantern/releases/tag/mcp/v0.15.2).
Their observed registry index and platform digests are recorded in
[historical-image-matrix.json](historical-image-matrix.json). They predate the Allow-only resource/read-write policy and **do not qualify this
new source**. New published tags/digests and actual-pulled-image acceptance remain
pending separate release authorization and the normal publication gates. Do not
substitute the historical tags merely to satisfy required variables.

Before initialization and every selected-image change, run this read-only
preflight from the repository root and retain its output with acceptance logs:

```sh
export LANTERN_COMPOSE_SOURCE="$(git rev-parse HEAD)"
python3 deploy/compose/image_matrix.py --source "$LANTERN_COMPOSE_SOURCE" \
  --mode off --env-file /absolute/private/off/compose.env
```

It requires locally available immutable images labeled
`org.opencontainers.image.revision` with the selected commit. It never pulls,
builds or runs anything. For deliberate source qualification, build all three
product images from the clean frozen checkout with `--build-arg COMMIT=<commit>`,
select their immutable image IDs, then run the preflight. Source-built operator,
edge and metrics helpers remain explicitly local. Build them separately from
the clean same-source checkout, then place their inspected immutable IDs in
the private Compose env file. For example, using a temporary commit-specific
build tag:

```sh
docker build --build-arg COMMIT="$LANTERN_COMPOSE_SOURCE" \
  -f deploy/compose/oidc-ha/metrics-relay.Dockerfile \
  -t "lantern-metrics-relay:$LANTERN_COMPOSE_SOURCE" .
docker image inspect --format '{{.Id}}' "lantern-metrics-relay:$LANTERN_COMPOSE_SOURCE"
```

Repeat with `operator.Dockerfile` / `edge.Dockerfile` for OIDC. The image matrix
preflight reads the **actual rendered canonical services** from that same env
file and requires every selected helper. Passing an unrelated spare image ID
cannot validate a different runtime tag. Use `--mode oidc` and its private env
file for OIDC. Ordinary startup uses `--no-build`. Neither labels nor this
identity-only preflight establish runtime, provider or publication acceptance.

## OFF: explicit private peer preparation

Public OFF means anonymous data access, not anonymous replication. Keep every
published port loopback-only. Provision the existing private workload domain,
separate certificates, signed membership and publisher journal before startup;
see [private membership distribution](../../docs/live-membership-renewal.md#one-supported-container-distribution-topology).
No runtime PostgreSQL, identity provider or shared authorization database is added.

Set `LANTERN_HA_CONFIG_DIR` to the private absolute layout and
`LANTERN_RUN_UID` / `LANTERN_RUN_GID` to its dedicated non-root owner. Each
`lantern-N.env` must explicitly supply the fixed `LANTERN_NODE_ID`, peer domain,
workload ID/listener, certificate/key/CA/operator-public-key paths, signed
membership path and owned `/state/membership.state` path. Each matching
`lantern-N/` directory contains **only that node's** private peer inputs and
`ntp.conf` (`server time.asia.apple.com`). The mount is read-only at
`/run/lantern-config`; renewable membership must be directory-published there
at its original selected path. Operator/CA signers and publisher state stay
outside all Server mounts. Keep the external publisher running while nodes run
or drain. Never use fixture generation as production trust provisioning.

Use the normal `membershippublisher` with a Version 1 configuration containing
one `data` stream for this signed OFF domain. Its `Initial` is the exact existing
signed manifest, `Targets` are the three selected per-node membership input
paths, and its operator key, roots and workload certificates are the original
private-plane inputs. Follow the [publisher configuration and capacity
checks](../../docs/live-membership-renewal.md#declare-and-provision-a-finite-window).
Prepare the operator configuration and journal outside all Server mounts.
Before first node initialization, start this separate owned process once:

```sh
membershippublisher -config /absolute/private/off/operator/publisher.json \
  -state /absolute/private/off/operator/state.json -mode init
```

An ordinary publisher restart uses `-mode resume` with those same intact files;
it never runs `init` again. Keep it live while stopping or restarting nodes, then
join it only after their CLEAN shutdown. Retain its journal and lease sidecars.
The fixture renewal command is not a substitute for this durable publisher.

Supply independent metrics server leaves in
`tls/metrics/lantern-N/{server.pem,server-key.pem,ca.pem}` and the separate
Prometheus client leaf in `tls/metrics/client/{client.pem,client-key.pem,ca.pem}`.
Use the exact SANs, usages, owners and 0700/0600 layout described in the
[shared TLS preparation](oidc-ha/README.md#private-preparation). The Server's
metrics remain loopback-only; the relays expose only authenticated collection
on the private network. These metrics keys are separate from workload identity.

From `deploy/compose`, select a unique project and the private env file:

```sh
dc() { docker compose --project-name lantern-off-example \
  --env-file /absolute/private/off/compose.env -f docker-compose.yml "$@"; }
dc config --quiet
# Once only, for three NEW empty private membership state directories:
dc -f off-initialize.yml up -d --no-build --wait --wait-timeout 120 lantern-0 lantern-1 lantern-2
# Inspect logs and require CLEAN shutdown before continuing:
dc stop -t 45 lantern-0 lantern-1 lantern-2
# Every ordinary start, including the first complete stack, resumes intact state:
dc up -d --no-build --wait --wait-timeout 120
```

Before first setup, prepare distinct empty private `state/lantern-N`,
`backup/lantern-N` and `spool/lantern-N` directories owned by the selected UID/GID
(mode 0700). The canonical stack binds them without host-path creation. It
never changes ownership or initializes a missing directory at startup.
OFF retains this dedicated state, backup and graph-snapshot spool per node. The bounded spool is 128 MiB with a
32 MiB frame cap. Graph snapshots restore data, not original receipt continuity.
Never include `off-initialize.yml` during ordinary restart or recover by deleting
volumes. Missing/partial/mismatched native state is a failure to diagnose.

Open <http://localhost:8080>; a fresh browser selects this same-origin Gateway.
Saved direct-server selections remain selected. Public endpoints 6380/6381/6382,
MCP 6390 and Prometheus 9091 remain loopback-only. OFF has no user Roles and
cannot establish an OIDC authorization refusal. All MCP callers share anonymous
backend access; keep its unauthenticated ingress local/private.

## OIDC: exact-origin full stack

Follow the existing [three-node preparation and lifecycle guide](oidc-ha/README.md),
using canonical `-f docker-compose.oidc.yml`. It reuses the existing HA
implementation, not a second replication framework. The guide covers complete
current startup JSON on provision/init/every start, distinct node custody,
separate application WAL creation, retained membership publication, exact HTTPS
origin, verified upstream TLS, scoped MCP credentials and clean restart.

The default HTTPS edge is <https://localhost:8443>. Browser/auth/control traffic
is pinned to node 0; data replication does not create transparent browser
transaction failover. Ordinary Bearer data RPCs can use eligible verified data
backends without automatic uncertain-mutation replay. The same-origin Gateway
initial default fixes new browser setup; deliberate saved/direct choices stay
unchanged. A static SPA served without a proxy must select its direct Server
explicitly (Vite development retains the localhost:6380 default).

Enroll a separate scoped noninteractive MCP Principal and explicitly assign the
provided Allow-only data Role. Supply only its accepted credential in private
`mcp.env`; handle expiry/refresh explicitly. A browser cookie or Google ID token
is not an API credential. Container health or tools/list alone cannot prove MCP
operations. Read/write on data never grants management or Ops. Assign the
provided read-on-Operations Role explicitly to the intended human operator for
Admin status/replication/search/metrics; security_admin and data_admin do not
implicitly include it.

## Qualification and retained restart

Both stacks scrape exactly `lantern-0`, `lantern-1`, `lantern-2`. Admin proxies
`/api/prom` to the fixed Prometheus upstream and checks Operations authority in
OIDC. Prometheus receives neither browser cookies nor Authorization. The UI
separates authentication, missing Operations permission, unreachable Server,
missing/unreachable Prometheus and an invalid HTML/API response.

Budget the final exact-source checks once on a coherent frozen matrix:
three-node replication, allowed/denied data and MCP operations, actual Ops and
named per-replica/sum query_range history, all four global Edge modes, a bounded
node outage/recovery, then orderly CLEAN stop and intact restart preserving
original identities, Role changes and global policy. Reuse immutable images and
unchanged inputs; never retry an uncertain mutation. The existing bounded
[driver](oidc-ha/check.py) verifies a subset and retained sentinel; it does not
by itself prove all-replica convergence/outage or global policy acceptance.

Stop clients/proxies/Prometheus/relays first, drain all Servers while the
publisher remains live, confirm CLEAN state, then stop the publisher. Retain
all volumes, receipt inputs, private keys and journals. Resume in the ordinary
configuration; never use `down -v`, reinitialize, or reset trust to obtain a pass.

The redundant generic HA and named-spool overlays have been removed; both
canonical stacks already own those resources. The standalone
[`docker-compose.membership.yml`](docker-compose.membership.yml) retains the
specialized static/renewable/state operator layout and its optional OFF spool
bind addon; it is not merged with a canonical stack. `docker-compose.off.yml`
remains an explicitly selected small standalone development example. Existing
physical Dart, real-provider app and Kubernetes acceptance holds remain separate.
