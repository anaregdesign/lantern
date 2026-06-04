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

## Connection topologies

All three constructors enable the same default retry policy
(`UNAVAILABLE` + `RESOURCE_EXHAUSTED` retried with capped exponential
backoff) and client-side `round_robin` load balancing; only the resolver
changes.

| Constructor | When to use |
| --- | --- |
| `NewLantern("host:port")` | Single endpoint — PaaS (Cloud Run, ACA, App Service single instance) or any case where you have one stable URL. |
| `NewLantern("dns:///service.ns.svc:6380")` | DNS fan-out — k8s ClusterIP/headless Services, Compose service names, any DNS source resolving one host to N backends. gRPC re-resolves on connection failure. |
| `NewLanternWithEndpoints([]string{...})` | Explicit static list — bare hosts, env-supplied pod IPs, any non-DNS discovery source. Uses gRPC's manual resolver under the hood. |

All three transparently failover: any backend returning `codes.Unavailable`
is retried on the next healthy address, and the server-side readiness gate
(`/healthz/ready`) keeps draining nodes out of rotation until they catch up
on replication.

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

TLS / mTLS via `WithTLS`, `WithMTLS`, or `WithSystemRootsTLS` options on the
constructors. Per-RPC authentication (bearer tokens, OAuth) is not built in —
wrap the dialer with your own
`grpc.DialOption(grpc.WithPerRPCCredentials(...))` via `WithDialOptions(...)`.
