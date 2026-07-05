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
  `illuminate.tsx`, `ops.tsx`).
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
  -e LANTERN_ADMIN_PROMETHEUS_UPSTREAM=http://prometheus:9090 \
  ghcr.io/anaregdesign/lantern-admin:latest
# Ops Metrics → /api/prom/api/v1/query_range → Prometheus /api/v1/query_range
```

- **Opt-in.** With `LANTERN_ADMIN_PROMETHEUS_UPSTREAM` unset, the proxy is
  a no-op and the Metrics section degrades gracefully (it shows an
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

The container does **not** reverse-proxy the Lantern gateway — the user's
browser talks to the gateway directly, so the server must have
`LANTERN_CORS_ALLOWED_ORIGINS` set to allow the admin origin (see
[`server/README.md`](../server/README.md)). Switch between gateways at
runtime via the **Gateway** button in the header. The container _does_
optionally reverse-proxy Prometheus same-origin under `/api/prom` for the
Ops Metrics page — see [Metrics (Prometheus)](#metrics-prometheus).

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
