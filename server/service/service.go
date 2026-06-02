package service

import (
	"context"
	"log/slog"
	"math"
	"net"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ServiceName is the fully-qualified gRPC service name used for per-service
// health reporting (grpc.health.v1) and as a logical metric label.
const ServiceName = "graph.v1.LanternService"

// LanternService implements pb.LanternServiceServer.
//
// Per-call logging, metrics, recovery, tracing and validation are wired up
// via interceptors in server/provider so handlers stay focused on business
// logic. Handlers honor the incoming context — short paths rely on gRPC to
// propagate cancellation; the early ctx.Err() checks let us return a clean
// Canceled / DeadlineExceeded status when a client already gave up.
//
// NOTE: the constructor keeps the concrete generic type because wire cannot
// synthesize type arguments for generic providers.
type LanternService struct {
	pb.UnimplementedLanternServiceServer
	cache *graph.GraphCache[string, *pb.Vertex]
}

func NewLanternService(cache *graph.GraphCache[string, *pb.Vertex]) *LanternService {
	return &LanternService{cache: cache}
}

// Illuminate returns a subgraph rooted at the seed, optionally optimized into
// a spanning or shortest-path tree.
func (s *LanternService) Illuminate(ctx context.Context, request *pb.IlluminateRequest) (*pb.IlluminateResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	g, expirations, err := s.cache.NeighborWithExpirationsContext(ctx, request.GetSeed(), int(request.GetStep()), int(request.GetK()), request.GetTfidf())
	if err != nil {
		return nil, status.FromContextError(err).Err()
	}

	switch request.GetOptimization() {
	case pb.Optimization_OPTIMIZATION_UNSPECIFIED:
		// do nothing
	case pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE:
		g, err = g.MinimumSpanningTreeContext(ctx, request.GetSeed())
	case pb.Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE:
		g, err = g.MaximumSpanningTreeContext(ctx, request.GetSeed())
	case pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE:
		g, err = g.ShortestPathTreeContext(ctx, request.GetSeed(), func(weight float32) float32 { return weight })
	case pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE:
		g, err = g.ShortestPathTreeContext(ctx, request.GetSeed(), func(weight float32) float32 {
			if weight == 0 {
				return math.MaxFloat32
			}
			return 1 / weight
		})
	}
	if err != nil {
		return nil, status.FromContextError(err).Err()
	}

	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	vertices := make([]*pb.Vertex, 0, len(g.Vertices))
	for k, v := range g.Vertices {
		if v == nil {
			vertices = append(vertices, &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			vertices = append(vertices, v)
		}
	}

	var edges []*pb.Edge
	for tail, heads := range g.Edges {
		expRow := expirations[tail]
		for head, weight := range heads {
			edge := &pb.Edge{Tail: tail, Head: head, Weight: weight}
			if exp, ok := expRow[head]; ok && !exp.IsZero() {
				edge.Expiration = timestamppb.New(exp)
			}
			edges = append(edges, edge)
		}
	}

	return &pb.IlluminateResponse{
		Graph: &pb.Graph{Vertices: vertices, Edges: edges},
	}, nil
}

func (s *LanternService) GetVertex(ctx context.Context, request *pb.GetVertexRequest) (*pb.GetVertexResponse, error) {
	resp, err := s.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{request.GetKey()}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetMissing()) == 1 {
		return nil, status.Errorf(codes.NotFound, "vertex %q not found", request.GetKey())
	}
	return &pb.GetVertexResponse{Vertex: resp.GetVertices()[0]}, nil
}

// GetVertices reads several vertices in one round trip. Keys present at call
// time are returned in Vertices; missing (or expired) keys are reported in
// Missing. Order within either slice is not guaranteed to follow request
// order — clients should match by Vertex.key.
func (s *LanternService) GetVertices(ctx context.Context, request *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	keys := request.GetKeys()
	resp := &pb.GetVerticesResponse{
		Vertices: make([]*pb.Vertex, 0, len(keys)),
	}
	for _, k := range keys {
		v, ok := s.cache.GetVertex(k)
		if !ok {
			resp.Missing = append(resp.Missing, k)
			continue
		}
		if v == nil {
			resp.Vertices = append(resp.Vertices, &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			resp.Vertices = append(resp.Vertices, v)
		}
	}
	return resp, nil
}

func (s *LanternService) PutVertex(ctx context.Context, request *pb.PutVertexRequest) (*pb.PutVertexResponse, error) {
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{request.GetVertex()}}); err != nil {
		return nil, err
	}
	return &pb.PutVertexResponse{}, nil
}

