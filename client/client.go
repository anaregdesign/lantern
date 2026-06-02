package client

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	model "github.com/anaregdesign/lantern/core/graph"
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

// VertexInput describes one vertex for the batch PutVertices API. Use
// Expiration to set an absolute deadline; for relative TTLs, prefer the
// single-shot PutVertex helper or convert via time.Now().Add(ttl).
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
}

// NewLantern creates a client. The underlying gRPC connection is established
// lazily on the first RPC (grpc.NewClient semantics), so no dial timeout is
// applied here — callers should pass a context with a deadline to the first
// call if they need bounded connect time.
func NewLantern(hostname string, port int) (*Lantern, error) {
	target := net.JoinHostPort(hostname, strconv.Itoa(port))
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}
	return &Lantern{
		conn:   conn,
		client: pb.NewLanternServiceClient(conn),
	}, nil
}

func (l *Lantern) Close() error {
	return l.conn.Close()
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
	result, err := l.client.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return (*Vertex)(result.Vertex), nil
}

// PutVertex upserts a single vertex with a relative TTL. The vertex is
// overwritten if key already exists; see PutVertices for batched writes and
// PutVertexAt for absolute expirations.
func (l *Lantern) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	return l.PutVertexAt(ctx, key, value, time.Now().Add(ttl))
}

// PutVertexAt upserts a single vertex with an absolute expiration time.
func (l *Lantern) PutVertexAt(ctx context.Context, key string, value any, expiration time.Time) error {
	v, err := nativeVertex{key: key, value: value, expiration: expiration}.asVertex()
	if err != nil {
		return err
	}
	_, err = l.client.PutVertex(ctx, &pb.PutVertexRequest{Vertices: []*pb.Vertex{v}})
	return err
}

// PutVertices upserts a batch of vertices in a single RPC.
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
	_, err := l.client.PutVertex(ctx, &pb.PutVertexRequest{Vertices: vs})
	return err
}

func (l *Lantern) DeleteVertex(ctx context.Context, key string) error {
	_, err := l.client.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: key})
	return err
}

// GetEdge fetches the edge weight (and any expiration) between tail and head.
// Returns an error wrapping ErrNotFound when the edge does not exist.
func (l *Lantern) GetEdge(ctx context.Context, tail string, head string) (*Edge, error) {
	result, err := l.client.GetEdge(ctx, &pb.GetEdgeRequest{Tail: tail, Head: head})
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return (*Edge)(result.Edge), nil
}

// AddEdge accumulates weight onto the (tail, head) pair: repeated calls with
// the same endpoints sum their weights. Use PutEdge for replace semantics.
func (l *Lantern) AddEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.AddEdgeAt(ctx, tail, head, weight, time.Now().Add(ttl))
}

// AddEdgeAt is AddEdge with an absolute expiration.
func (l *Lantern) AddEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	_, err := l.client.AddEdge(ctx, &pb.AddEdgeRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	})
	return err
}

// AddEdges accumulates weight onto a batch of edges in a single RPC.
func (l *Lantern) AddEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	edges := make([]*pb.Edge, 0, len(inputs))
	for _, in := range inputs {
		edges = append(edges, &pb.Edge{Tail: in.Tail, Head: in.Head, Weight: in.Weight, Expiration: timestamppb.New(in.Expiration)})
	}
	_, err := l.client.AddEdge(ctx, &pb.AddEdgeRequest{Edges: edges})
	return err
}

// PutEdge overwrites the (tail, head) pair, replacing any existing weight and
// expiration. Use AddEdge to accumulate instead.
func (l *Lantern) PutEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	return l.PutEdgeAt(ctx, tail, head, weight, time.Now().Add(ttl))
}

// PutEdgeAt is PutEdge with an absolute expiration.
func (l *Lantern) PutEdgeAt(ctx context.Context, tail string, head string, weight float32, expiration time.Time) error {
	_, err := l.client.PutEdge(ctx, &pb.PutEdgeRequest{
		Edges: []*pb.Edge{{Tail: tail, Head: head, Weight: weight, Expiration: timestamppb.New(expiration)}},
	})
	return err
}

// PutEdges overwrites a batch of edges in a single RPC.
func (l *Lantern) PutEdges(ctx context.Context, inputs []EdgeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	edges := make([]*pb.Edge, 0, len(inputs))
	for _, in := range inputs {
		edges = append(edges, &pb.Edge{Tail: in.Tail, Head: in.Head, Weight: in.Weight, Expiration: timestamppb.New(in.Expiration)})
	}
	_, err := l.client.PutEdge(ctx, &pb.PutEdgeRequest{Edges: edges})
	return err
}

func (l *Lantern) DeleteEdge(ctx context.Context, tail string, head string) error {
	_, err := l.client.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: tail, Head: head})
	return err
}

// Illuminate runs a k-bounded BFS from seed, returning the resulting subgraph.
// Use IlluminateWithOptimization to request a server-side spanning tree or
// shortest-path-tree transform on the response.
func (l *Lantern) Illuminate(ctx context.Context, seed string, step uint32, k uint32, tfidf bool) (*model.Graph[string, *Vertex], error) {
	return l.IlluminateWithOptimization(ctx, seed, step, k, tfidf, OptimizationUnspecified)
}

// IlluminateWithOptimization is Illuminate with an explicit server-side
// post-processing strategy. Pass OptimizationUnspecified to disable it.
func (l *Lantern) IlluminateWithOptimization(ctx context.Context, seed string, step uint32, k uint32, tfidf bool, opt Optimization) (*model.Graph[string, *Vertex], error) {
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
	g := model.NewGraph[string, *Vertex]()
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
