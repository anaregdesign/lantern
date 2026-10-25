# Authenticated three-node Compose example

[`docker-compose.oidc-ha.yml`](../docker-compose.oidc-ha.yml) includes three fixed
OIDC Servers, Admin, an HTTPS edge, Prometheus, per-node mTLS metrics relays,
MCP and a separate membership publisher. Build it from this checkout; this is
an operator template, not evidence that a published image, real provider account
or target platform has passed acceptance. It performs no automatic provider
registration, account enrollment or permission grant.

The edge publishes only loopback `https://localhost:8443`. Admin and
browser/native/security routes stay pinned to `lantern-0` to retain login attempt
and session state. Ordinary Bearer data RPCs use the three verified HTTPS
backends without automatic request retries. Backend replication has a fixed
three-member quorum; loss of node 0 still interrupts browser login and Admin
management. This example does not provide automatic browser transaction
failover or dynamic membership expansion.

Prometheus publishes loopback port 9091. MCP publishes loopback port 6390 at
`/mcp`; its endpoint has no caller authentication and every tool uses the one
configured backend credential. Keep it local/private or add a separately
reviewed authenticated transport before sharing it. An announced MCP agent ID
does not choose a different Principal.

## Private preparation

Use a dedicated non-root local owner, a new private directory outside the
checkout/build context, and a unique Compose project name. From the repository
root:

```sh
python3 deploy/compose/oidc-ha/prepare.py --directory /absolute/private/oidc-ha
```

The helper writes placeholder JSON and one-time random node IDs plus a shared
receipt deployment epoch. It creates mount parents `material/` and `operator/`
but leaves final `material/identity` and `operator/custody` absent for create-only
provisioning. It creates no keys, credentials, native state, network resources or
containers. Retain its output; rerunning against an existing directory refuses
to regenerate anything.

Complete each `config/lantern-N/startup.json` privately. These are the **same
complete input form** used by provision, init and every start/restart, including
current Issuers, secrets and public TLS. The three files differ only in selected
`node` and node public TLS material; their fixed topology, seed identity and
desired Issuer intent must agree. Keep the exact container paths
`material_root=/material/identity`, `persistence_root=/state`, the named topology,
and `target_platform=linux`. Generating on a Mac must not stamp Darwin semantics
into a Linux container. The supplied JSON is read afresh from `/input/startup.json`
at every invocation; retained identity material is never a configuration fallback.

Each Linux node also receives the repository's [ntp.conf](ntp.conf) as a
read-only `0444` Compose file config at `/etc/ntp.conf`. Its exact admitted source
is `server time.asia.apple.com`; file-backed delivery keeps the service root
read-only. This is application input, not a host time-service configuration
change or evidence of trusted-time qualification.

