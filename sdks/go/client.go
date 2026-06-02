package client

import (
	"context"
	"errors"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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

// ErrInvalidArgument wraps gRPC codes.InvalidArgument responses from the
// server (typically raised by the ValidationInterceptor for empty/oversized
// keys or batch entries above MaxBatchSize). Use errors.Is to branch on
// caller-fixable input errors vs. transient failures.
var ErrInvalidArgument = errors.New("invalid argument")

// ErrResourceExhausted wraps gRPC codes.ResourceExhausted responses (rate
// limiter, server-side back-pressure). Callers that see this should back off
// before retrying.
var ErrResourceExhausted = errors.New("resource exhausted")

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
		keepalive:         defaultKeepalive,
	}
	for _, apply := range opts {
		apply(&o)
	}

	dialOpts := make([]grpc.DialOption, 0, len(o.dialOptions)+3)
	dialOpts = append(dialOpts, grpc.WithKeepaliveParams(o.keepalive))
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

// wrapStatus converts a gRPC status error into one that satisfies
// errors.Is against the matching SDK sentinel (ErrNotFound /
// ErrInvalidArgument / ErrResourceExhausted) while preserving the original
// gRPC error for callers that want full status detail.
func wrapStatus(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.NotFound:
		return errors.Join(ErrNotFound, err)
	case codes.InvalidArgument:
		return errors.Join(ErrInvalidArgument, err)
	case codes.ResourceExhausted:
		return errors.Join(ErrResourceExhausted, err)
	}
	return err
}

// GetVertex fetches the vertex at key.
//
// Presence vs. nil-value semantics:
//   - Absent key  → returns (nil, err) where errors.Is(err, ErrNotFound) is true.
//   - Present key holding an explicit nil value (the proto Vertex_Nil tombstone,
//     written by passing a Go nil to PutVertex/PutVertexAt) → returns a
//     non-nil *Vertex whose Kind() reports VertexKindNil and a nil error.
//
// Callers that need to distinguish "missing" from "present-but-nil" must
// check errors.Is(err, ErrNotFound) rather than only the returned pointer.
func (l *Lantern) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	result, err := l.client.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil {
		return nil, wrapStatus(err)
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
	_, err = l.client.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}})
	return wrapStatus(err)
}

// PutVertices upserts a batch of vertices. Large batches are automatically
// chunked according to WithBatchChunkSize (default 1000).
//
// Partial-write semantics: chunks are sent sequentially. If chunk N fails,
// the entries from chunks 0..N-1 are already committed server-side. The
// returned error is a *BatchError whose Written field records the number of
// successfully committed inputs, so callers can resume with
// inputs[err.Written:]. errors.Is / errors.As continue to work against the
// wrapped gRPC sentinel.
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
	written := 0
	for _, chunk := range chunkSlice(vs, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: chunk})
		cancel()
		if err != nil {
			return &BatchError{Written: written, Err: wrapStatus(err)}
		}
		written += len(chunk)
	}
	return nil
}

// DeleteVertex removes the vertex identified by key. The returned
// bool reports whether the vertex existed (and was therefore removed
// by this call).
func (l *Lantern) DeleteVertex(ctx context.Context, key string) (bool, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := l.client.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: key})
	if err != nil {
		return false, wrapStatus(err)
	}
	return resp.GetExisted(), nil
}

// DeleteVertices removes a batch of vertices. Automatically chunked to
// respect the server's MaxBatchSize cap. Edges incident to removed vertices
// are reaped lazily by the server's GC loop.
//
// Returns the number of keys that actually existed and were therefore
// removed (summed across chunks). Keys absent at call time are silently
// skipped — they do not appear in the count and do not produce errors.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// keys already deleted, so callers can resume with keys[err.Written:].
func (l *Lantern) DeleteVertices(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	written := 0
	deleted := 0
	for _, chunk := range chunkSlice(keys, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		resp, err := l.client.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: chunk})
		cancel()
		if err != nil {
			return deleted, &BatchError{Written: written, Err: wrapStatus(err)}
		}
		written += len(chunk)
		deleted += int(resp.GetDeleted())
	}
	return deleted, nil
}

