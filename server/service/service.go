package service

import (
	"context"
	"log/slog"
	"math"
	"net"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/sdks/go/gen/graph/v1"
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

	g := s.cache.Neighbor(request.GetSeed(), int(request.GetStep()), int(request.GetK()), request.GetTfidf())

	switch request.GetOptimization() {
	case pb.Optimization_OPTIMIZATION_UNSPECIFIED:
		// do nothing
	case pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE:
		g = g.MinimumSpanningTree(request.GetSeed(), false)
	case pb.Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE:
		g = g.MinimumSpanningTree(request.GetSeed(), true)
	case pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE:
		g = g.ShortestPathTree(request.GetSeed(), func(weight float32) float32 { return weight })
	case pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE:
		g = g.ShortestPathTree(request.GetSeed(), func(weight float32) float32 {
			if weight == 0 {
				return math.MaxFloat32
			}
			return 1 / weight
		})
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
		for head, weight := range heads {
			_, exp, ok := s.cache.GetEdgeDetail(tail, head)
			edge := &pb.Edge{Tail: tail, Head: head, Weight: weight}
			if ok && !exp.IsZero() {
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
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	v, ok := s.cache.GetVertex(request.GetKey())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "vertex %q not found", request.GetKey())
	}
	if v == nil {
		return &pb.GetVertexResponse{
			Vertex: &pb.Vertex{Key: request.GetKey(), Value: &pb.Vertex_Nil{Nil: true}},
		}, nil
	}
	return &pb.GetVertexResponse{Vertex: v}, nil
}

func (s *LanternService) PutVertex(ctx context.Context, request *pb.PutVertexRequest) (*pb.PutVertexResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	var written int32
	for _, v := range request.GetVertices() {
		s.cache.AddVertexWithExpiration(v.GetKey(), v, v.GetExpiration().AsTime())
		written++
	}
	return &pb.PutVertexResponse{Written: written}, nil
}

func (s *LanternService) DeleteVertex(ctx context.Context, in *pb.DeleteVertexRequest) (*pb.DeleteVertexResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	// Per the proto contract, deleting a vertex leaves its edges orphaned;
	// the periodic GC loop reaps any tf/df rows whose endpoints disappear.
	s.cache.DeleteVertex(in.GetKey())
	return &pb.DeleteVertexResponse{}, nil
}

func (s *LanternService) DeleteVertices(ctx context.Context, in *pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	var n int32
	for _, k := range in.GetKeys() {
		s.cache.DeleteVertex(k)
		n++
	}
	return &pb.DeleteVerticesResponse{Deleted: n}, nil
}

func (s *LanternService) GetEdge(ctx context.Context, request *pb.GetEdgeRequest) (*pb.GetEdgeResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	w, exp, ok := s.cache.GetEdgeDetail(request.GetTail(), request.GetHead())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "edge %q -> %q not found", request.GetTail(), request.GetHead())
	}
	edge := &pb.Edge{Tail: request.GetTail(), Head: request.GetHead(), Weight: w}
	if !exp.IsZero() {
		edge.Expiration = timestamppb.New(exp)
	}
	return &pb.GetEdgeResponse{Edge: edge}, nil
}

func (s *LanternService) AddEdge(ctx context.Context, request *pb.AddEdgeRequest) (*pb.AddEdgeResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	var written int32
	for _, e := range request.GetEdges() {
		s.cache.AddEdgeWithExpiration(e.GetTail(), e.GetHead(), e.GetWeight(), e.GetExpiration().AsTime())
		written++
	}
	return &pb.AddEdgeResponse{Written: written}, nil
}

func (s *LanternService) PutEdge(ctx context.Context, request *pb.PutEdgeRequest) (*pb.PutEdgeResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	var written int32
	for _, e := range request.GetEdges() {
		// PutEdgeWithExpiration locks the cache once and performs the
		// delete + add atomically, so concurrent GetEdge readers never
		// observe a transient NotFound between the two operations.
		s.cache.PutEdgeWithExpiration(e.GetTail(), e.GetHead(), e.GetWeight(), e.GetExpiration().AsTime())
		written++
	}
	return &pb.PutEdgeResponse{Written: written}, nil
}

func (s *LanternService) DeleteEdge(ctx context.Context, in *pb.DeleteEdgeRequest) (*pb.DeleteEdgeResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	s.cache.DeleteEdge(in.GetTail(), in.GetHead())
	return &pb.DeleteEdgeResponse{}, nil
}

func (s *LanternService) DeleteEdges(ctx context.Context, in *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	var n int32
	for _, e := range in.GetEdges() {
		s.cache.DeleteEdge(e.GetTail(), e.GetHead())
		n++
	}
	return &pb.DeleteEdgesResponse{Deleted: n}, nil
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
	return s.server.Serve(s.listener)
}
