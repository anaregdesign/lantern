# Lantern — Agent Instructions

Lantern is an in-memory `key-vertex-store` (graph-based KVS). It runs as a gRPC server, with both vertices and edges carrying TTLs so they decay over time. See [README.md](README.md) for the full overview.

## Monorepo layout

This project used to be split across four separate repositories (`lantern` / `lantern-proto` / `lantern-cli`, plus a shared graph/cache/NLP toolkit), but **everything is now consolidated into this repo**.

| Path | Origin | Role |
|---|---|---|
| `server/` | lantern (this repo) | gRPC server (DI via google/wire) |
| `sdks/go/` | lantern (this repo, formerly `client/`) | Go client SDK |
| `cli/` | former [`lantern-cli`](https://github.com/anaregdesign/lantern-cli) | Interactive CLI (cobra + promptui) |
| `proto/` | former [`lantern-proto`](https://github.com/anaregdesign/lantern-proto) | `.proto` sources (regenerated with buf) |
| `gen/go/` | former `lantern-proto/go` | Generated Go bindings |
| `core/` | shared toolkit (was a separate repo) | Common building blocks reused by server & client: graph, cache, collections, concurrency, NLP |

The module path in `go.mod` is still `github.com/anaregdesign/lantern`. All external dependencies on the old repos have been removed.

## Architecture notes

- **gRPC service**: [server/service/service.go](server/service/service.go) implements `LanternService` (`Illuminate`, `GetVertex`, `PutVertex`, `DeleteVertex`, `DeleteVertices`, `GetEdge`, `AddEdge`, `PutEdge`, `DeleteEdge`, `DeleteEdges`).
- **DI**: [google/wire](https://github.com/google/wire). [server/cmd/wire.go](server/cmd/wire.go) holds the definitions; [server/cmd/wire_gen.go](server/cmd/wire_gen.go) is generated — **never edit it by hand**. After changing providers, regenerate with `go generate ./...` (or `make wire` for just the wire step). Wire itself is pulled in via the `tool` directive in `go.mod`, so no install is required.
- **Providers**: [server/provider/provider.go](server/provider/provider.go) assembles `Config` (env vars `LANTERN_PORT`, `LANTERN_DEFAULT_TTL_SECONDS`), `net.Listener`, `grpc.Server`, and `core/cache/graph.GraphCache`. `NewListener` now receives the wire-injected `*Config`.
- **Client SDK**: [sdks/go/client.go](sdks/go/client.go) is a thin gRPC wrapper. [sdks/go/value.go](sdks/go/value.go) handles Go-native ↔ `pb.Vertex` conversion via `nativeVertex.asVertex()` and `Vertex.*Value()`, and renders Go-friendly JSON via `Vertex.MarshalJSON` (shape: `{key,type,value,expiration}`). **When adding a new value type, update all three:** `asVertex` (Go → proto), the matching `*Value()` accessor plus `Kind()` / `VertexKind*` constant (proto → Go), and the `MarshalJSON` switch. The import path is `github.com/anaregdesign/lantern/sdks/go` and the package name remains `client`.
- **SDK dependency boundary**: the Go SDK depends only on `gen/go/graph/v1` and gRPC — **never import `core/...` from `sdks/go/`**. `Illuminate*` returns the SDK-local `client.Graph` (field shape `{Vertices map[string]*Vertex; Edges map[string]map[string]float32}`, JSON-compatible with `core/graph.Graph`). Consumers that need richer graph algorithms (SPT/MST) adapt it themselves — see `cli/service/service.go`'s `toModelGraph` for the canonical pattern.
- **Decay model**: edges are **additive** and carry their own TTL. Be mindful of the difference between `AddEdge` and `PutEdge` (idempotency) — see the discussion in [sdks/go/example/main.go](sdks/go/example/main.go).

## Build / Run / Test / Generate

```bash
go build -v ./...                # build (same as CI)
go test -v ./...                 # tests
go generate ./...                # regenerate wire_gen.go AND gen/go (zero-install)
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
- **Regenerating proto**: `option go_package` in `proto/graph/v1/graph.proto` is `github.com/anaregdesign/lantern/gen/go/graph/v1`. `go generate ./...` (or `make proto`) runs `buf generate --clean` and rebuilds everything under `gen/go`. `buf.yaml` (v2 workspace) and `buf.gen.yaml` live at the repo root. `buf` does not need to be installed locally — the directive in [generate.go](generate.go) falls back to `go run github.com/bufbuild/buf/cmd/buf@v1.70.0`.
- **Test gaps**: there are no tests for the server/service layer, wire wiring, or client transport paths. For non-trivial changes, **add at least a minimal table test in the same PR**.

## Docs / Links

- End-to-end usage: [README.md](README.md)
- Comprehensive client SDK example: [sdks/go/example/main.go](sdks/go/example/main.go)