// GetVertices reads a batch of vertices in one (or a few chunked) round
// trips. Vertices present at call time are returned in found; keys that did
// not exist (or had expired) are returned in missing. Order is unspecified;
// callers should match by Vertex.Key. Automatically chunked to respect the
// server's MaxBatchSize cap.
func (l *Lantern) GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error) {
	if len(keys) == 0 {
		return nil, nil, nil
	}
	for _, chunk := range chunkSlice(keys, l.opts.batchChunkSize) {
		cctx, cancel := l.applyTimeout(ctx)
		resp, rerr := l.client.GetVertices(cctx, &pb.GetVerticesRequest{Keys: chunk})
		cancel()
		if rerr != nil {
			return nil, nil, wrapStatus(rerr)
		}
		for _, v := range resp.GetVertices() {
			found = append(found, (*Vertex)(v))
		}
		missing = append(missing, resp.GetMissing()...)
	}
	return found, missing, nil
}

// GetEdge fetches the edge weight (and any expiration) between tail and head.
func (l *Lantern) GetEdge(ctx context.Context, tail string, head string) (*Edge, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	result, err := l.client.GetEdge(ctx, &pb.GetEdgeRequest{Tail: tail, Head: head})
	if err != nil {
		return nil, wrapStatus(err)
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
	_, err := l.client.AddEdges(ctx, &pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	})
	return wrapStatus(err)
}

// AddEdges accumulates weight onto a batch of edges. Automatically chunked.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// edges already committed, so callers can resume with inputs[err.Written:].
// Note that because AddEdge is additive (not idempotent), naively retrying
// from index 0 will double-count weight for the already-committed prefix.
func (l *Lantern) AddEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	edges := edgesFrom(inputs)
	written := 0
	for _, chunk := range chunkSlice(edges, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.AddEdges(ctx, &pb.AddEdgesRequest{Edges: chunk})
		cancel()
		if err != nil {
			return &BatchError{Written: written, Err: wrapStatus(err)}
		}
		written += len(chunk)
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
	_, err := l.client.PutEdges(ctx, &pb.PutEdgesRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	})
	return wrapStatus(err)
}

// PutEdges overwrites a batch of edges. Automatically chunked.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// edges already overwritten, so callers can resume with inputs[err.Written:].
// Unlike AddEdges, PutEdges is idempotent, so a full retry from index 0 is
// also safe (it will overwrite the already-committed prefix).
func (l *Lantern) PutEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	edges := edgesFrom(inputs)
	written := 0
	for _, chunk := range chunkSlice(edges, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		_, err := l.client.PutEdges(ctx, &pb.PutEdgesRequest{Edges: chunk})
		cancel()
		if err != nil {
			return &BatchError{Written: written, Err: wrapStatus(err)}
		}
		written += len(chunk)
	}
	return nil
}

// DeleteEdge removes the (tail, head) edge. The returned bool reports
// whether the edge existed (and was therefore removed by this call).
func (l *Lantern) DeleteEdge(ctx context.Context, tail string, head string) (bool, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := l.client.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: tail, Head: head})
	if err != nil {
		return false, wrapStatus(err)
	}
	return resp.GetExisted(), nil
}

// EdgeRef identifies an edge by its (tail, head) pair without weight.
// Used by DeleteEdges.
type EdgeRef struct {
	Tail string
	Head string
}

// DeleteEdges removes a batch of edges. Automatically chunked.
//
// Returns the number of edges that actually existed and were therefore
// removed (summed across chunks). Edges absent at call time are silently
// skipped — they do not appear in the count and do not produce errors.
//
// Partial-write semantics: chunks are sent sequentially. On failure the
// returned error is a *BatchError whose Written field records the number of
// edges already deleted, so callers can resume with refs[err.Written:].
func (l *Lantern) DeleteEdges(ctx context.Context, refs []EdgeRef) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	keys := make([]*pb.EdgeKey, 0, len(refs))
	for _, r := range refs {
		keys = append(keys, &pb.EdgeKey{Tail: r.Tail, Head: r.Head})
	}
	written := 0
	deleted := 0
	for _, chunk := range chunkSlice(keys, l.opts.batchChunkSize) {
		ctx, cancel := l.applyTimeout(ctx)
		resp, err := l.client.DeleteEdges(ctx, &pb.DeleteEdgesRequest{Edges: chunk})
		cancel()
		if err != nil {
			return deleted, &BatchError{Written: written, Err: wrapStatus(err)}
		}
		written += len(chunk)
		deleted += int(resp.GetDeleted())
	}
	return deleted, nil
}

