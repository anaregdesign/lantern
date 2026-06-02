package client

import (
	"context"
	"errors"
	"time"

	pb "github.com/anaregdesign/lantern/gen/go/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrNotFound is returned by GetVertex / GetEdge when the requested key or
// edge does not exist. Use errors.Is to detect it; the underlying gRPC error
// (with codes.NotFound) is wrapped for callers that need the full detail.
var ErrNotFound = errors.New("not found")

// Edge is a thin type alias over the generated protobuf Edge that lets the
// client return the full record (including Expiration) instead of just the
// weight.
type Edge pb.Edge

// ExpirationTime returns the absolute expiration carried by e, or the zero
// time if no expiration was set on the server response.
func (e *Edge) ExpirationTime() time.Time {
	if e == nil || e.Expiration == nil {
		return time.Time{}
	}
	return e.Expiration.AsTime()
}

// Optimization re-exports the server-side Optimization enum so SDK callers do
// not need to import the generated proto package directly.
type Optimization = pb.Optimization

const (
	OptimizationUnspecified             = pb.Optimization_OPTIMIZATION_UNSPECIFIED
	OptimizationMinimumSpanningTree     = pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE
	OptimizationMaximumSpanningTree     = pb.Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE
	OptimizationShortestPathTree        = pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE
	OptimizationShortestPathTreeInverse = pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE
)

// VertexInput describes one vertex for the batch PutVertices API.
type VertexInput struct {
	Key        string
	Value      any
	Expiration time.Time
}

// EdgeInput describes one edge for the batch AddEdges / PutEdges APIs.
type EdgeInput struct {
	Tail       string
	Head       string
	Weight     float32
	Expiration time.Time
}

type Lantern struct {
	conn   *grpc.ClientConn
	client pb.LanternServiceClient
	opts   options
}

// NewLantern dials target (host:port) and returns a connected Lantern client.
//
// The connection is established lazily on the first RPC (grpc.NewClient
// semantics); pass a context with a deadline to your first call if you need
// bounded connect time.
//
// Defaults:
//   - insecure credentials (override with WithTransportCredentials)
//   - retry policy on every idempotent RPC (override with
//     WithDefaultServiceConfig)
//   - batch helpers auto-chunk at 1000 entries (override with
//     WithBatchChunkSize)
func NewLantern(target string, opts ...Option) (*Lantern, error) {
	o := options{
		batchChunkSize:    defaultBatchChunkSize,
		serviceConfigJSON: defaultServiceConfig,
	}
	for _, apply := range opts {
		apply(&o)
	}

	dialOpts := make([]grpc.DialOption, 0, len(o.dialOptions)+2)
	if o.transportCreds != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(o.transportCreds))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if o.serviceConfigJSON != "" {
		dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(o.serviceConfigJSON))
	}
	dialOpts = append(dialOpts, o.dialOptions...)

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, err
	}
	return &Lantern{
		conn:   conn,
		client: pb.NewLanternServiceClient(conn),
		opts:   o,
	}, nil
}

func (l *Lantern) Close() error {
	return l.conn.Close()
}

// applyTimeout returns ctx with the default timeout applied when the caller
// did not already set a deadline. The returned cancel is always safe to call
// (no-op when no timeout was applied).
func (l *Lantern) applyTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if l.opts.defaultTimeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, l.opts.defaultTimeout)
}

// wrapNotFound converts a codes.NotFound gRPC error into one that satisfies
// errors.Is(err, ErrNotFound) while preserving the original detail.
func wrapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.NotFound {
		return errors.Join(ErrNotFound, err)
	}
	return err
}

// GetVertex fetches the vertex at key. Returns an error wrapping ErrNotFound
// when the key is absent.
func (l *Lantern) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	result, err := l.client.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return (*Vertex)(result.Vertex), nil
}

// PutVertex upserts a single vertex with a relative TTL.
func (l *Lantern) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return l.PutVertexAt(ctx, key, value, time.Now().Add(ttl))
}

// PutVertexAt upserts a single vertex with an absolute expiration time.
func (l *Lantern) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error {
	v, err := nativeVertex{key: key, value: value, expiration: expiration}.asVertex()
	if err != nil {
		return err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err = l.client.PutVertex(ctx, &pb.PutVertexRequest{Vertices: []*pb.Vertex{v}})
	return err
}

// PutVertices upserts a batch of vertices. Large batches are automatically
// chunked according to WithBatchChunkSize (default 1000).
func (l *Lantern) PutVertices(ctx context.Context, inputs []VertexInput) error {
	if len(inputs) == 0 {
		return nil
	}
	vs := make([]*pb.Vertex, 0, len(inputs))
	for _, in := range inputs {
		v, err := nativeVertex{key: in.Key, value: in.Value, expiration: in.Expiration}.asVertex()
		if err != nil {
			return err
		}
		vs = append(vs, v)
	}
	for _, chunk := range chunkSlice(vs, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.PutVertex(ctx, &pb.PutVertexRequest{Vertices: chunk})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (l *Lantern) DeleteVertex(ctx context.Context, key string) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := l.client.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: key})
	return err
}

