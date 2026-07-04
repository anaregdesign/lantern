# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Lantern is an in-memory graph KVS ("key-vertex-store") served over Connect/HTTP-2 (wire-compatible with gRPC and gRPC-Web on one h2c socket). Vertices and edges carry TTLs and decay over time.

**[AGENTS.md](AGENTS.md) is the full agent guide — read it before non-trivial work.** [CONTRIBUTING.md](CONTRIBUTING.md) is the process/release contract (issue triage, quality gate, tag order, toolchain bumps). This file is the always-on summary; when docs overlap they must not conflict, and details live there.

## Commands

```bash
go build -v ./...                # build (same as CI)
go test ./...                    # tests — root module only, does NOT span submodules
go test -run TestName ./path/to/pkg  # single test (run from the owning module)
go generate ./...                # regenerate wire_gen.go AND pb/ stubs (zero-install)
make wire                        # just wire: cd server && go tool wire ./cmd
make proto                       # just buf generate (never pass --clean)
make lint                        # golangci-lint (same as CI Lint job)
go run ./server/cmd              # start the server (:6380)
go run ./cli                     # start the CLI
```

**Pre-push quality gate** (matches required CI checks):

```bash
gofmt -l .                       # must print nothing
go test ./...                    # then repeat in EACH submodule:
(cd core && go test ./...); (cd mcp && go test ./...); (cd pb && go test ./...)
(cd sdks/go && go test ./...); (cd server && go vet ./... && go test ./...)
```

CI additionally enforces **per-module coverage floors** (a ratchet — table in CONTRIBUTING.md "Coverage floor"; raise the floor in the same PR when coverage rises durably) and fails the `Proto (buf)` check on any uncommitted codegen diff — regenerate and commit before pushing.

Admin SPA (`admin/`, Bun-managed, outside `go.work`): `bun run format && bun run lint && bun run typecheck && bun run test && bun run build` (use package scripts, never bare `bun test`). See [admin/AGENTS.md](admin/AGENTS.md).

Node SDK (`sdks/node/`, Bun-managed, outside `go.work`, npm `lantern-sdk`): from `sdks/node/` run `bun run codegen` (must leave `src/generated` clean — CI fails otherwise), then `bun run lint && bun run format:check && bun run typecheck && bun test && bun run build`. Single-endpoint only — no counterpart to the Go SDK's failover.

## Architecture

Multi-module Go workspace ([go.work](go.work)); dependency direction is a DAG with no back edges:

| Module | Role | May import |
|---|---|---|
| `pb/` | Generated protobuf + Connect-Go stubs (from `proto/` via buf). **Never hand-edit.** | (leaf) |
| `core/` | Reusable graph/cache/collections/concurrency/NLP building blocks | (leaf) |
| `sdks/go/` | Go client SDK (package name `client`) | `pb` only — never `core`/`server` |
| `server/` | Connect server, DI via google/wire | `pb` + `core` only — **never the client SDK** |
| `mcp/` | MCP server exposing Lantern as a multi-agent shared working context (#851; legacy memory verbs behind `LANTERN_MCP_PROFILE=memory`) — Streamable HTTP, `:6390/mcp` | `pb` + `sdks/go` |
| `.` (root) | CLI (`cli/`) + cross-module tests (`tests/integration/`) | everything |

- **Plural-first RPC surface** ([server/service/service.go](server/service/service.go)): every read/write/delete has singular + plural forms; the plural is the canonical implementation and the singular forwards a one-element batch to it. New write surface: implement plural first, singular as thin facade.
- **Generated code**: `pb/**` (regen `go generate ./...`; buf `--clean` is forbidden — it would delete `pb/go.mod`) and `server/cmd/wire_gen.go` (regen from `server/`: `go tool wire ./cmd`). Wire cannot handle generic type arguments — providers return concrete types.
- **Providers** ([server/provider/provider.go](server/provider/provider.go)): config comes from `LANTERN_*` env vars, split into focused sub-configs (`NetConfig`, `TLSConfig`, …); each provider takes only the slice it needs, not `*Config`. Env-var contract: [server/internal/envconfig](server/internal/envconfig).
- **SDK value accessors are free functions, not methods**: `Kind(v)`, `IntValue(v)`, `StringValue(v)`, etc. `client.Vertex`/`client.Edge` are true aliases of the `pb` types. Adding a value type updates three sites in [sdks/go/value.go](sdks/go/value.go).
- Server tests needing the client SDK (full-stack round-trips) go in `tests/integration/`, never under `server/`.
- HA/replication work implements against [docs/replication.md](docs/replication.md) (RFC); [docs/ha-runbook.md](docs/ha-runbook.md) is the operator playbook.
- `testbed/` is the release-QA harness (docker-compose Lantern+Prometheus, exhaustive CLI/SDK sweeps, HA smoke/recovery) — procedure in [testbed/SKILL.md](testbed/SKILL.md). `.github/skills/lantern-ops` is the canonical `lantern-cli` data-operations reference; `.github/skills/react-router-app-architecture` governs admin SPA work.
- **Pre-v1.0.0: no backward-compat guarantees** — break the proto/wire schema, SDK APIs, CLI grammar, `LANTERN_*` env vars, and metric names freely; don't hedge for old clients. Canonical: CONTRIBUTING.md "Versioning & compatibility". Separate & still forbidden: `buf generate --clean`.

## Go conventions

- **1:1 source↔test pairing**: each `xxx.go` has at most one `xxx_test.go` in the same package. Never add `xxx_<concern>_test.go` siblings — extend the existing file with `t.Run(...)` sub-tests. Only permitted extras: `xxx_external_test.go` (black-box, package `xxx_test`), `_gate_test.go` (cross-cutting suites touching ≥3 sources), `helpers_test.go` (shared helpers).
- New dependencies go in the module that actually imports it, then `go mod tidy` in **every** affected module (workspace `replace`s don't propagate `go.sum`).
- Go toolchain version: read it from `go.mod`/CI workflows — never hard-code a number in prose.

## Workflow (hard rules)

- **File a GitHub Issue before any non-trivial change** (exceptions: doc-only edits, in-flight PR-review follow-ups, one-line obvious fixes). PR closes it with one keyword per issue: `Closes #1, closes #2`.
- **External-surface Definition of Done**: a PR that adds/changes the RPC surface, SDKs, CLI grammar, MCP tools, `LANTERN_*` env contract, or TTL/decay semantics ships an integration test in `tests/integration/` (real wire path; happy + failure/edge case) in the same PR; perf-relevant hot paths also join a bench scenario (`testbed/bench/scenarios/` — release-sweep scenarios carry `perf_gate:` floors gating the nightly). Wire-schema changes must migrate the bench scenario templates in the same PR (`testbed/bench/scenarios_gate_test.go` enforces this in root `go test ./...`). Canonical: CONTRIBUTING.md "External-surface testing policy".
- **PR titles must be Conventional Commits** (`feat`/`fix`/`docs`/`chore`/`ci`/`refactor`/`perf`/`test`/`build`/`revert`) — a required check rejects others.
- Wait for all required CI checks; never `--admin`/`--no-verify`; merge `--squash --delete-branch`; never push to `main` directly.
- When work surfaces a fact another open Issue needs, comment it on that Issue in the same session (format in CONTRIBUTING.md).