func (s *LanternService) PutVertices(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetVertices()
	items := make([]graph.VertexItem[string, *pb.Vertex], 0, len(in))
	for _, v := range in {
		items = append(items, graph.VertexItem[string, *pb.Vertex]{
			Key:        v.GetKey(),
			Value:      v,
			Expiration: v.GetExpiration().AsTime(),
		})
	}
	s.cache.AddVerticesWithExpiration(items)
	return &pb.PutVerticesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) DeleteVertex(ctx context.Context, in *pb.DeleteVertexRequest) (*pb.DeleteVertexResponse, error) {
	// Per the proto contract, deleting a vertex leaves its edges orphaned;
	// the periodic GC loop reaps any tf/df rows whose endpoints disappear.
	resp, err := s.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{in.GetKey()}})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteVertexResponse{Existed: resp.GetDeleted() == 1}, nil
}

func (s *LanternService) DeleteVertices(ctx context.Context, in *pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	n := s.cache.DeleteVertices(in.GetKeys())
	return &pb.DeleteVerticesResponse{Deleted: int32(n)}, nil
}

func (s *LanternService) GetEdge(ctx context.Context, request *pb.GetEdgeRequest) (*pb.GetEdgeResponse, error) {
	resp, err := s.GetEdges(ctx, &pb.GetEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: request.GetTail(), Head: request.GetHead()}},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.GetMissing()) == 1 {
		return nil, status.Errorf(codes.NotFound, "edge %q -> %q not found", request.GetTail(), request.GetHead())
	}
	return &pb.GetEdgeResponse{Edge: resp.GetEdges()[0]}, nil
}

// GetEdges reads several edges in one round trip. Pairs present at call time
// are returned in Edges; missing (or expired) pairs are reported in Missing.
// Order within either slice is not guaranteed to follow request order.
func (s *LanternService) GetEdges(ctx context.Context, request *pb.GetEdgesRequest) (*pb.GetEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetEdges()
	resp := &pb.GetEdgesResponse{
		Edges: make([]*pb.Edge, 0, len(in)),
	}
	for _, k := range in {
		w, exp, ok := s.cache.GetEdgeDetail(k.GetTail(), k.GetHead())
		if !ok {
			resp.Missing = append(resp.Missing, &pb.EdgeKey{Tail: k.GetTail(), Head: k.GetHead()})
			continue
		}
		edge := &pb.Edge{Tail: k.GetTail(), Head: k.GetHead(), Weight: w}
		if !exp.IsZero() {
			edge.Expiration = timestamppb.New(exp)
		}
		resp.Edges = append(resp.Edges, edge)
	}
	return resp, nil
}

func (s *LanternService) AddEdge(ctx context.Context, request *pb.AddEdgeRequest) (*pb.AddEdgeResponse, error) {
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{request.GetEdge()}}); err != nil {
		return nil, err
	}
	return &pb.AddEdgeResponse{}, nil
}

func (s *LanternService) AddEdges(ctx context.Context, request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetEdges()
	items := make([]graph.EdgeItem[string], 0, len(in))
	for _, e := range in {
		items = append(items, graph.EdgeItem[string]{
			Tail:       e.GetTail(),
			Head:       e.GetHead(),
			Weight:     e.GetWeight(),
			Expiration: e.GetExpiration().AsTime(),
		})
	}
	s.cache.AddEdgesWithExpiration(items)
	return &pb.AddEdgesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) PutEdge(ctx context.Context, request *pb.PutEdgeRequest) (*pb.PutEdgeResponse, error) {
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{request.GetEdge()}}); err != nil {
		return nil, err
	}
	return &pb.PutEdgeResponse{}, nil
}