// DeleteVertices removes a batch of vertices. Automatically chunked to
// respect the server's MaxBatchSize cap. Edges incident to removed vertices
// are reaped lazily by the server's GC loop.
func (l *Lantern) DeleteVertices(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	for _, chunk := range chunkSlice(keys, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: chunk})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// GetEdge fetches the edge weight (and any expiration) between tail and head.
func (l *Lantern) GetEdge(ctx context.Context, tail string, head string) (*Edge, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	result, err := l.client.GetEdge(ctx, &pb.GetEdgeRequest{Tail: tail, Head: head})
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return (*Edge)(result.Edge), nil
}

// AddEdge accumulates weight onto the (tail, head) pair: repeated calls with
// the same endpoints sum their weights.
func (l *Lantern) AddEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.AddEdgeAt(ctx, tail, head, weight, time.Now().Add(ttl))
}

// AddEdgeAt is AddEdge with an absolute expiration.
func (l *Lantern) AddEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := l.client.AddEdge(ctx, &pb.AddEdgeRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	})
	return err
}

// AddEdges accumulates weight onto a batch of edges. Automatically chunked.
func (l *Lantern) AddEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	edges := edgesFrom(inputs)
	for _, chunk := range chunkSlice(edges, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.AddEdge(ctx, &pb.AddEdgeRequest{Edges: chunk})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// PutEdge overwrites the (tail, head) pair.
func (l *Lantern) PutEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.PutEdgeAt(ctx, tail, head, weight, time.Now().Add(ttl))
}

// PutEdgeAt is PutEdge with an absolute expiration.
func (l *Lantern) PutEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := l.client.PutEdge(ctx, &pb.PutEdgeRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	})
	return err
}

// PutEdges overwrites a batch of edges. Automatically chunked.
func (l *Lantern) PutEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	edges := edgesFrom(inputs)
	for _, chunk := range chunkSlice(edges, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.PutEdge(ctx, &pb.PutEdgeRequest{Edges: chunk})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (l *Lantern) DeleteEdge(ctx context.Context, tail string, head string) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := l.client.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: tail, Head: head})
	return err
}

// EdgeRef identifies an edge by its (tail, head) pair without weight.
// Used by DeleteEdges.
type EdgeRef struct {
	Tail string
	Head string
}

// DeleteEdges removes a batch of edges. Automatically chunked.
func (l *Lantern) DeleteEdges(ctx context.Context, refs []EdgeRef) error {
	if len(refs) == 0 {
		return nil
	}
	keys := make([]*pb.EdgeKey, 0, len(refs))
	for _, r := range refs {
		keys = append(keys, &pb.EdgeKey{Tail: r.Tail, Head: r.Head})
	}
	for _, chunk := range chunkSlice(keys, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.DeleteEdges(ctx, &pb.DeleteEdgesRequest{Edges: chunk})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// Graph is the SDK-native representation of an Illuminate response. It mirrors
// the field layout (and JSON shape) of core/graph.Graph[string, *Vertex] so
// callers that need richer graph algorithms can adapt it trivially, but the
// SDK itself stays free of any non-proto dependency.
type Graph struct {
	Vertices map[string]*Vertex             `json:"vertices,omitempty"`
	Edges    map[string]map[string]float32 `json:"edges,omitempty"`
}

// NewGraph returns an empty Graph with both maps initialized.
func NewGraph() *Graph {
	return &Graph{
		Vertices: make(map[string]*Vertex),
		Edges:    make(map[string]map[string]float32),
	}
}

// Illuminate runs a k-bounded BFS from seed, returning the resulting subgraph.
func (l *Lantern) Illuminate(ctx context.Context, seed string, step uint32, k uint32, tfidf bool) (*Graph, error) {
	return l.IlluminateWithOptimization(ctx, seed, step, k, tfidf, OptimizationUnspecified)
}

// IlluminateWithOptimization is Illuminate with an explicit server-side
// post-processing strategy. Pass OptimizationUnspecified to disable it.
func (l *Lantern) IlluminateWithOptimization(ctx context.Context, seed string, step uint32, k uint32, tfidf bool, opt Optimization) (*Graph, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	result, err := l.client.Illuminate(ctx, &pb.IlluminateRequest{
		Seed:         seed,
		Step:         step,
		K:            k,
		Tfidf:        tfidf,
		Optimization: opt,
	})
	if err != nil {
		return nil, err
	}
	g := NewGraph()
	for _, v := range result.Graph.Vertices {
		g.Vertices[v.Key] = (*Vertex)(v)
	}
	for _, e := range result.Graph.Edges {
		if _, ok := g.Edges[e.Tail]; !ok {
			g.Edges[e.Tail] = make(map[string]float32)
		}
		g.Edges[e.Tail][e.Head] = e.Weight
	}
	return g, nil
}

func edgesFrom(inputs []EdgeInput) []*pb.Edge {
	edges := make([]*pb.Edge, 0, len(inputs))
	for _, in := range inputs {
		edges = append(edges, &pb.Edge{
			Tail:       in.Tail,
			Head:       in.Head,
			Weight:     in.Weight,
			Expiration: timestamppb.New(in.Expiration),
		})
	}
	return edges
}

func chunkSlice[T any](s []T, size int) [][]T {
	if size <= 0 || len(s) <= size {
		return [][]T{s}
	}
	out := make([][]T, 0, (len(s)+size-1)/size)
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}