Replace the Google browser client ID/secret and exact verified initial admin
subject. Register the callback for the exact public origin using the
[callback recipe](../../examples/auth/README.md#browser-callbacks). The Google
browser profile needs no API audience. The separate API Issuer is deliberately
an invalid placeholder: set its actual issuer, algorithms and API audience only
after verifying its RFC 9068 token contract. The example explicitly requests
`human_subject_namespace_qualified=true`: qualify its human subject namespace
against client impersonation/noncollision before asserting this property.
Do not turn an ID token into an API credential or broaden audience acceptance.

Supply private TLS files listed in `TLS-REQUIRED.txt`:

| Consumer | Files and exact scope |
| --- | --- |
| Each Server | Its own public TLS certificate/key PEM in its current JSON; SAN `lantern-N`, server authentication. |
| Edge | `tls/edge/server.pem`, `server-key.pem`: server leaf with SANs `localhost` and `edge`; `ca.pem` pins the Server public TLS CA. |
| Admin and MCP | `tls/trust/ca.pem`: public CA material that verifies the edge and Server public leaves. Use one dedicated example CA for these leaves. |
| Metrics relay N | `tls/metrics/lantern-N/server.pem`, `server-key.pem`: separate serverAuth-only leaf with SAN `lantern-N`; `ca.pem` pins the metrics CA. |
| Prometheus | `tls/metrics/client/client.pem`, `client-key.pem`: separate clientAuth leaf; `ca.pem` pins that metrics CA. |

Keep CA signers outside all serving mounts. Metrics credentials are independent
of Server workload TLS, voting/origin keys and operator signing custody. Keep
directories `0700`, files `0600`, owned by the configured non-root UID/GID.
The helper's `compose.env` selects that owner. No TLS verification bypass is
present. If you change the public port/origin, update the supplied JSON,
certificates, callback registration and client URLs together.

## Explicit first initialization

Run the following from `deploy/compose`; `dc` selects only this new project and
private input file. Render with `config --quiet`; ordinary rendered output
contains environment values and must not be shared.

```sh
cd deploy/compose
dc() { docker compose --project-name lantern-oidc-ha-example \
  --env-file /absolute/private/oidc-ha/compose.env \
  -f docker-compose.oidc-ha.yml "$@"; }
dc config --quiet
dc build lantern-0 admin lantern-mcp membershippublisher metrics-0
# Once, only for this project's three NEW empty state volumes:
dc --profile prepare run --rm --no-deps prepare-state
# Once: creates /material/identity and /operator/custody, both previously absent.
dc --profile provision run --rm --no-deps provision-security
# Once: initializes the publisher journal; keep it running during node init.
LANTERN_PUBLISHER_MODE=init dc up -d membershippublisher
# Sequential explicit native initialization on the actual Linux target:
dc --profile init run --rm --no-deps init-node-0
dc --profile init run --rm --no-deps init-node-1
dc --profile init run --rm --no-deps init-node-2
dc stop -t 45 membershippublisher
```

Native initialization must finish successfully with CLEAN state. It does not
start application serving or initialize a data WAL. `prepare-state` only assigns
the mounted persistence roots; it never creates final native node directories,
repairs ownership on retained state or removes existing files. On an interrupted
or failed step, retain the output and diagnose the failure. Never delete it and
rerun automatically.

Create each **new, empty application WAL** with this separate one-time ceremony:

```sh
dc -f oidc-ha/initialize-data.yml up -d --wait --wait-timeout 120 \
  lantern-0 lantern-1 lantern-2
dc stop -t 45 lantern-0 lantern-1 lantern-2 membershippublisher
# Every ordinary start, including the first full stack start, uses restart:
dc up -d --wait --wait-timeout 120
dc ps
```

Do not include `initialize-data.yml` again. Normal configuration always requires
the existing WAL, stable node ID/epoch, original identity and native custody;
missing/partial/mismatched state fails closed. State is durable in three distinct
named volumes, bound to the corresponding node. Keep all three receipt env files,
volumes, identity material and operator journal. Prometheus history uses bounded
temporary storage in this example; it is not an application backup.

The publisher renews both control and data memberships independently of Server.
Default finite operator windows are 30 days; short signed manifests continue to
expire if publication stops. Renew same-identity workload leaves/window before
their boundary using the [explicit maintenance procedure](../../../docs/live-membership-renewal.md#renew-generated-workload-certificates).
The operator service needs only its custody/journal and the three renewable
membership directories. Servers receive only their own node directory read-only;
neither operator keys nor sibling node keys enter a Server mount. Keep exact
absolute paths when reviewing/installing renewal candidates; do not regenerate
trust, copy/rebase identity files or reset state to recover from expiry.
Keep renewal candidate configurations under the mounted operator parent, for
example `/operator/renewal-1/publisher.json`. After the reviewed stopped-journal
advance, set `LANTERN_PUBLISHER_CONFIG` in private `compose.env` to that exact
immutable configuration and recreate the publisher in `resume` mode. Retain
every previous config and use the active path for the next renewal. Never
overwrite `/operator/custody/publisher.json` or initialize a new journal.

## Enroll the scoped MCP credential explicitly

Sign in to Admin with the initial administrator and review the supplied
`mcp-role.change.json.example`, `operations-role.change.json.example` and
`service-principal.changes.json.example` before applying individual management
changes with current revisions and operation reauthentication where required.
They are `SecurityChange` ProtoJSON examples, not automatically applied seed
policy or a bulk request. Replace the API subject with the exact independently
verified subject and confirm its ACTIVE enrollment. Assign only `ha_mcp_context`
to the MCP identity. The optional operations Role grants only `OperationsRead`;
assign it explicitly to the intended human operator when using Admin `/ops`.

Put that identity's valid, correctly typed/audienced API JWT into private
`mcp.env` as `LANTERN_TOKEN`, then recreate only `lantern-mcp`. It must remain valid
for the bounded check; refresh it explicitly when it expires. A separately
provisioned existing machine credential is an alternative only if the Server
already accepts it. This example provides no machine token issuance API and
never grants authority just by adding Issuer trust.

The proposed Role allows Vertex read/write/delete and query for `agents.`,
`claims.demo.`, `notes.` and `demo.`. Edge access is derived from live endpoints
and those Vertex grants; no independent Edge action is installed. Use `demo.*`
resource keys and existing endpoints for MCP activity/claims. Global context
statistics or unrelated resources can correctly fail under this scope. Every
MCP caller shares these grants, so do not expose this endpoint to untrusted
callers. Admin's Prometheus proxy still checks authenticated operations access;
the local Prometheus UI is an operator-only diagnostic endpoint.

## Bounded checks and retained restart

After explicit enrollment, the source-only acceptance driver checks verified
HTTPS/Admin readiness, API allow/deny, exactly three healthy Prometheus targets,
and actual MCP announce/list/denied-track tools. It creates one random permanent
`demo.ha_acceptance.*` sentinel once. It never retries mutations or starts/stops
services; run it only against this isolated example. Evidence contains synthetic
sentinel metadata and input/material hashes, never tokens or current JSON.

```sh
python3 oidc-ha/check.py --phase before --private-directory /absolute/private/oidc-ha \
  --evidence /absolute/private/oidc-ha/checkpoint.json
dc stop -t 45 lantern-mcp edge admin prometheus metrics-0 metrics-1 metrics-2
dc stop -t 45 lantern-0 lantern-1 lantern-2
dc stop -t 45 membershippublisher
# Confirm orderly CLEAN shutdown; preserve all state and private inputs.
dc up -d --wait --wait-timeout 120
python3 oidc-ha/check.py --phase after --private-directory /absolute/private/oidc-ha \
  --evidence /absolute/private/oidc-ha/checkpoint.json
```

The after phase requires unchanged input/material/receipt identities and the
same retained sentinel. It does not prove real-provider login or a physical
device flow. Synthetic IdP/controlled-time execution must be labeled as such and
does not replace the normal configured-source trusted-time gate. Bound readiness
attempts and back off on time acquisition failure; do not repeatedly recreate a
fresh cohort or probe live NTP to obtain a pass. Keep the source/CI/target/provider
qualification status explicit. Do not use `down -v`, delete volumes/custody, or
rerun fresh initialization to make a restart succeed.
