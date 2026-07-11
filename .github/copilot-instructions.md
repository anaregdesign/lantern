# Lantern — Copilot instructions

Lantern is an in-memory graph KVS (`key-vertex-store`) served over Connect/HTTP-2
(wire-compatible with gRPC and gRPC-Web on a single h2c socket); both vertices and
edges carry TTLs and decay over time. The repo is a monorepo: a multi-module Go
workspace (`go.work`), a pure-Dart SDK (`sdks/dart/`), and standalone TypeScript
packages, with every non-Go package **outside** `go.work`.

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

## Architecture invariants

- **The RPC surface is plural-first.** Every read/write/delete has a singular and a
  plural form; the plural is the canonical implementation and the singular forwards a
  one-element batch to it. When adding write surface, implement the plural first and
  the singular as a thin facade — never duplicate logic.
- **Dependency direction is a DAG — no back edges.** `pb` and `core` are leaves.
  `sdks/go` imports `pb` only (never `core`/`server`). `server` imports `pb` and `core`
  only (**never** the client SDK). The root module (cli + tests/integration) is the only
  place that depends on everything. Cross-module/full-stack tests live in
  `tests/integration/`, not under the producing package.
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

- **File a GitHub Issue before any non-trivial change.** Exceptions: doc-only edits,
  in-flight PR-review follow-ups, and one-line obvious fixes. The PR closes it with
  `Closes #N` — use one keyword per issue (`Closes #1, closes #2`), since GitHub only
  auto-links the first issue on a comma-separated line.
- **PR titles must be Conventional Commits** (`feat`/`fix`/`docs`/`chore`/`ci`/
  `refactor`/`perf`/`test`/`build`/`revert`); a required check rejects others.
- **Before every push**, run the local quality gate: `gofmt -l` must print nothing,
  then `go test ./...` from the root **and** from each Go submodule (the root run does
  not span submodules), plus Dart format/analyze/test in `sdks/dart/`.
- Wait for all required CI checks before merging; never use `--admin`/`--no-verify`.
  Merge with `--squash --delete-branch`. Never push to `main` directly.

## Admin SPA (`admin/`)

A separate Bun + React Router + Fluent UI v9 + Sigma.js stack — see `admin/AGENTS.md`.
When editing admin code, follow the vendored `react-router-app-architecture` skill.
