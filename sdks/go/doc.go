// Package client is the Connect-only Go SDK for the Lantern graph
// service.
//
// # Quick start
//
//	c, err := client.NewLantern("http://localhost:6380")
//	if err != nil { log.Fatal(err) }
//	defer c.Close()
//
//	c.PutVertex(ctx, "user:42", "alice", time.Minute)
//	v, _ := c.GetVertex(ctx, "user:42")
//	fmt.Println(client.StringValue(v))
//
// See example/main.go for a comprehensive end-to-end walkthrough.
//
// # baseURL
//
// NewLantern requires a base URL with scheme:
//
//   - "http://host:port"  — h2c (plaintext HTTP/2), the Lantern primary
//     listener default.
//   - "https://host[:port]" — TLS-backed HTTP/2; supply a TLS-aware
//     *http.Client via WithHTTPClient.
//
// Bare "host:port" forms are rejected. For load balancing across
// multiple replicas use a reverse proxy or DNS round-robin in front
// of Lantern — the SDK speaks to one URL.
//
// # TTL and expiration
//
// Decay is opt-in. The relative-TTL convenience methods (PutVertex,
// AddEdge, PutEdge) treat a non-positive ttl as "no expiration": a ttl
// of 0 (or any negative duration) stores the vertex/edge permanently,
// and the value never decays. Pass a positive ttl to opt into decay,
// or use the absolute *At variants (PutVertexAt, AddEdgeAt, PutEdgeAt)
// with a zero time.Time for the same permanent semantics. The SDK never
// injects a hidden default expiration — an omitted/zero TTL is honoured
// as permanent end to end (see #523).
//
// # Illuminate family selection
//
// Illuminate accepts zero or one traversal family option: WithBFS, WithPPR,
// or WithLocalCommunity. Passing more than one returns a local error matching
// both ErrInvalidArgument and ErrConflictingIlluminateFamilies before any
// endpoint is contacted. The same rule applies through NewLanternFailover.
// Shared axes such as WithWeighting and WithVertexPrefix remain freely
// composable with the one family option.
//
// # Content search
//
// SearchVertices, SearchVerticesPage, SearchVerticesIter, and
// NewIncrementalSearch share the canonical projection, Unicode analysis,
// membership, ranking, TTL, typed-error, cursor, and HA contract documented at
// https://github.com/anaregdesign/lantern/blob/main/docs/search.md. The
// compiling example/search.go demonstrates capability discovery, one-shot and
// incremental search, phrase/typo options, bounded pagination, cancellation,
// and a disabled endpoint.
//
// # Retry policy
//
// Retries are opt-in and off by default — a zero-config client behaves
// exactly as before. WithRetry(RetryPolicy{...}) arms a bounded,
// context-aware backoff loop with full-jitter exponential delays, applied
// ONLY to RPCs that are idempotent under the client's configuration. The
// eligibility matrix is enforced in code (see retry.go), not documentation:
//
//	RPC family                                   Retried?
//	-------------------------------------------  ------------------------------
//	Get*/Scan*/Count*/Search*/Illuminate/status  yes (reads are idempotent)
//	Put*/Delete*/DeleteVerticesByPrefix          yes (idempotent by semantics)
//	AddEdge/AddEdgeAt/AddEdges                    only under WithIdempotentAdds
//	                                             (or explicit ContribIDs): the
//	                                             per-edge keys let a retry record
//	                                             each contribution exactly once
//	Subscribe/Backup/Restore                     no (streaming / io, excluded v1)
//
// Never retried regardless of policy: deterministic outcomes (NotFound,
// InvalidArgument, FailedPrecondition) and DeadlineExceeded/Canceled (the
// caller's budget is already spent). The default retryable code is
// Unavailable; add ResourceExhausted via RetryPolicy.RetryableCodes to retry
// through the server-side capacity cap / rate limiter.
//
// Under NewLanternFailover the policy drives the ring walk: each retry
// attempt re-runs the failover rotation, so a persistently-unavailable
// endpoint is retried against its siblings with backoff and MaxAttempts is
// the cross-replica budget — there is no second rotation mechanism. The
// per-endpoint clients' own retry is neutralised so the attempt budget is
// never squared.
//
// # Model definition policy
//
// This SDK follows a strict "no parallel models" rule: wherever a
// protobuf message already expresses a domain concept, the SDK
// re-exports the pb type directly via a Go alias rather than
// introducing a parallel struct:
//
//   - Vertex       = pb.Vertex       (see value.go)
//   - Edge         = pb.Edge         (see client.go)
//   - Reduction    = pb.Reduction    (see client.go)
//   - Objective    = pb.Objective    (see client.go)
//   - Weighting    = pb.Weighting    (see client.go)
//
// Because these are true aliases (declared with `type X = pb.X`, not
// `type X pb.X`), client.Vertex and pb.Vertex are the same type and
// require no conversion at the boundary. Go-friendly accessors that
// would normally be methods are exposed as free functions in this
// package (Kind, IntValue, StringValue, ExpirationTime,
// MarshalVertexJSON, EdgeExpiration, …), because methods cannot be
// attached to aliases of types declared in another package.
//
// A few SDK-only types remain and are intentional, not redundant:
//
//   - VertexInput / EdgeInput
//     Ergonomic input shapes that accept Go-native values (any,
//     time.Time, time.Duration) and let the SDK pick the correct
//     pb.Vertex_* oneof variant or build a timestamppb.Timestamp. They
//     exist to spare callers from constructing protobuf oneof wrappers
//     by hand.
//
//   - EdgeRef
//     A small comparable struct (Tail, Head string) used in batch
//     results. pb.EdgeKey is not used here because it embeds
//     protoimpl state, which makes it unsafe to compare with `==` in
//     user code or to place in a map key.
//
//   - Graph
//     A keyed/adjacency-map shape (map[string]*Vertex,
//     map[string]map[string]float32) that is more convenient for
//     client-side traversal than pb.Graph's flat slice shape. This is
//     a deliberate shape transformation, not a duplicate data
//     definition.
//
// See https://github.com/anaregdesign/lantern/issues/106 for the
// design discussion.
package client
