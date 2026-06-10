// Package client: client.go owns the *Lantern type, its
// Connect-Go-backed constructor, the unary forwarder helper, and the
// canonical surface of the SDK (GetVertex, PutVertices, Illuminate, …).
//
// Wire transport is Connect-Go over h2c (plaintext HTTP/2) or
// TLS-backed HTTP/2. The SDK does not speak gRPC and does not depend
// on google.golang.org/grpc.
package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// ErrNotFound is returned by GetVertex / GetEdge when the requested key or
// edge does not exist. Use errors.Is to detect it; the underlying
// *connect.Error (with connect.CodeNotFound) is joined for callers that
// need the full detail.
var ErrNotFound = errors.New("not found")

// ErrInvalidArgument wraps connect.CodeInvalidArgument responses from the
// server (typically raised by the ValidationInterceptor for empty/oversized
// keys or batch entries above MaxBatchSize). Use errors.Is to branch on
// caller-fixable input errors vs. transient failures.
var ErrInvalidArgument = errors.New("invalid argument")

// ErrResourceExhausted wraps connect.CodeResourceExhausted responses
// (rate limiter, server-side back-pressure). Callers that see this
// should back off before retrying.
var ErrResourceExhausted = errors.New("resource exhausted")

// Edge re-exports the generated protobuf Edge type so SDK callers do not
// need to import the pb package directly. It is a true Go type alias, not a
// parallel struct: client.Edge and pb.Edge are the same type, freely
// interchangeable without conversion.
//
// Use EdgeExpiration to read the Expiration field as a Go time.Time.
type Edge = pb.Edge

// EdgeExpiration returns the absolute expiration carried by e, or the zero
// time if no expiration was set on the server response.
func EdgeExpiration(e *Edge) time.Time {
	if e == nil || e.Expiration == nil {
		return time.Time{}
	}
	return e.Expiration.AsTime()
}

// Algorithm re-exports the server-side post-traversal reduction enum so SDK
// callers do not need to import the generated proto package directly. See
// #410 for the orthogonal-axes redesign that replaced the legacy
// Optimization enum with (Algorithm, Objective, Weighting).
type Algorithm = pb.Algorithm

const (
	AlgorithmUnspecified         = pb.Algorithm_ALGORITHM_UNSPECIFIED
	AlgorithmMinimumSpanningTree = pb.Algorithm_ALGORITHM_MINIMUM_SPANNING_TREE
	AlgorithmShortestPathTree    = pb.Algorithm_ALGORITHM_SHORTEST_PATH_TREE
)

// Objective is the direction of the weight-sensitive optimisation. It
// governs both the per-hop top-k pruning and the post-traversal reduction
// (#560): MINIMIZE treats edge weights as costs (keeps the k smallest-weight
// edges per hop, smallest tree wins), MAXIMIZE treats them as relevance
// (keeps the k largest-weight edges per hop, largest tree wins). Server
// resolves ObjectiveUnspecified to ObjectiveMaximize.
type Objective = pb.Objective

const (
	ObjectiveUnspecified = pb.Objective_OBJECTIVE_UNSPECIFIED
	ObjectiveMinimize    = pb.Objective_OBJECTIVE_MINIMIZE
	ObjectiveMaximize    = pb.Objective_OBJECTIVE_MAXIMIZE
)

// Weighting is the edge-weight transform applied BEFORE the BFS walk.
// RAW uses the stored edge.weight verbatim; TFIDF re-scores using TF-IDF
// over the per-vertex out-edge distribution. Server resolves
// WeightingUnspecified to WeightingRaw.
type Weighting = pb.Weighting