// GetEdges reads a batch of edges in one (or a few chunked) round trips.
// Pairs present at call time are returned in found; pairs that did not exist
// (or had expired) are returned in missing. Order is unspecified; callers
// should match by (Edge.Tail, Edge.Head). Automatically chunked to respect
// the server's MaxBatchSize cap.
func (l *Lantern) GetEdges(ctx context.Context, refs []EdgeRef) (found []*Edge, missing []EdgeRef, err error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	keys := make([]*pb.EdgeKey, 0, len(refs))
	for _, r := range refs {
		keys = append(keys, &pb.EdgeKey{Tail: r.Tail, Head: r.Head})
	}
	for _, chunk := range chunkSlice(keys, l.opts.batchChunkSize) {
		cctx, cancel := l.applyTimeout(ctx)
		resp, rerr := l.client.GetEdges(cctx, &pb.GetEdgesRequest{Edges: chunk})
		cancel()
		if rerr != nil {
			return nil, nil, wrapStatus(rerr)
		}
		for _, e := range resp.GetEdges() {
			found = append(found, (*Edge)(e))
		}
		for _, m := range resp.GetMissing() {
			missing = append(missing, EdgeRef{Tail: m.GetTail(), Head: m.GetHead()})
		}
	}
	return found, missing, nil
}

// Graph is the SDK-native representation of an Illuminate response. It mirrors
// the field layout (and JSON shape) of core/graph.Graph[string, *Vertex] so
// callers that need richer graph algorithms can adapt it trivially, but the
// SDK itself stays free of any non-proto dependency.
type Graph struct {
	Vertices map[string]*Vertex            `json:"vertices,omitempty"`
	Edges    map[string]map[string]float32 `json:"edges,omitempty"`
}

// NewGraph returns an empty Graph with both maps initialized.
func NewGraph() *Graph {
	return &Graph{
		Vertices: make(map[string]*Vertex),
		Edges:    make(map[string]map[string]float32),
	}
}

// IlluminateOption configures a single Illuminate call. Pass any combination
// of WithStep, WithK, WithTFIDF, and WithOptimization to Illuminate.
type IlluminateOption func(*illuminateConfig)

type illuminateConfig struct {
	step         uint32
	k            uint32
	tfidf        bool
	optimization Optimization
}

// WithStep sets the BFS depth for an Illuminate call.
func WithStep(step uint32) IlluminateOption {
	return func(c *illuminateConfig) { c.step = step }
}

// WithK sets the per-hop fan-out (top-k neighbours) for an Illuminate call.
func WithK(k uint32) IlluminateOption {
	return func(c *illuminateConfig) { c.k = k }
}

// WithTFIDF toggles server-side TF-IDF re-weighting of edges before
// optimization runs.
func WithTFIDF(tfidf bool) IlluminateOption {
	return func(c *illuminateConfig) { c.tfidf = tfidf }
}

// WithOptimization selects the server-side post-processing strategy applied
// to the illuminated subgraph (e.g. MST, SPT). Pass OptimizationUnspecified
// to disable it.
func WithOptimization(opt Optimization) IlluminateOption {
	return func(c *illuminateConfig) { c.optimization = opt }
}

// Illuminate runs a k-bounded BFS from seed, returning the resulting subgraph.
// Configure step, k, TF-IDF, and optimization via IlluminateOption values; any
// option omitted defaults to its zero value (step=0, k=0, tfidf=false,
// optimization=OptimizationUnspecified), which the server treats as "no
// expansion / no optimization".
func (l *Lantern) Illuminate(ctx context.Context, seed string, opts ...IlluminateOption) (*Graph, error) {
	cfg := illuminateConfig{optimization: OptimizationUnspecified}
	for _, opt := range opts {
		opt(&cfg)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	result, err := l.client.Illuminate(ctx, &pb.IlluminateRequest{
		Seed:         seed,
		Step:         cfg.step,
		K:            cfg.k,
		Tfidf:        cfg.tfidf,
		Optimization: cfg.optimization,
	})
	if err != nil {
		return nil, wrapStatus(err)
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
	if size <= 0 {
		size = defaultBatchChunkSize
	}
	if len(s) <= size {
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
