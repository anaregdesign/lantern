# Admin SPA — Agent Instructions

The browser control surface for Lantern. This is a **standalone TypeScript package**,
not a Go module, and is **not** part of `go.work`. See [README.md](README.md) for setup
and scripts; this file is the always-on agent context for editing admin code.

## Stack

- **React Router (SPA mode)** + **TypeScript**, bundled by **Vite**.
- **Fluent UI v9** (`@fluentui/react-components`) for UI; **Sigma.js** + **graphology**
  for graph rendering.
- **Bun** is the toolchain and package manager — the exact version is pinned in
  `package.json` (`packageManager`); read it there, don't hard-code a number.
- Talks to the Lantern server over **Connect-Web** via `lantern-sdk/web` (the browser
  subpath of the Node SDK). All protobuf types, value codecs, batch helpers, and error
  mapping live in the SDK — admin has no Connect-Web codegen of its own.

## Follow the React Router app-architecture skill

When editing admin code, follow the vendored `react-router-app-architecture` skill
(`.github/skills/react-router-app-architecture/`). It owns route boundaries, component
structure, use-case/repository-port layering, Fluent UI usage, and Playwright
verification. The conventions below are the load-bearing subset.

## Structure conventions (principle-based — do not enumerate routes here)

- **One React component per `.tsx`**, filename matches the export, with a colocated CSS
  Module. Use **CSS Modules**, not Fluent `makeStyles`, for component styling.
- UI-facing use cases live under `app/lib/client/usecase/`; browser/HTTP adapters under
  `app/lib/client/infrastructure/`. All per-RPC adapters route through the single client
  builder in `app/lib/client/infrastructure/api/`.
- There is **no `lib/server/`** — the SPA calls the server directly from the browser, so
  CORS is enforced server-side. The server's `LANTERN_CORS_ALLOWED_ORIGINS` must include
  the admin origin.
- `app/styles/` is reserved for `FluentProvider` host wiring only; feature styles live
  with their components.

## Quality gate (before pushing admin changes)

Run from `admin/`:

```bash
bun run format
bun run lint
bun run typecheck      # react-router typegen, then tsc -b
bun run test           # unit tests (do NOT use bare `bun test`)
bun run build
bun run test:e2e       # Playwright; free the server ports first
```

Use the package scripts, not bare `bun test` — the `test` script scopes Bun's runner to
the right paths. The Playwright suite needs a running server, so stop any local compose
stack that holds the ports before starting it.
