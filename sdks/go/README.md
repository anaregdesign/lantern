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

outcome, err := c.PutVertex(ctx, "user:42", "alice", 10*time.Minute)
if err != nil {
    log.Fatal(err)
}
if outcome != client.PutOutcomeAppliedAndLive {
    log.Fatalf("user:42 was not live after Put: %s", outcome)
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
if _, err := c.PutVertex(ctx, "user:42", "alice", 10*time.Minute); err != nil {
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
type Reduction = pb.Reduction
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
- **`ChangeOrigin` / `ChangeCursor`** — comparable SDK-local cursor keys for
  Subscribe's fixed 16-byte mutation origins. `ChangeEvent` remains a true
  alias of the generated `pb.Mutation`; the SDK therefore stays `pb`-only
  without creating a parallel mutation model or importing `core/hlc`.

See [issue #106](https://github.com/anaregdesign/lantern/issues/106) for the
design discussion.

## API surface

| Category | Singular | Plural |
| --- | --- | --- |
| Read   | `GetVertex` / `GetEdge` | `GetVertices` / `GetEdges` |
| Write  | `PutVertex` / `PutVertexAt` / `PutVertexIfAbsent` / `PutVertexIfAbsentAt` / `AddEdge` / `AddEdgeAt` / `PutEdge` / `PutEdgeAt` | `PutVertices` / `PutVerticesIfAbsent` / `AddEdges` / `PutEdges` |
| Delete | `DeleteVertex` / `DeleteEdge` | `DeleteVertices` / `DeleteEdges` |
| Scan   | — | `ScanVertices`, `ScanVerticesAll`, `ScanVertexKeys`, `ScanVertexKeysAll`, `ScanEdges`, `ScanEdgesAll`, `CountVerticesByPrefix`, `DeleteVerticesByPrefix`, `DeleteEdgesByPrefix` |
| Search | `SearchVertices`, `SearchVerticesPage` | `SearchVerticesIter` |
| Graph  | `Illuminate` | — |
| Replication | `Subscribe` (server-stream iter.Seq2) | — |
| Status | `Ping`, `GetServerStatus`, `GetReplicationStatus` | — |
| Backup | — | `Backup` / `Restore` |

`Subscribe` accepts a `ChangeCursor` whose values are the next sequence
expected per origin and yields `*ChangeEvent` mutations directly. Use
`ChangeOriginFromBytes(event.GetOrigin())` to obtain the comparable cursor key;
`ChangeOrigin.String()` returns the canonical 32-character hex form used on
the wire and in diagnostics.

`AddEdge` is **additive** (multiple calls add weight, each contribution
carries its own TTL); `PutEdge` is **idempotent replace** (single weight,
single TTL). See the in-line discussion in `example/main.go` for the
semantic difference.

Every `PutVertex*` / `PutEdge*` singular call returns a typed `PutOutcome`.
Plural Put calls return request-index-aligned `[]VertexPutResult` /
`[]EdgePutResult`, including duplicate identities. The bounded outcomes are
`PutOutcomeAppliedAndLive`, `PutOutcomeExpired`,
`PutOutcomeConditionNotMet`, and `PutOutcomeSuperseded`; an unspecified,
unknown, or misaligned wire response is an error. Liveness is decided by the
server clock at application time. In particular, an unconditional
born-expired Put returns `PutOutcomeExpired` and removes a prior live value.
On return, the exact absolute expiration sent by the SDK is also a local upper
bound: `PutOutcomeAppliedAndLive` is conservatively downgraded to
`PutOutcomeExpired` if that instant has already passed locally. Other bounded
outcomes are never reclassified, so condition and causality diagnostics remain
visible even with a fast client clock or delayed response.

Search capability failures stay machine-readable: a disabled index matches
`ErrSearchDisabled`, while a phrase request without positional postings
matches `ErrSearchPositionsDisabled`; both also match
`ErrFailedPrecondition`. `SearchFailureReason` returns the bounded wire reason,
and `ServerStatus.GetSearch()` exposes positions, defaults, limits,
implementation versions, and the HA configuration fingerprint.
Execution containment failures remain typed: work-cap exhaustion matches
`ErrSearchWorkBudget` and exposes its stable counter through
`SearchFailureWorkKind`, while in-flight saturation matches
`ErrSearchAdmission`; both also match `ErrResourceExhausted`. Cancellation,
deadline, and work-budget failures are terminal for that attempt. Retry an
unchanged query only for admission saturation (with jittered backoff), and let
incremental UI drivers issue only their newest input.
Leaving `WithMatchMode` unset always preserves the server's configured match
mode, including when `WithFuzziness` or `WithPrefixTerms` is present.
`WithMatchMode(MatchMinShould)` plus `WithMinShouldMatch(0)` selects the
server's threshold. `ValidateSearchOptions` and `SearchVertices` reject
non-zero thresholds under another mode, fuzziness above 2, and phrase combined
with an explicit mode/fuzziness/prefix terms as `ErrInvalidArgument` before an
RPC. `WithIncrementalSearchOptions` forwards the complete one-shot option set;
the package-level `NewIncrementalSearch` accepts the narrow `Searcher`
interface implemented by both `Lantern` and `Failover`.

`SearchVerticesPage` returns one immutable, endpoint-sticky ranked snapshot
page with `NextCursor`, `EffectiveLimit`, `Truncated`, and
`ContinuationLimited`. Repeat every query option and projection with
`WithSearchCursor`; a mismatch/tamper matches `ErrSearchCursorInvalid`, while an
expired or evicted session matches `ErrSearchCursorStale`. Both are terminal
for that chain—restart from page one explicitly. `SearchVerticesIter` follows
pages lazily and never accumulates an unbounded slice; if the server could
retain only a bounded prefix it yields `ErrSearchContinuationLimited` after the
last retained hit.

The default projection is lightweight key+score. `WithFullVertex()` includes
the exact value/TTL snapshot selected with ranking and removes a racy follow-up
`GetVertices` call. Static `Failover` may rotate for page one, but a non-empty
cursor is sent only to the current endpoint because sessions are not portable
between replicas.

The [canonical SearchVertices contract](../../docs/search.md) is authoritative
for document projection, Unicode analysis, ranking, TTL consistency, budgets,
typed reasons, and HA. The maintained
[`example/search.go`](example/search.go) compiles one-shot, capability,
phrase/typo, pagination, disabled, cancellation, and incremental flows in CI.

`Illuminate` accepts at most one traversal family option (`WithBFS`,
`WithPPR`, or `WithLocalCommunity`). Combining them returns a local error that
matches both `ErrInvalidArgument` and `ErrConflictingIlluminateFamilies` before
any RPC is sent; the same contract applies to `NewLanternFailover`. Shared
axes (`WithWeighting`, `WithVertexPrefix`) remain composable.

`PutVertexIfAbsent` / `PutVerticesIfAbsent` are conditional writes (SET NX,
#896): each applies only when no **live** vertex already exists at the key,
closing the check-then-act race of a `GetVertex` → `PutVertex` sequence. The
singular returns `PutOutcomeAppliedAndLive` or the reason it did not apply;
the plural returns one `VertexPutResult` per request index. An existing live
vertex wins condition precedence and returns `PutOutcomeConditionNotMet` even
when the candidate is born expired. An expired-but-uncollected vertex does not
block the write. Automatic transport retry/failover is disabled for these
conditional calls: if the first response is lost, replay would observe the
newly-created value and change `AppliedAndLive` into `ConditionNotMet`.

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