const (
	WeightingUnspecified = pb.Weighting_WEIGHTING_UNSPECIFIED
	WeightingRaw         = pb.Weighting_WEIGHTING_RAW
	WeightingTFIDF       = pb.Weighting_WEIGHTING_TFIDF
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

// Lantern is a Connect-Go-backed client for the Lantern graph service.
// Construct one via NewLantern; share it across goroutines.
type Lantern struct {
	client     graphv1connect.LanternServiceClient
	opts       options
	httpClient *http.Client
	baseURL    string
}

// NewLantern dials the Lantern server at baseURL and returns a
// connected client. baseURL must include the scheme: "http://host:port"
// for h2c (matches the Lantern primary listener default) or
// "https://host[:port]" for TLS. A trailing slash is stripped
// defensively; an empty string or schemeless input is rejected.
//
// Override the default h2c http.Client via WithHTTPClient — for TLS
// supply &http.Client{Transport: &http2.Transport{TLSClientConfig: cfg}}.
func NewLantern(baseURL string, opts ...Option) (*Lantern, error) {
	if baseURL == "" {
		return nil, errors.New("client: NewLantern requires a base URL with scheme")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("client: NewLantern baseURL must start with http:// or https://; got %q", baseURL)
	}
	baseURL = strings.TrimRight(baseURL, "/")

	o := options{batchChunkSize: defaultBatchChunkSize}
	for _, apply := range opts {
		apply(&o)
	}
	if o.httpClient == nil {
		o.httpClient = defaultH2CClient()
	}

	return &Lantern{
		client:     graphv1connect.NewLanternServiceClient(o.httpClient, baseURL, o.clientOptions...),
		opts:       o,
		httpClient: o.httpClient,
		baseURL:    baseURL,
	}, nil
}

// Close releases any idle HTTP/2 connections the SDK is holding. The
// underlying http.Client manages its own connection pool, so Close is
// best-effort and noop-safe to call multiple times.
func (l *Lantern) Close() error {
	if l.httpClient != nil {
		l.httpClient.CloseIdleConnections()
	}
	return nil
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

// unary is the one-line forwarding helper every *Lantern RPC method
// uses. It boxes req via connect.NewRequest, runs the typed Connect-Go
// method, lifts errors through wrapConnectErr so the SDK sentinels
// (ErrNotFound, etc.) match, and returns the unwrapped response.
//
// Callers are responsible for applyTimeout — unary leaves ctx alone so
// batch helpers can drive a single outer deadline across multiple
// chunks.
func unary[Req, Resp any](
	ctx context.Context,
	req *Req,
	fn func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error),
) (*Resp, error) {
	resp, err := fn(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, wrapConnectErr(err)
	}
	return resp.Msg, nil
}

// wrapConnectErr lifts a *connect.Error into a joined error that
// satisfies errors.Is against the matching SDK sentinel (ErrNotFound /
// ErrInvalidArgument / ErrResourceExhausted) while preserving the
// original *connect.Error for callers that need connect.CodeOf or the
// per-error metadata. Non-Connect errors pass through unchanged.
func wrapConnectErr(err error) error {
	if err == nil {
		return nil
	}
	switch connect.CodeOf(err) {
	case connect.CodeNotFound:
		return errors.Join(ErrNotFound, err)
	case connect.CodeInvalidArgument:
		return errors.Join(ErrInvalidArgument, err)
	case connect.CodeResourceExhausted:
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
	resp, err := unary(ctx, &pb.GetVertexRequest{Key: key}, l.client.GetVertex)
	if err != nil {
		return nil, err
	}
	return resp.Vertex, nil
}

// expirationFromTTL converts a relative TTL into the absolute expiration
// the wire carries. A non-positive ttl means "no expiration" (permanent):
// it yields the zero time.Time, which serialises to the wire's permanent
// sentinel and is stored by the server as never-expiring (see #523 and
// core/cache.IsLiveAt). Decay is therefore strictly opt-in via a positive
// ttl. This keeps the relative-TTL convenience methods consistent with the
// absolute *At variants, which already treat a zero expiration as permanent.
func expirationFromTTL(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

// PutVertex upserts a single vertex with a relative TTL. A non-positive ttl
// stores the vertex permanently (no decay); see expirationFromTTL.
func (l *Lantern) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return l.PutVertexAt(ctx, key, value, expirationFromTTL(ttl))
}

// PutVertexAt upserts a single vertex with an absolute expiration time.
func (l *Lantern) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error {
	v, err := nativeVertex{key: key, value: value, expiration: expiration}.asVertex()
	if err != nil {
		return err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err = unary(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}, l.client.PutVertices)
	return err
}

// PutVertices upserts a batch of vertices. Large batches are automatically
// chunked according to WithBatchChunkSize (default 1000).
//
// Partial-write semantics: chunks are sent sequentially. If chunk N fails,
// the entries from chunks 0..N-1 are already committed server-side. The
// returned error is a *BatchError whose Written field records the number of
// successfully committed inputs, so callers can resume with
// inputs[err.Written:]. errors.Is / errors.As continue to work against the
// wrapped SDK sentinel.
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
	_, err := runBatchWrite(ctx, l, vs, func(ctx context.Context, chunk []*pb.Vertex) (int32, error) {
		_, err := unary(ctx, &pb.PutVerticesRequest{Vertices: chunk}, l.client.PutVertices)
		return 0, err
	})
	return err
}

// DeleteVertex removes the vertex identified by key. The returned
// bool reports whether the vertex existed (and was therefore removed
// by this call).
func (l *Lantern) DeleteVertex(ctx context.Context, key string) (bool, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, &pb.DeleteVertexRequest{Key: key}, l.client.DeleteVertex)
	if err != nil {
		return false, err
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
	return runBatchWrite(ctx, l, keys, func(ctx context.Context, chunk []string) (int32, error) {
		resp, err := unary(ctx, &pb.DeleteVerticesRequest{Keys: chunk}, l.client.DeleteVertices)
		if err != nil {
			return 0, err
		}
		return resp.GetDeleted(), nil
	})
}

// GetVertices reads a batch of vertices in one (or a few chunked) round
// trips. Vertices present at call time are returned in found; keys that did
// not exist (or had expired) are returned in missing. Order is unspecified;
// callers should match by Vertex.Key. Automatically chunked to respect the
// server's MaxBatchSize cap.
func (l *Lantern) GetVertices(ctx context.Context, keys []string) (found []*Vertex, missing []string, err error) {
	err = runBatchRead(ctx, l, keys, func(ctx context.Context, chunk []string) error {
		resp, rerr := unary(ctx, &pb.GetVerticesRequest{Keys: chunk}, l.client.GetVertices)
		if rerr != nil {
			return rerr
		}
		found = append(found, resp.GetVertices()...)
		missing = append(missing, resp.GetMissing()...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return found, missing, nil
}

// GetEdge fetches the edge weight (and any expiration) between tail and head.
func (l *Lantern) GetEdge(ctx context.Context, tail string, head string) (*Edge, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, &pb.GetEdgeRequest{Tail: tail, Head: head}, l.client.GetEdge)
	if err != nil {
		return nil, err
	}
	return resp.Edge, nil
}

// AddEdge accumulates weight onto the (tail, head) pair: repeated calls with
// the same endpoints sum their weights. A non-positive ttl stores the edge
// permanently (no decay); see expirationFromTTL.
func (l *Lantern) AddEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.AddEdgeAt(ctx, tail, head, weight, expirationFromTTL(ttl))
}

// AddEdgeAt is AddEdge with an absolute expiration.
func (l *Lantern) AddEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := unary(ctx, &pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	}, l.client.AddEdges)
	return err
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
	_, err := runBatchWrite(ctx, l, edgesFrom(inputs), func(ctx context.Context, chunk []*pb.Edge) (int32, error) {
		_, err := unary(ctx, &pb.AddEdgesRequest{Edges: chunk}, l.client.AddEdges)
		return 0, err
	})
	return err
}

