# Lantern — Copilot instructions

Lantern is an in-memory graph KVS (`key-vertex-store`) served over Connect/HTTP-2
(wire-compatible with gRPC and gRPC-Web on a single h2c socket); both vertices and
edges carry TTLs and decay over time. The repo is a monorepo: a multi-module Go
workspace (`go.work`), a pure-Dart SDK (`sdks/dart/`), a native Rust crate
(`sdks/rust/`), and standalone TypeScript packages, with every non-Go package
**outside** `go.work`.

`AGENTS.md` is the full agent guide and `CONTRIBUTING.md` is the release/CI/maintenance
contract. This file is the short, always-on subset — when it and `AGENTS.md` overlap,
they must not conflict.

## Never hand-edit generated code

- `pb/**` — protobuf messages + Connect-Go stubs. Regenerate with `go generate ./...`
  (runs `buf generate`; **never** pass `--clean`).
- `server/cmd/wire_gen.go` — google/wire output. Regenerate from `server/` with
  `go tool wire ./cmd`.
- `sdks/dart/lib/src/gen/**` — Dart Protobuf + Connect stubs. Regenerate with
  `sdks/dart/scripts/codegen.sh` (which cleans only that generated directory).
- `sdks/rust/src/generated/**` — private Tonic/Prost stubs. Regenerate from
  `sdks/rust/` with pinned `cargo xtask codegen`, never at library build time.

## Architecture invariants

- **Minimize external dependencies.** Lantern is the database; Server auth
  uses native storage/replication/persistence, never PostgreSQL. ADR 0012 plans
  `sys:` metadata and physical `data:` keys behind public logical keys; the
  boundary is not yet qualified and does not itself prove policy freshness.
  The Dart SDK SQLite route, including `offline_sqlite`, is approved.

- **The RPC surface is plural-first.** Every read/write/delete has a singular and a
  plural form; the plural is the canonical implementation and the singular forwards a
  one-element batch to it. When adding write surface, implement the plural first and
  the singular as a thin facade — never duplicate logic.
- **Dependency direction is a DAG — no back edges.** `pb` and `core` are leaves.
  `sdks/go` imports `pb` only (never `core`/`server`). `server` imports `pb` and `core`
  only (**never** the client SDK). The root module (cli + tests/integration) is the only
  place that depends on everything. Cross-module Go integration tests live in
  `tests/integration/`, not under the producing package; standalone Node real-wire
  tests live in `sdks/node/test/` per `CONTRIBUTING.md`.
- **Rust is standalone.** `sdks/rust/` is a Cargo crate outside `go.work`,
  and its edition-2024 private module is `generated` (`gen` is reserved).
  No generated gRPC service clients are exported from the crate root.
- **SDK value accessors are free functions, not methods**: `Kind(v)`, `IntValue(v)`,
  `StringValue(v)`, etc. `client.Vertex`/`client.Edge` are true aliases of the `pb`
  types (one `Vertex` type, no boundary casts). Adding a value type updates three sites
  in `sdks/go/value.go`.
- **Pre-v1.0.0: no backward-compatibility guarantees.** Break the proto/wire schema, SDK
  APIs, CLI grammar, `LANTERN_*` env vars, and metric names freely — prefer the cleanest
  design, don't hedge for old clients. Canonical home: CONTRIBUTING.md "Versioning &
  compatibility". (Separate, still forbidden: `buf generate --clean`.)

## Go conventions

- **1:1 source↔test pairing.** Each `xxx.go` has at most one `xxx_test.go` in the same
  package. Do **not** add `xxx_<concern>_test.go` siblings — extend the existing
  `xxx_test.go` with sub-tests (`t.Run(...)`). The only permitted extra is
  `xxx_external_test.go` (black-box, package `xxx_test`). Cross-cutting suites touching
  ≥3 sources use `_gate_test.go`; shared test helpers live in `helpers_test.go`.
- Put a new dependency in the module that actually imports it, then run `go mod tidy`
  in every affected module (workspace `replace`s don't propagate `go.sum`).
- The Go toolchain version is whatever each `go.mod` and the CI workflows pin — read it
  there, never assume or hard-code a number.

## Workflow (hard rules)

- Write GitHub Issue titles, bodies, comments and updates in English.

- **Search existing Issues; link or file one before any non-trivial change**, including
  bugs, improvements, and validation repairs discovered mid-PR. Only already-scoped
  direct doc edits or proofreading and in-flight review fixes to the already-filed
  Issue are exempt; newly discovered bugs or improvements, including in docs,
  need an Issue even if one line. A cohesive PR may close related Issues with
  one keyword per Issue (`Closes #1, closes #2`).
- **Keep epic progress in independent exit buckets** for merged source/CI,
  final exact-source acceptance, publication, and human/device evidence, each with
  done/total and linked blockers. A branch diagnostic or simulator is not final
  acceptance.
- **Batch validation, not standards**: targeted checks during edits, full mandatory
  gate before each push. Budget costly whole-host/device runs before execution,
  preflight every required run/family boundary while reusing the one immutable
  image and unchanged setup, pin final merged source, and retain raw evidence
  with its SHA-256. Repeat only for a relevant build change, documented invalid
  run, or predeclared stability check; never select a pass or relax load, GC, or limits.
- **PR titles must be Conventional Commits** (`feat`/`fix`/`docs`/`chore`/`ci`/
  `refactor`/`perf`/`test`/`build`/`revert`); a required check rejects others.
- **Before every push**, run the local quality gate: `gofmt -l` must print nothing,
  then `go test ./...` from the root **and** from each Go submodule (the root run does
  not span submodules), plus Dart format/analyze/test in `sdks/dart/` and Flutter
  analyze/test in `sdks/dart/example/`; the standalone Rust crate has its own
  format, Clippy (warnings denied), test, and warning-free doc gate in
  [CONTRIBUTING.md](../CONTRIBUTING.md).
- **Dart releases are independent.** `sdks/dart/vX.Y.Z` must match
  `sdks/dart/pubspec.yaml` and `CHANGELOG.md`; the tag workflow runs Dart plus
  Android/iOS gates, publishes `lantern_client` through pub.dev OIDC, and only then
  creates an exact-title GitHub Release. Never store a pub token.
- **Rust releases are independent.** `sdks/rust/vX.Y.Z` must match
  `sdks/rust/Cargo.toml` and `CHANGELOG.md`. The first crates.io publish is
  owner-held after archive verification; later versions use protected OIDC.
  Verify the registry archive before the exact-title GitHub Release.
  Never store a registry token.
- Wait for all required CI checks before merging; never use `--admin`/`--no-verify`.
  Merge with `--squash --delete-branch`. Never push to `main` directly.

## Admin SPA (`admin/`)

A separate Bun + React Router + Fluent UI v9 + Sigma.js stack — see `admin/AGENTS.md`.
When editing admin code, follow the vendored `react-router-app-architecture` skill.