func (s *LanternService) PutEdges(ctx context.Context, request *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	in := request.GetEdges()
	items := make([]graph.EdgeItem[string], 0, len(in))
	for _, e := range in {
		items = append(items, graph.EdgeItem[string]{
			Tail:       e.GetTail(),
			Head:       e.GetHead(),
			Weight:     e.GetWeight(),
			Expiration: e.GetExpiration().AsTime(),
		})
	}
	// PutEdgesWithExpiration takes the cache write lock once for the whole
	// batch, so concurrent GetEdge readers never observe a transient
	// NotFound between the per-edge delete and add.
	s.cache.PutEdgesWithExpiration(items)
	return &pb.PutEdgesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) DeleteEdge(ctx context.Context, in *pb.DeleteEdgeRequest) (*pb.DeleteEdgeResponse, error) {
	resp, err := s.DeleteEdges(ctx, &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: in.GetTail(), Head: in.GetHead()}},
	})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteEdgeResponse{Existed: resp.GetDeleted() == 1}, nil
}

func (s *LanternService) DeleteEdges(ctx context.Context, in *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	inEdges := in.GetEdges()
	keys := make([]graph.EdgeKey[string], 0, len(inEdges))
	for _, e := range inEdges {
		keys = append(keys, graph.EdgeKey[string]{Tail: e.GetTail(), Head: e.GetHead()})
	}
	n := s.cache.DeleteEdges(keys)
	return &pb.DeleteEdgesResponse{Deleted: int32(n)}, nil
}

// LanternServer ties the gRPC server, its listener, the cache GC loop, and
// the per-service health gauge into a single lifecycle owned by the App
// composition root.
type LanternServer struct {
	service         *LanternService
	server          *grpc.Server
	listener        net.Listener
	logger          *slog.Logger
	gcInterval      time.Duration
	shutdownTimeout time.Duration
	health          HealthSetter
}

// HealthSetter is the narrow surface of *health.Server that LanternServer
// needs to publish SERVING / NOT_SERVING per service. Defined here so
// callers can stub it in tests.
type HealthSetter interface {
	SetServingStatus(service string, status healthpb.HealthCheckResponse_ServingStatus)
}

// LifecycleConfig groups the tunables wire injects into LanternServer so the
// constructor signature stays stable as new options are added.
type LifecycleConfig struct {
	GCInterval      time.Duration
	ShutdownTimeout time.Duration
}

func NewLanternServer(
	service *LanternService,
	server *grpc.Server,
	listener net.Listener,
	logger *slog.Logger,
	cfg LifecycleConfig,
	hs HealthSetter,
) *LanternServer {
	return &LanternServer{
		service:         service,
		server:          server,
		listener:        listener,
		logger:          logger,
		gcInterval:      cfg.GCInterval,
		shutdownTimeout: cfg.ShutdownTimeout,
		health:          hs,
	}
}

// Run registers the gRPC service, marks it healthy, starts the cache GC
// loop, and serves until ctx is canceled. On shutdown GracefulStop drains
// in-flight RPCs but is bounded by ShutdownTimeout — past that, Stop forces
// a hard close so the process can exit.
func (s *LanternServer) Run(ctx context.Context) error {
	pb.RegisterLanternServiceServer(s.server, s.service)
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_SERVING)
	}

	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down grpc server", slog.Duration("timeout", s.shutdownTimeout))
		if s.health != nil {
			s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		}
		done := make(chan struct{})
		go func() {
			s.server.GracefulStop()
			close(done)
		}()
		if s.shutdownTimeout <= 0 {
			<-done
			return
		}
		select {
		case <-done:
		case <-time.After(s.shutdownTimeout):
			s.logger.Warn("graceful shutdown deadline exceeded; forcing stop")
			s.server.Stop()
		}
	}()

	go s.service.cache.Watch(ctx, s.gcInterval)

	s.logger.Info("grpc server starting",
		slog.String("addr", s.listener.Addr().String()),
		slog.Duration("gc_interval", s.gcInterval),
		slog.Duration("shutdown_timeout", s.shutdownTimeout),
	)
	// Serve blocks until the server stops. Whatever the reason — graceful
	// shutdown, listener error, or fatal panic recovered by grpc — flip the
	// health gauge to NOT_SERVING so probes don't keep reporting healthy
	// against a dead server. The shutdown goroutine above also sets this,
	// but only on ctx cancellation; this covers every other exit path.
	err := s.server.Serve(s.listener)
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	}
	return err
}
