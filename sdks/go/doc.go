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
// # Model definition policy
//
// This SDK follows a strict "no parallel models" rule: wherever a
// protobuf message already expresses a domain concept, the SDK
// re-exports the pb type directly via a Go alias rather than
// introducing a parallel struct:
//
//   - Vertex       = pb.Vertex       (see value.go)
//   - Edge         = pb.Edge         (see client.go)
//   - Algorithm    = pb.Algorithm    (see client.go)
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