// PutEdge overwrites the (tail, head) pair. A non-positive ttl stores the
// edge permanently (no decay); see expirationFromTTL.
func (l *Lantern) PutEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.PutEdgeAt(ctx, tail, head, weight, expirationFromTTL(ttl))
}

// PutEdgeAt is PutEdge with an absolute expiration.
func (l *Lantern) PutEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	_, err := unary(ctx, &pb.PutEdgesRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	}, l.client.PutEdges)
	return err
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
	_, err := runBatchWrite(ctx, l, edgesFrom(inputs), func(ctx context.Context, chunk []*pb.Edge) (int32, error) {
		_, err := unary(ctx, &pb.PutEdgesRequest{Edges: chunk}, l.client.PutEdges)
		return 0, err
	})
	return err
}

// DeleteEdge removes the (tail, head) edge. The returned bool reports
// whether the edge existed (and was therefore removed by this call).
func (l *Lantern) DeleteEdge(ctx context.Context, tail string, head string) (bool, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, &pb.DeleteEdgeRequest{Tail: tail, Head: head}, l.client.DeleteEdge)
	if err != nil {
		return false, err
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
	return runBatchWrite(ctx, l, keys, func(ctx context.Context, chunk []*pb.EdgeKey) (int32, error) {
		resp, err := unary(ctx, &pb.DeleteEdgesRequest{Edges: chunk}, l.client.DeleteEdges)
		if err != nil {
			return 0, err
		}
		return resp.GetDeleted(), nil
	})
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
	err = runBatchRead(ctx, l, keys, func(ctx context.Context, chunk []*pb.EdgeKey) error {
		resp, rerr := unary(ctx, &pb.GetEdgesRequest{Edges: chunk}, l.client.GetEdges)
		if rerr != nil {
			return rerr
		}
		found = append(found, resp.GetEdges()...)
		for _, m := range resp.GetMissing() {
			missing = append(missing, EdgeRef{Tail: m.GetTail(), Head: m.GetHead()})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
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
// of WithStep, WithK, WithAlgorithm, WithObjective, and WithWeighting to
// Illuminate. See #410 for the orthogonal-axes design.
type IlluminateOption func(*illuminateConfig)

type illuminateConfig struct {
	step      uint32
	k         uint32
	algorithm Algorithm
	objective Objective
	weighting Weighting
}

// WithStep sets the BFS depth for an Illuminate call.
func WithStep(step uint32) IlluminateOption {
	return func(c *illuminateConfig) { c.step = step }
}

// WithK sets the per-hop fan-out (top-k neighbours) for an Illuminate call.
func WithK(k uint32) IlluminateOption {
	return func(c *illuminateConfig) { c.k = k }
}

// WithAlgorithm selects the post-traversal subgraph reduction applied to
// the illuminated subgraph. Pass AlgorithmUnspecified to disable the
// reduction (the server returns the raw discovered subgraph).
func WithAlgorithm(a Algorithm) IlluminateOption {
	return func(c *illuminateConfig) { c.algorithm = a }
}

// WithObjective sets the direction of the Algorithm-driven reduction
// (MINIMIZE for cost-weighted trees, MAXIMIZE for relevance-weighted
// trees). Ignored when Algorithm == AlgorithmUnspecified.
func WithObjective(o Objective) IlluminateOption {
	return func(c *illuminateConfig) { c.objective = o }
}

// WithWeighting toggles the edge-weight transform applied BEFORE the
// BFS walk. WeightingRaw (the default) uses edge.weight verbatim;
// WeightingTFIDF re-scores edges using TF-IDF over the per-vertex
// out-edge distribution.
func WithWeighting(w Weighting) IlluminateOption {
	return func(c *illuminateConfig) { c.weighting = w }
}

// Illuminate runs a k-bounded BFS from seed, returning the resulting subgraph.
// Configure step, k, algorithm, objective, and weighting via IlluminateOption
// values; any option omitted defaults to its zero value, which the server
// resolves to (step=0, k=0, no reduction, RAW weighting). See #410.
func (l *Lantern) Illuminate(ctx context.Context, seed string, opts ...IlluminateOption) (*Graph, error) {
	var cfg illuminateConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, &pb.IlluminateRequest{
		Seed:      seed,
		Step:      cfg.step,
		K:         cfg.k,
		Algorithm: cfg.algorithm,
		Objective: cfg.objective,
		Weighting: cfg.weighting,
	}, l.client.Illuminate)
	if err != nil {
		return nil, err
	}
	g := NewGraph()
	for _, v := range resp.Graph.Vertices {
		g.Vertices[v.Key] = v
	}
	for _, e := range resp.Graph.Edges {
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
