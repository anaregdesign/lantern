# Lantern Admin

Browser-based control surface for the [Lantern](../README.md) in-memory graph
KVS. Built with React Router (SPA mode), Fluent UI v9, Sigma.js, and
TypeScript.

This package is a top-level peer of `mcp/` and `sdks/`. It is **not** part of
the Go workspace (`go.work`). The TypeScript toolchain is [Bun](https://bun.sh/)
— same version pin as [`sdks/node/`](../sdks/node/README.md).

## Requirements

- [Bun](https://bun.sh/) `1.3.14+` (the version pinned in `package.json`).
- A running Lantern server (defaults to `http://localhost:6380`) with CORS
  configured to allow the admin origin — see
  `LANTERN_CORS_ALLOWED_ORIGINS` in [`server/README.md`](../server/README.md).

Admin talks to the server over the Connect protocol (HTTP/2) using
`lantern-sdk/web` — the browser subpath of the Lantern Node SDK
(#409). All protobuf message types, value codecs, batch helpers and
error mapping live in the SDK so admin no longer needs its own
Connect-Web codegen.

The `lantern-sdk` dependency is declared as
`"lantern-sdk": "file:../sdks/node"` so a fresh checkout points
straight at the sibling SDK source; run `cd sdks/node && bun install
&& bun run build` before the first `bun install` in `admin/` so
bun's copy can see the SDK's `dist/`.

## Quick start

```bash
cd sdks/node && bun install && bun run build  # one-time: build the SDK admin links to
cd admin && bun install
bun run dev                                   # http://localhost:5173
```

The server base URL can be changed at runtime via the **Gateway** button in
the top-right header. The choice is persisted to `localStorage`.

## Authenticated scopes and Add recovery

OIDC requires a same-origin HTTPS gateway and a valid browser session. Only a
supported, explicit OFF capability opens an anonymous view. Resume and permission
lease expiry hide protected views until a new authentication check succeeds;
identity, policy, gateway or selected scope changes abort and clear old results.

**Role scope** suggestions come from the Roles returned by the Server for the
current principal. Browse uses VertexRead prefixes; search and generated traversal
commands use the intersection with Query. The picker displays overlapping Deny
exceptions. Every request still passes the Server's authorization checks, including
typed CLI commands: their keys and prefixes are never silently rewritten. The
selection is held in memory and is not a persisted grant.

Add records bounded recovery metadata in per-tab `sessionStorage` before a possible
send. It contains gateway/identity, target and receipt identifiers, without browser
credentials, tokens or CSRF secrets. A lost or cancelled response remains uncertain
across reload and login. **Check original Add** reads the existing receipt Status
API with the original operation ID when receipts are supported and both endpoints
have current ReceiptRead grants. ReceiptRead is not required for ordinary Add.
The check never resends a
mutation. An unavailable, unobserved, undisclosed or no-longer-provable result keeps
the request locked. Legacy Add and decaying Add have no original-result receipt in
this flow: reading the current Edge cannot confirm them or authorize a retry.
Pending metadata is not automatically discarded to admit another Add.

## Scripts

| Script              | Purpose                               |
| ------------------- | ------------------------------------- |
| `bun run dev`       | Vite dev server on `:5173`.           |
| `bun run build`     | Production build to `build/client/`.  |
| `bun run start`     | Preview the built SPA on `:4173`.     |
| `bun run typecheck` | `react-router typegen` then `tsc -b`. |
| `bun run lint`      | ESLint (flat config).                 |
| `bun run format`    | Prettier write.                       |
| `bun run test`      | Unit tests (Bun test runner).         |
| `bun run test:e2e`  | Playwright smoke tests.               |

## Project layout

Follows the React Router app architecture conventions used in this repo. Key
points:

- `app/routes/` — FlatRoute file routing (`_index.tsx`, `browse.tsx`,
  `cli.tsx`, `ops.tsx`). The graph explorer is part of the CLI workspace.
- `app/components/<feature>/<Component>/` — one React component per `.tsx`,
  filename matches export, CSS Module colocated.
- `app/lib/client/usecase/` — UI-facing use cases (e.g. `connection/`,
  `theme/`).
- `app/lib/client/infrastructure/` — adapters that touch the browser or HTTP
  (e.g. `browser/storage.ts`, `api/lantern-client.ts`).
- `app/lib/client/infrastructure/api/lantern-client.ts` — builds the `lantern-sdk/web` client used everywhere; all per-RPC adapters in this directory route through it (#409).
- `app/styles/` — reserved for `FluentProvider` host wiring only. Feature
  styles live with their components.

No `lib/server/` is present in v1 because the admin app calls the Lantern
server directly from the browser over Connect-Web (CORS is enforced by the
server via `LANTERN_CORS_ALLOWED_ORIGINS`).

## Metrics (Prometheus)

The **Ops** page renders live Prometheus time-series charts (cache size,
throughput, RPC latency, TTL expirations, replication lag, Go runtime, …)
alongside the existing point-in-time status cards. The charts issue
`query_range` calls against a Prometheus server that scrapes the Lantern
`/metrics` endpoint.

Prometheus serves no CORS headers, so the browser cannot call it
cross-origin. The admin SPA therefore queries Prometheus **same-origin**
under `/api/prom` (the default), and the container reverse-proxies that
path to your Prometheus when `LANTERN_ADMIN_PROMETHEUS_UPSTREAM` is set:

```sh
docker run --rm -p 8080:8080 \
  -e LANTERN_ADMIN_SERVER_UPSTREAM=h2c://lantern:6380 \
  -e LANTERN_ADMIN_PROMETHEUS_UPSTREAM=http://prometheus:9090 \
  ghcr.io/anaregdesign/lantern-admin:latest
# Ops Metrics → /api/prom/api/v1/query_range → Prometheus /api/v1/query_range
```

- **Opt-in.** With `LANTERN_ADMIN_PROMETHEUS_UPSTREAM` unset, the proxy is
  rejected with an API error and the Metrics section degrades gracefully (it shows an
  "unreachable" banner instead of charts). Everything else on the Ops page
  keeps working.
- **Runtime override.** Change the Prometheus URL at runtime via the
  **Prometheus** button in the Metrics toolbar — a same-origin path like
  `/api/prom`, or an absolute `http(s)://…` URL if that server sends CORS
  headers. The choice is persisted to `localStorage`
  (`lantern.admin.prometheusUrl`), as is the selected time range
  (`lantern.admin.metricsRange`).
- **Dev server.** Under `bun run dev` / `bun run start` there is no Caddy
  proxy; point the **Prometheus** button at an absolute URL of a
  CORS-enabled Prometheus, or run the container image to get the
  same-origin proxy.

Ready-made Prometheus + admin stacks: [`deploy/compose/`](../deploy/compose/)
(`LANTERN_ADMIN_PROMETHEUS_UPSTREAM` pre-wired) and the Helm
`admin.prometheus.upstream` value in
[`deploy/helm/lantern/`](../deploy/helm/lantern/).

## Container image

Tagged releases of `admin/vX.Y.Z` publish a multi-arch (`linux/amd64`,
`linux/arm64`) image to
[`ghcr.io/anaregdesign/lantern-admin`](https://github.com/anaregdesign/lantern/pkgs/container/lantern-admin),
signed with cosign keyless. The image is Caddy 2 Alpine serving the built
SPA from `/srv` on port `8080`, with SPA fallback to `index.html`,
immutable cache headers on hashed `/assets/*`, and a `GET /healthz`
endpoint that returns `200 ok`.

```sh
# Pull and run the latest tagged admin.
docker run --rm -p 8080:8080 ghcr.io/anaregdesign/lantern-admin:latest
# → http://localhost:8080
```

In **OFF mode**, this default run serves the static Admin separately from the
Server. Select the direct Server address with the **Gateway** button and set
`LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080` on the Server (see
[`server/README.md`](../server/README.md)).

For **OIDC**, expose Admin over HTTPS and select that exact public origin in the
Gateway picker. Configure `LANTERN_OIDC_BROWSER_ORIGIN` to the same origin and
register the exact Issuer-specific `/auth/callback/<SHA-256>` redirect URI.
The container optionally proxies `/auth/*`, `/browser/*` and `/graph.v1.*/*` to
an operator-fixed `LANTERN_ADMIN_SERVER_UPSTREAM`; the gateway picker never
selects that upstream. Use the verified HTTPS upstream, optional private CA,
exact trusted proxy and preserved public Host/scheme configuration described in
[Fixed Server and diagnostics proxy](#fixed-server-and-diagnostics-proxy) and the
[operations guide](../docs/oidc-operations.md#browser-and-diagnostics-boundary).
This is the current fixed-writer baseline; future eligible-node routing remains
#1608/#1609 S5 work.

The optional same-origin `/api/prom` proxy for Ops Metrics requires the Server
upstream and checks `/auth/operations` admission before each GET. Prometheus
never receives cookies or Authorization — see [Metrics (Prometheus)](#metrics-prometheus).

### Releasing

Tag from `main` with the `admin/vX.Y.Z` prefix and push:

```sh
git tag admin/v0.1.0
git push origin admin/v0.1.0
```

This triggers
[`.github/workflows/admin-publish.yml`](../.github/workflows/admin-publish.yml),
which re-runs the admin gates (lint / typecheck / build), builds + pushes
the multi-arch image, signs it with cosign, and creates a GitHub
Release titled exactly `admin/vX.Y.Z` (per the AGENTS.md release-title
convention).

The admin module's only cross-module build-time dependency is
`sdks/node/` (the Lantern Node SDK admin links via `file:`). A
`pb/vX.Y.Z` bump that requires re-tagging admin must therefore flow
through an `sdks/node/v*` release first; the admin image is then
rebuilt against the new SDK.

## OIDC and security management

Authentication defaults to OFF. Admin discovers the Server mode explicitly; a
failed discovery or session request never becomes OFF. With OIDC, serve Admin
and the public Server/browser routes behind one HTTPS origin. The Server owns
Authorization Code/PKCE, opaque Secure/HttpOnly/SameSite cookies, state/nonce
and CSRF. Admin holds no OIDC token, browser client secret or bearer credential
in localStorage. Legacy stored bearer material is removed on mount.

The Security pages manage exact Issuer/subject identities and Role memberships.
Permissions belong only to Roles. Prefixes are literal logical keys; all-key
selection is explicit, and matching Deny wins. ExplainAccess is Server-derived.
Environment-owned Issuers, memberships and Role policies are locked. Mutations
require a reviewed revision and current explicit authority. Ordinary management
by a qualified human allows missing or older signed `auth_time`; valid
authentication, credential/session expiry and current policy admission still apply.
Changes to accepted Issuer/credential trust and effective `security.manage`
expansion require purpose-bound per-operation reauthentication. Machine
management mutations are prohibited. Response loss retains the original change
ID for status-only recovery. Committed/pending and globally enforced revisions
are shown separately. OFF exposes setup guidance without
an anonymous authentication toggle. IdP account/password/MFA management remains
with the provider. See [ADR 0012](../docs/decisions/0012-oidc-prefix-rbac.md).

`GetSecurityChangeStatus` returns retained commit proof for the original change
ID, version and enforcement. It does not return item outcomes or prove that
the original grants are still effective. Admin preserves request-aligned
outcomes only from an original Apply acknowledgement; after response loss it
shows item outcomes as unavailable. Status recovery retains the original ID
across policy remounts in the same browser session without sending Apply again.
Unknown or retired IDs remain indeterminate.

## Fixed Server and diagnostics proxy

The image entrypoint supports `LANTERN_ADMIN_SERVER_UPSTREAM` (fixed
`http`, `h2c` or `https` origin with explicit port) and optional
`LANTERN_ADMIN_SERVER_CA_FILE` for verified private-CA HTTPS. Serve OIDC Admin
over HTTPS and pin this upstream to the security writer. `/auth/*`,
`/browser/*` and `/graph.v1.*/*` API errors never fall through to SPA routing.
No credentials or client-supplied upstreams enter this configuration.

`LANTERN_ADMIN_PROMETHEUS_UPSTREAM` requires the Server upstream. Every GET
first passes `/auth/operations` admission, including current `operations.read`
in OIDC. Query parameters stay on the metrics query, and cookies/Authorization
never reach Prometheus. Missing, invalid or unavailable configuration fails
closed. The [operations guide](../docs/oidc-operations.md) describes the HTTPS,
private peer, renewal and recovery boundaries.
