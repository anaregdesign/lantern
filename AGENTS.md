# Lantern — Agent Instructions

Lantern is an in-memory `key-vertex-store` (graph-based KVS). It runs as a gRPC server, with both vertices and edges carrying TTLs so they decay over time. See [README.md](README.md) for the full overview.

## Monorepo layout

This project used to be split across four separate repositories (`lantern` / `lantern-proto` / `lantern-cli`, plus a shared graph/cache/NLP toolkit), but **everything is now consolidated into this repo**.

| Path | Origin | Role |
|---|---|---|
| `server/` | lantern (this repo) | gRPC server (DI via google/wire) |
| `sdks/go/` | lantern (this repo, formerly `client/`) | Go client SDK — **its own Go module** (`github.com/anaregdesign/lantern/sdks/go`) so external consumers can `go get` only the client without pulling in `server/`, `cli/`, or `core/` |
| `cli/` | former [`lantern-cli`](https://github.com/anaregdesign/lantern-cli) | Interactive CLI (cobra + promptui) |
| `proto/` | former [`lantern-proto`](https://github.com/anaregdesign/lantern-proto) | `.proto` sources (regenerated with buf) |
| `sdks/go/gen/` | former `lantern-proto/go` | Generated Go bindings (live inside the SDK module) |
| `core/` | shared toolkit (was a separate repo) | Common building blocks reused by server & CLI: graph, cache, collections, concurrency, NLP. **Not** imported by `sdks/go/`. |
| `tests/integration/` | new in this repo | Cross-module integration tests (root module wires `sdks/go` + `server/service` via bufconn) |
| `go.work` | new in this repo | Multi-module workspace pinning root + `./sdks/go` for local dev |

The module path in `go.mod` is still `github.com/anaregdesign/lantern` for the root, and the SDK is published as `github.com/anaregdesign/lantern/sdks/go` (subdirectory module). Tag SDK releases with the prefixed convention `sdks/go/vX.Y.Z`.

## Architecture notes

- **gRPC service**: [server/service/service.go](server/service/service.go) implements `LanternService`. Every read/write/delete has a **singular** and a **plural** form: `Illuminate`, `GetVertex`/`GetVertices`, `PutVertex`/`PutVertices`, `DeleteVertex`/`DeleteVertices`, `GetEdge`/`GetEdges`, `AddEdge`/`AddEdges`, `PutEdge`/`PutEdges`, `DeleteEdge`/`DeleteEdges`. The plural is the canonical implementation; the singular forwards a one-element batch to its plural counterpart (for `GetVertex`/`GetEdge`, `len(Missing)==1` maps to `codes.NotFound`; for `DeleteVertex`/`DeleteEdge`, `Existed = resp.GetDeleted() == 1`). When you add new write surface, **always implement plural first and singular as the facade** — do not duplicate logic.
- **DI**: [google/wire](https://github.com/google/wire). [server/cmd/wire.go](server/cmd/wire.go) holds the definitions; [server/cmd/wire_gen.go](server/cmd/wire_gen.go) is generated — **never edit it by hand**. After changing providers, regenerate with `go generate ./...` (or `make wire` for just the wire step). Wire itself is pulled in via the `tool` directive in `go.mod`, so no install is required.
- **Providers**: [server/provider/provider.go](server/provider/provider.go) assembles `Config` (env vars `LANTERN_PORT`, `LANTERN_DEFAULT_TTL_SECONDS`), `net.Listener`, `grpc.Server`, and `core/cache/graph.GraphCache`. `NewListener` now receives the wire-injected `*Config`.
- **Client SDK**: [sdks/go/client.go](sdks/go/client.go) is a thin gRPC wrapper. [sdks/go/value.go](sdks/go/value.go) handles Go-native ↔ `pb.Vertex` conversion via `nativeVertex.asVertex()` and `Vertex.*Value()`, and renders Go-friendly JSON via `Vertex.MarshalJSON` (shape: `{key,type,value,expiration}`). **When adding a new value type, update all three:** `asVertex` (Go → proto), the matching `*Value()` accessor plus `Kind()` / `VertexKind*` constant (proto → Go), and the `MarshalJSON` switch. The import path is `github.com/anaregdesign/lantern/sdks/go` and the package name remains `client`.
- **SDK dependency boundary**: the Go SDK is a separate Go module (`github.com/anaregdesign/lantern/sdks/go`) and depends only on `sdks/go/gen/graph/v1` + gRPC/protobuf — **never import `core/...` or `server/...` from `sdks/go/`**. Cross-module integration tests live in `tests/integration/` in the root module (they wire the real server via bufconn and exercise the SDK as an external consumer would). `Illuminate*` returns the SDK-local `client.Graph` (field shape `{Vertices map[string]*Vertex; Edges map[string]map[string]float32}`, JSON-compatible with `core/graph.Graph`). Consumers that need richer graph algorithms (SPT/MST) adapt it themselves — see `cli/service/service.go`'s `toModelGraph` for the canonical pattern.
- **Decay model**: edges are **additive** and carry their own TTL. Be mindful of the difference between `AddEdge` and `PutEdge` (idempotency) — see the discussion in [sdks/go/example/main.go](sdks/go/example/main.go).

## Build / Run / Test / Generate

```bash
go build -v ./...                # build (same as CI)
go test -v ./...                 # tests
go generate ./...                # regenerate wire_gen.go AND sdks/go/gen (zero-install)
make wire                        # alias: go tool wire ./server/cmd
make proto                       # alias: buf generate --clean (system `buf` if present, else `go run`)
go run ./server/cmd              # start the server (:6380)
go run ./cli                     # start the CLI
docker build -t lantern .        # container build (Go 1.26-alpine)
```

CI: [.github/workflows/go.yml](.github/workflows/go.yml) runs `go build` + `go test` on PR/push (Go 1.26, actions/checkout@v4, setup-go@v5). [.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml) publishes to ghcr.io on `v*.*.*` tag pushes with cosign keyless signing (cosign v2, `--yes`).

## Conventions and gotchas

- **Go version**: `go.mod` is on `1.26` and the Dockerfile uses `golang:1.26-alpine`. Bumping it requires updating all three places at once: `go.mod`, Dockerfile, and the `go-version` in `.github/workflows/go.yml`.
- **wire and generics**: as the `// Avoiding bug of 'wire'. Generic type is not supported.` comment in `service.go` notes, wire cannot handle generic type arguments, so the provider returns the concrete `GraphCache[string, *Vertex]`. Re-check this constraint before trying to introduce generics there.
- **Regenerating proto**: `go_package_prefix` in `buf.gen.yaml` is `github.com/anaregdesign/lantern/sdks/go/gen`, so the generated files land **inside the SDK module** at `sdks/go/gen/graph/v1`. `go generate ./...` (or `make proto`) runs `buf generate --clean` and rebuilds everything under `sdks/go/gen`. `buf.yaml` (v2 workspace) and `buf.gen.yaml` live at the repo root. `buf` does not need to be installed locally — the directive in [generate.go](generate.go) falls back to `go run github.com/bufbuild/buf/cmd/buf@v1.70.0`.
- **Multi-module layout**: the root module (`github.com/anaregdesign/lantern`) and the SDK module (`github.com/anaregdesign/lantern/sdks/go`) are stitched together for local dev via [go.work](go.work) and via a `replace` directive in the root `go.mod`. When adding a dep that should ship with the SDK, add it to `sdks/go/go.mod`; deps used only by `server/`, `cli/`, `core/`, or `tests/integration/` belong in the root `go.mod`. Run `go mod tidy` in **both** module roots after dependency changes.
- **Test gaps**: there are no tests for the server/service layer, wire wiring, or client transport paths. For non-trivial changes, **add at least a minimal table test in the same PR**.

## Docs / Links

- End-to-end usage: [README.md](README.md)
- Comprehensive client SDK example: [sdks/go/example/main.go](sdks/go/example/main.go)
