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

c, err := client.NewLantern("http://localhost:6380")
if err != nil {
    log.Fatal(err)
}
defer c.Close()

if err := c.PutVertex(ctx, "user:42", "alice", 10*time.Minute); err != nil {
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

`Illuminate` requires exactly one family option: `WithBFS`, `WithPPR`, or
`WithLocalCommunity`. A BFS also requires positive `BFSOpts.Step` and
`BFSOpts.FanOut`; a missing family or either zero dimension returns
`ErrInvalidArgument` before an RPC is sent.

## Transport

The SDK is Connect-only (Connect-Go over h2c by default, or HTTP/2 over
TLS). `NewLantern` requires a base URL with scheme:

- `http://host:port` — h2c, the Lantern primary listener default.
- `https://host[:port]` — TLS; supply a TLS-aware `*http.Client` via
  `WithHTTPClient`.

```go
import (
    "crypto/tls"
    "net/http"

    "golang.org/x/net/http2"
    client "github.com/anaregdesign/lantern/sdks/go"
)

cfg := &tls.Config{
    RootCAs:    pool,
    NextProtos: []string{"h2"},
}
hc := &http.Client{Transport: &http2.Transport{TLSClientConfig: cfg}}
c, _ := client.NewLantern("https://lantern.example.com:6380",
    client.WithHTTPClient(hc))
```

Bare `"host:port"` is rejected. A single `NewLantern` client speaks to one
URL. For load balancing across replicas you have two options:

- **Reverse proxy, k8s Service / Ingress, or DNS round-robin** in front of
  Lantern — the right choice when the replica set churns (pods come and go,
  endpoints are not stable). The HA runbook
  ([../../docs/ha-runbook.md](../../docs/ha-runbook.md)) walks through the
  canonical reverse-proxy fan-out pattern.
- **`NewLanternFailover`** (below) — opt-in client-side failover over a
  **fixed, known** set of endpoints, with no extra infrastructure.

### Static-endpoint failover

`NewLanternFailover` wraps a fixed list of endpoints and transparently
fails over to the next reachable one. It is the SDK-native generalisation
of the failover MCP used to embed privately; the policy now lives here.

```go
c, err := client.NewLanternFailover([]string{
    "http://lantern-0:6380",
    "http://lantern-1:6380",
    "http://lantern-2:6380",
}, client.WithDefaultTimeout(2*time.Second))
if err != nil {
    log.Fatal(err)
}
defer c.Close()

// Same surface as *Lantern — every call routes to the current node and
// rotates to another only when a node is unreachable.
if err := c.PutVertex(ctx, "user:42", "alice", 10*time.Minute); err != nil {
    log.Fatal(err)
}
```

Semantics:

- **Static endpoint set, no discovery.** The endpoint list is supplied once
  at construction and never changes. The SDK performs **no** dynamic
  discovery, gossip, or membership tracking — that explicit non-goal keeps
  the client a thin wrapper. If your endpoints churn, use a proxy / DNS
  instead (see above).
- **Sticky current node.** Calls go to the current node and stay there
  until it becomes unreachable; only then does the client rotate to the
  next node in the ring. Application-level errors (not-found, etc.) never
  trigger rotation.
- **`ErrUnavailable`.** Rotation is driven by the exported `ErrUnavailable`
  sentinel — a node is considered down when an RPC fails with Connect's
  `CodeUnavailable` (connection refused or server-side unavailable), which
  the SDK joins with `ErrUnavailable`. `errors.Is(err, client.ErrUnavailable)`
  is true for those failures. When **all** nodes are unreachable the last
  such error is returned.
- **Same API surface.** `*Failover` exposes the same read/write/delete/scan
  methods plus `Ping` and `Close`, so it is a drop-in for `*Lantern`.

> The Node SDK (`sdks/node`) has **no** failover counterpart yet — it speaks
> to a single endpoint via `connect` / `connectWeb`. Front it with a proxy /
> DNS for HA.

### Options

| Option | Purpose |
| --- | --- |
| `WithHTTPClient(*http.Client)` | Override the default h2c client (for TLS, custom timeouts, OpenTelemetry transport wrapping, etc.). |
| `WithConnectClientOption(...connect.ClientOption)` | Escape hatch for Connect-Go client options — interceptors, codec selection, compression, `connect.WithGRPC()`, `otelconnect.NewInterceptor()`. |
| `WithDefaultTimeout(time.Duration)` | Per-call timeout applied when the caller's context has no deadline. `0` (default) disables. |
| `WithBatchChunkSize(int)` | Override the chunk size used by `PutVertices`, `AddEdges`, `PutEdges`, `DeleteVertices`, `DeleteEdges`, `GetVertices`, `GetEdges`. Default `1000`. |

Compression example:

```go
import "connectrpc.com/connect"

c, _ := client.NewLantern("http://lantern.svc:6380",
    client.WithConnectClientOption(connect.WithSendCompression("gzip")))
```

### Health check

`(*Lantern).Ping` POSTs a `Connect+JSON` `grpc.health.v1.Health/Check`
against the same base URL the client was built with. The Lantern server
mounts the gRPC-Health-v1 surface via `connectrpc.com/grpchealth` on
the primary listener (no separate metrics port required).

## Model definition policy ("no parallel models")

Wherever a protobuf message already expresses a domain concept, the SDK
re-exports the `pb` type directly via a Go **alias** rather than introducing
a parallel struct:

```go
type Vertex    = pb.Vertex
type Edge      = pb.Edge
type Algorithm = pb.Algorithm
type Objective = pb.Objective
type Weighting = pb.Weighting
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
| Write  | `PutVertex` / `PutVertexAt` / `PutVertexIfAbsent` / `PutVertexIfAbsentAt` / `AddEdge` / `AddEdgeAt` / `PutEdge` / `PutEdgeAt` | `PutVertices` / `PutVerticesIfAbsent` / `AddEdges` / `PutEdges` |
| Delete | `DeleteVertex` / `DeleteEdge` | `DeleteVertices` / `DeleteEdges` |
| Scan   | — | `ScanVertices`, `ScanVerticesAll`, `ScanVertexKeys`, `ScanVertexKeysAll`, `ScanEdges`, `ScanEdgesAll`, `CountVerticesByPrefix`, `DeleteVerticesByPrefix`, `DeleteEdgesByPrefix` |
| Graph  | `Illuminate` | — |
| Replication | `Subscribe` (server-stream iter.Seq2) | — |
| Status | `Ping`, `GetServerStatus`, `GetReplicationStatus` | — |
| Backup | — | `Backup` / `Restore` |

`AddEdge` is **additive** (multiple calls add weight, each contribution
carries its own TTL); `PutEdge` is **idempotent replace** (single weight,
single TTL). See the in-line discussion in `example/main.go` for the
semantic difference.

`Illuminate` accepts at most one traversal family option (`WithBFS`,
`WithPPR`, or `WithLocalCommunity`). Combining them returns a local error that
matches both `ErrInvalidArgument` and `ErrConflictingIlluminateFamilies` before
any RPC is sent; the same contract applies to `NewLanternFailover`. Shared
axes (`WithWeighting`, `WithVertexPrefix`) remain composable.

`PutVertexIfAbsent` / `PutVerticesIfAbsent` are conditional writes (SET NX,
#896): each applies only when no **live** vertex already exists at the key,
closing the check-then-act race of a `GetVertex` → `PutVertex` sequence. The
singular returns a `bool` (`true` when the write landed); the plural returns
`(written int, skipped []string)`. An expired-but-uncollected vertex does not
block the write.

`ScanVertices` / `ScanVertexKeys` iterate a prefix ascending by default;
`WithScanOrder(ScanOrderDesc)` walks it descending (#898). The order is bound
into the returned cursor, so replaying a cursor under the opposite order is
rejected with `InvalidArgument` — always carry the same `WithScanOrder` value
through a paginated loop. `ScanVerticesAll` / `ScanVertexKeysAll` accept the
same scan options after `batchSize`, e.g.
`ScanVerticesAll(ctx, "user:", 100, client.WithScanOrder(client.ScanOrderDesc))`.

`Backup` streams a whole-graph, point-in-time dump (`FormatProto` by default,
or human-readable `FormatNDJSON`; optionally scoped to a key prefix via
`WithBackupPrefix`); `Restore` replays it through chunked `PutVertices` /
`PutEdges`. Both work against a single node (no replication gate) — they back
the `lantern-cli dump` / `restore` commands.

## Versioning

Tagged independently as `sdks/go/vX.Y.Z`. Depends on `pb/vX.Y.Z` of the same
release cycle — bumping the SDK without bumping `pb` is allowed (additive
changes); bumping `pb` requires re-tagging the SDK so the published module
points at the new `pb` tag.

## Authentication

TLS / mTLS is plumbed through `WithHTTPClient` — see the [Transport](#transport)
section. Per-RPC authentication (bearer tokens, OAuth) is supplied by
wrapping the `http.Client`'s `Transport` with a custom `http.RoundTripper`
that injects the header. See [`cli/cmd/root.go`](../../cli/cmd/root.go) for
the canonical TLS-aware `*http.Client` construction.
