# Server — Agent Instructions

The Lantern Connect server module. See [README.md](README.md) for the package layout,
env-var contract, and observability endpoints; this file is the always-on agent context
for editing server code. Root [../AGENTS.md](../AGENTS.md) covers cross-module
architecture.

Apply the repository's [`v0.*.*` compatibility policy](../CONTRIBUTING.md#versioning--compatibility-pre-v100). Use the simplest correct design and remove compatibility-only Server paths when you encounter them.

## RPC surface is plural-first

[internal/dataruntime/executor.go](internal/dataruntime/executor.go) implements
`Executor`; `service/connect.go` supplies the Connect adapter. Every
read/write/delete has a **singular** and a **plural** form. The plural is the canonical
implementation; the singular forwards a one-element batch to its plural counterpart
(e.g. for `GetVertex`/`GetEdge`, `len(Missing)==1` maps to `codes.NotFound`; for
`DeleteVertex`/`DeleteEdge`, `Existed = resp.GetExisted()[0]`). When you add write
surface, **always implement the plural first and the singular as the facade** — never
duplicate logic.

## Dependency boundary

The server imports `pb/` and `core/` only — **never** the client SDK. If a server test
needs the client (e.g. a full-stack bufconn round-trip), put it in `tests/integration/`
in the root module instead of under `server/`.

## Dependency injection: google/wire

- [cmd/wire.go](cmd/wire.go) holds the provider list (hand-edited, build-tagged
  `wireinject`).
- [cmd/wire_gen.go](cmd/wire_gen.go) is generated — **never hand-edit it.** Regenerate
  from this directory after changing providers:

  ```bash
  go tool wire ./cmd       # or: make wire (from repo root)
  ```

- **Providers take the focused sub-config slice they actually need** (e.g.
  `NewListener` takes `NetConfig` plus the private runtime-certification
  marker), not the aggregate `*Config`. Keep that SRP invariant when
  adding providers; only `App`/`main` may hold `*Config` because they observe multiple
  slices. Ordinary operational values, defaults and semantic validation live in
  `internal/config/`; primitive parsing, immutable input snapshots and per-load diagnostics live
  in `internal/envconfig/`. `provider.NewConfig` completes all loaders before the
  unknown-key/strict sweep. Runtime material and private ownership certification
  remain provider/security concerns, including the cache namespace selection.
- `provider.NewServingRuntime` opens the node-owned Tarp database in graph-only
  or receipt mode. Restore the active graph generation, derived indexes, relay
  and origin frontiers, HLC floor and any receipt state before certifying the
  exact runtime and services for network use. Graph-only startup can recover a
  missing directory from its configured private backup. Receipt restart requires
  an existing matching database. Recover a lost receipt volume explicitly using
  an independently witnessed final cut. See [private recovery](../docs/backup.md).
- **wire cannot handle generic type arguments**, so the cache provider returns the
  concrete `GraphCache[string, *Vertex]`. Re-check this before trying to introduce
  generics in the provider graph.

## Tests

Follow the repo-wide 1:1 source↔test pairing (one `xxx_test.go` per `xxx.go`; sub-tests
via `t.Run`, not `xxx_<concern>_test.go` siblings). The service/wire layers are
historically under-tested — add at least a minimal table test alongside any non-trivial
change.
