// Package client is the Go SDK for the Lantern graph service.
//
// # Model definition policy
//
// This SDK follows a strict "no parallel models" rule: wherever a protobuf
// message already expresses a domain concept, the SDK re-exports the pb type
// directly via a Go alias rather than introducing a parallel struct. As of
// today this applies to:
//
//   - Vertex     = pb.Vertex     (see value.go)
//   - Edge       = pb.Edge       (see client.go)
//   - Optimization = pb.Optimization (see client.go)
//
// Because these are true aliases (declared with `type X = pb.X`, not
// `type X pb.X`), client.Vertex and pb.Vertex are the same type and require
// no conversion at the boundary. Go-friendly accessors that would normally
// be methods are exposed as free functions in this package (Kind, IntValue,
// StringValue, ExpirationTime, MarshalVertexJSON, EdgeExpiration, …),
// because methods cannot be attached to aliases of types declared in
// another package.
//
// A few SDK-only types remain and are intentional, not redundant:
//
//   - VertexInput / EdgeInput
//     Ergonomic input shapes that accept Go-native values (any, time.Time,
//     time.Duration) and let the SDK pick the correct pb.Vertex_* oneof
//     variant or build a timestamppb.Timestamp. They exist to spare callers
//     from constructing protobuf oneof wrappers by hand.
//
//   - EdgeRef
//     A small comparable struct (Tail, Head string) used in batch results.
//     pb.EdgeKey is not used here because it embeds protoimpl state, which
//     makes it unsafe to compare with `==` in user code or to place in a
//     map key.
//
//   - Graph
//     A keyed/adjacency-map shape (map[string]*Vertex, map[string]map[string]float32)
//     that is more convenient for client-side traversal than pb.Graph's flat
//     slice shape. This is a deliberate shape transformation, not a duplicate
//     data definition.
//
// See https://github.com/anaregdesign/lantern/issues/106 for the design
// discussion.
package client
