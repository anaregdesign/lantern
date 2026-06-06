# sdks/go — Lantern Go client SDK

The official Go client for the
[Lantern](https://github.com/anaregdesign/lantern) graph KVS.

```bash
go get github.com/anaregdesign/lantern/sdks/go@latest
```

```go
import client "github.com/anaregdesign/lantern/sdks/go"
```

The package name is `client`, so once imported you call into it as
`client.NewLantern(...)`, `client.Kind(v)`, etc.

## Quick start

```go
ctx := context.Background()

c, err := client.NewLantern("localhost:6380")
if err != nil {
    log.Fatal(err)
}
defer c.Close()

if err := c.PutVertex(ctx, client.VertexInput{
    Key:   "user:42",
    Value: "alice",
    TTL:   10 * time.Minute,
}); err != nil {
    log.Fatal(err)
}

v, err := c.GetVertex(ctx, "user:42")
if err != nil {
    log.Fatal(err)
}
fmt.Println(client.StringValue(v)) // alice
```

See [`example/main.go`](example/main.go) for a comprehensive end-to-end
walkthrough covering vertices, edges (`AddEdge` vs `PutEdge` semantics),
`Illuminate`, prefix scans, batches, and TLS.

## Connect-only (since #367)

As of #367 the SDK is **Connect-only**. The legacy gRPC dial path and
its options have been removed; both `NewLantern` and `NewLanternConnect`
return the same `*Lantern` and share one `Option` type. Existing call
sites that pass a bare `"host:port"` keep compiling — `NewLantern` now
just normalises the input by prepending `http://` and delegates to
`NewLanternConnect`.

```go
// Equivalent — pick whichever reads better at your call site.
c1, _ := client.NewLantern("lantern.svc:6380")
c2, _ := client.NewLanternConnect("http://lantern.svc:6380")
```

Differences from the pre-#367 surface:

- **baseURL may include a scheme.** Bare `"host:port"` is upgraded to
  `http://host:port` (h2c). Use `https://...` explicitly with a TLS-aware
  `http.Client` via `WithHTTPClient` for TLS.
- **gRPC dial options are gone.** Pass a configured `*http.Client` via
  `WithHTTPClient`; forward Connect-Go client options via
  `WithConnectClientOption(connect.With...())`. Compression: use
  `client.WithConnectClientOption(connect.WithSendCompression("gzip"))`.
- **Health check** now POSTs to the same Connect base URL at
  `/grpc.health.v1.Health/Check` (mounted by the server's
  connectrpc.com/grpchealth handler). Continue calling `c.Ping(ctx)`.
- **Load balancing** moves out of the SDK. The pre-#367
  `NewLanternWithEndpoints([]string{...})` LB constructor is gone; use
  DNS (`dns:///service.ns.svc:6380` style works via the operating system
  resolver), a k8s Service / Ingress, or a reverse proxy / sidecar in
  front of Lantern. The HA runbook ([../../docs/ha-runbook.md](../../docs/ha-runbook.md))
  walks through the canonical reverse-proxy fan-out pattern.

### v0.x → v1.0 option migration

| v0.x option | Replacement |
| --- | --- |
| `WithTransportCredentials(creds)` | `WithHTTPClient(&http.Client{Transport: &http2.Transport{TLSClientConfig: cfg}})` |
| `WithDialOption(...)` | `WithConnectClientOption(...)` for Connect equivalents; per-RPC auth via a custom `http.RoundTripper` on the supplied `http.Client`. |
| `WithCompression("gzip")` | `WithConnectClientOption(connect.WithSendCompression("gzip"))` |
| `WithKeepaliveParams(...)` | Tune the `http2.Transport` on the supplied `http.Client` (`ReadIdleTimeout`, `PingTimeout`). |
| `WithDefaultServiceConfig(...)` | Wrap the `http.Client.Transport` with your own retry middleware; the SDK no longer ships a built-in retry policy. |
| `WithOpenTelemetry()` | Wrap the `http.Client.Transport` with `otelhttp.NewTransport(...)`. |

## Model definition policy ("no parallel models")

Wherever a protobuf message already expresses a domain concept, the SDK
re-exports the `pb` type directly via a Go **alias** rather than introducing
a parallel struct:

```go
type Vertex       = pb.Vertex
type Edge         = pb.Edge
type Optimization = pb.Optimization
```

Because these are *true* aliases (`type X = pb.X`, not `type X pb.X`),
`client.Vertex` and `pb.Vertex` are the **same type** — there is no
conversion at the boundary. Go-friendly accessors that would normally be
methods are exposed as **free functions** in this package, because methods
cannot be attached to aliases of types declared in another package:

```go
client.Kind(v)            // pb.VertexKind
client.IntValue(v)        // int64, ok
client.StringValue(v)     // string, ok
client.BoolValue(v)       // bool, ok
client.BytesValue(v)      // []byte, ok
client.TimeValue(v)       // time.Time, ok
client.DurationValue(v)   // time.Duration, ok
client.FloatValue(v)      // float64, ok
client.UIntValue(v)       // uint64, ok
client.IsNil(v)           // bool
client.VertexExpiration(v) // time.Time, ok
client.EdgeExpiration(e)   // time.Time, ok
client.MarshalVertexJSON(v) // []byte, error  -- note: NOT a json.Marshaler;
                            //                   callers must invoke it explicitly.
```

A few SDK-only types remain and are intentional, not redundant:

- **`VertexInput` / `EdgeInput`** — ergonomic input shapes that accept Go-native
  values (`any`, `time.Time`, `time.Duration`) and let the SDK pick the correct
  `pb.Vertex_*` oneof variant or build a `timestamppb.Timestamp`. They exist to
  spare callers from constructing protobuf oneof wrappers by hand.
- **`EdgeRef`** — a small comparable struct (`Tail`, `Head string`) used in
  batch results. `pb.EdgeKey` is not used here because it embeds `protoimpl`
  state, which makes it unsafe to compare with `==` in user code or to place
  in a map key.
- **`Graph`** — a keyed/adjacency-map shape
  (`map[string]*Vertex`, `map[string]map[string]float32`) that is more
  convenient for client-side traversal than `pb.Graph`'s flat slice shape.
  This is a deliberate shape transformation, not a duplicate data definition.

See [issue #106](https://github.com/anaregdesign/lantern/issues/106) for the
design discussion.

## API surface

| Category | Singular | Plural |
| --- | --- | --- |
| Read   | `GetVertex` / `GetEdge` | `GetVertices` / `GetEdges` |
| Write  | `PutVertex` / `AddEdge` / `PutEdge` | `PutVertices` / `AddEdges` / `PutEdges` |
| Delete | `DeleteVertex` / `DeleteEdge` | `DeleteVertices` / `DeleteEdges` |
| Scan   | — | `ScanByPrefix`, `ScanEdgesByTailPrefix`, `ScanEdgesByHeadPrefix`, `DeleteByPrefix` |
| Graph  | — | `Illuminate` |
| Batches | — | `Batch(...).Do(ctx)` builder for mixed ops in one round trip |
| Health | `Check`, `Watch` (grpc health/v1) | — |

`AddEdge` is **additive** (multiple calls add weight, each contribution carries
its own TTL); `PutEdge` is **idempotent replace** (single weight, single TTL).
See the in-line discussion in `example/main.go` for the semantic
difference.

## Versioning

Tagged independently as `sdks/go/vX.Y.Z`. Depends on `pb/vX.Y.Z` of the same
release cycle — bumping the SDK without bumping `pb` is allowed (additive
changes); bumping `pb` requires re-tagging the SDK so the published module
points at the new `pb` tag.

## Authentication

TLS / mTLS: pass a TLS-aware `*http.Client` via `WithHTTPClient` — wrap
`http2.Transport{TLSClientConfig: cfg}` around a `tls.Config` carrying your
roots and any client certs:

```go
cfg := &tls.Config{
    RootCAs:      pool,
    Certificates: []tls.Certificate{cert}, // mTLS
    NextProtos:   []string{"h2"},
}
hc := &http.Client{Transport: &http2.Transport{TLSClientConfig: cfg}}
c, _ := client.NewLantern("https://lantern.example.com:6380",
    client.WithHTTPClient(hc))
```

Per-RPC authentication (bearer tokens, OAuth): wrap the `http.Client`'s
`Transport` with a custom `http.RoundTripper` that injects the header — the
SDK no longer hosts gRPC `PerRPCCredentials`. See
[`cli/cmd/root.go`](../../cli/cmd/root.go) for the canonical TLS-aware
`*http.Client` construction.
