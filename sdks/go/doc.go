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
//
// # Connection topology
//
// Pick the constructor that matches how your deployment exposes Lantern
// nodes. All three configurations enable the same default retry policy
// (UNAVAILABLE + RESOURCE_EXHAUSTED retried with capped exponential
// backoff) and client-side `round_robin` load balancing; only the resolver
// changes.
//
//   - Single endpoint — PaaS (Cloud Run, ACA, App Service single instance)
//     or any other case where you have one stable URL:
//
//     c, err := client.NewLantern("lantern.example.com:50051")
//
//   - DNS fan-out — k8s ClusterIP / headless Service, Compose service name,
//     or any DNS source that resolves a single hostname to N backend
//     addresses. gRPC re-resolves on connection failure and round_robin
//     spreads load across every resolved address:
//
//     c, err := client.NewLantern("dns:///lantern.default.svc.cluster.local:50051")
//
//   - Explicit static list — bare hosts, environment-supplied pod IPs, or
//     any non-DNS discovery source. Uses gRPC's manual resolver under the
//     hood; you control the membership list and the SDK round-robins
//     across it:
//
//     c, err := client.NewLanternWithEndpoints([]string{
//     "10.0.0.11:50051",
//     "10.0.0.12:50051",
//     "10.0.0.13:50051",
//     })
//
// All three forms transparently failover: if any backend returns
// `codes.Unavailable` the default service config retries on the next
// healthy address, and the readiness gate on each server (HTTP
// `/healthz/ready`) keeps draining nodes out of rotation until they catch
// up on replication.
package client
