# Lantern — Agent Instructions

Lantern is an in-memory `key-vertex-store` (graph-based KVS). It runs as a gRPC server, with both vertices and edges carrying TTLs so they decay over time. See [README.md](README.md) for the full overview.

## Monorepo layout

This project used to be split across four separate repositories (`lantern` / `lantern-proto` / `lantern-cli`, plus a shared graph/cache/NLP toolkit), but **everything is now consolidated into this repo**.

| Path | Origin | Role |
|---|---|---|
| `server/` | lantern (this repo) | gRPC server (DI via google/wire) |
| `client/` | lantern (this repo) | Go client SDK |
| `cli/` | former [`lantern-cli`](https://github.com/anaregdesign/lantern-cli) | Interactive CLI (cobra + promptui) |
| `proto/` | former [`lantern-proto`](https://github.com/anaregdesign/lantern-proto) | `.proto` sources (regenerated with buf) |
| `gen/go/` | former `lantern-proto/go` | Generated Go bindings |
| `core/` | shared toolkit (was a separate repo) | Common building blocks reused by server & client: graph, cache, collections, concurrency, NLP |

The module path in `go.mod` is still `github.com/anaregdesign/lantern`. All external dependencies on the old repos have been removed.

## Architecture notes

- **gRPC service**: [server/service/service.go](server/service/service.go) implements `LanternService` (`Illuminate`, `GetVertex`, `PutVertex`, `AddEdge`, `PutEdge`, `DeleteVertex`, `DeleteEdge`).
- **DI**: [google/wire](https://github.com/google/wire). [server/cmd/wire.go](server/cmd/wire.go) holds the definitions; [server/cmd/wire_gen.go](server/cmd/wire_gen.go) is generated — **never edit it by hand**. After changing providers, regenerate with `make wire` (or `wire ./server/cmd`).
- **Providers**: [server/provider/provider.go](server/provider/provider.go) assembles `Config` (env vars `LANTERN_PORT`, `LANTERN_DEFAULT_TTL_SECONDS`), `net.Listener`, `grpc.Server`, and `core/cache/graph.GraphCache`. `NewListener` now receives the wire-injected `*Config`.
- **Client SDK**: [client/client.go](client/client.go) is a thin gRPC wrapper. [client/value.go](client/value.go) handles Go-native ↔ `pb.Vertex` conversion via `nativeVertex.asVertex()` and `Vertex.*Value()`. **When adding a new value type, update both directions** (`asVertex` and each `*Value()` method).
- **Decay model**: edges are **additive** and carry their own TTL. Be mindful of the difference between `AddEdge` and `PutEdge` (idempotency) — see the discussion in [client/example/main.go](client/example/main.go).

## Build / Run / Test / Generate

```bash
go build -v ./...                # build (same as CI)
go test -v ./...                 # tests
make wire                        # regenerate wire code (requires: go install github.com/google/wire/cmd/wire@latest)
make proto                       # regenerate Go code from proto (requires buf)
go run ./server/cmd              # start the server (:6380)
go run ./cli                     # start the CLI
docker build -t lantern .        # container build (Go 1.26-alpine)
```

CI: [.github/workflows/go.yml](.github/workflows/go.yml) runs `go build` + `go test` on PR/push (Go 1.26, actions/checkout@v4, setup-go@v5). [.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml) publishes to ghcr.io on `v*.*.*` tag pushes with cosign keyless signing (cosign v2, `--yes`).

## Conventions and gotchas

- **Go version**: `go.mod` is on `1.26` and the Dockerfile uses `golang:1.26-alpine`. Bumping it requires updating all three places at once: `go.mod`, Dockerfile, and the `go-version` in `.github/workflows/go.yml`.
- **wire and generics**: as the `// Avoiding bug of 'wire'. Generic type is not supported.` comment in `service.go` notes, wire cannot handle generic type arguments, so the provider returns the concrete `GraphCache[string, *Vertex]`. Re-check this constraint before trying to introduce generics there.
- **Regenerating proto**: `option go_package` in `proto/graph/v1/graph.proto` is `github.com/anaregdesign/lantern/gen/go/graph/v1`. `make proto` invokes `buf generate proto` and rebuilds everything under `gen/go`. `buf.work.yaml` and `buf.gen.yaml` live at the repo root.
- **Test gaps**: there are no tests for the server/service layer, wire wiring, or client transport paths. For non-trivial changes, **add at least a minimal table test in the same PR**.

## Docs / Links

- End-to-end usage: [README.md](README.md)
- Comprehensive client SDK example: [client/example/main.go](client/example/main.go)
