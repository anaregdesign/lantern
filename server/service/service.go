package service

import (
	"context"
	"log/slog"
	"math"
	"net"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	. "github.com/anaregdesign/lantern/gen/go/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LanternService implements the LanternServiceServer.
//
// Per-call logging is handled by the slog logging interceptor configured in
// server/provider, so handlers stay focused on business logic.
//
// NOTE: we keep the concrete generic type in the constructor because wire
// cannot synthesize type arguments for generic providers.

type LanternService struct {
	UnimplementedLanternServiceServer
	cache *graph.GraphCache[string, *Vertex]
}

func NewLanternService(cache *graph.GraphCache[string, *Vertex]) *LanternService {
	return &LanternService{cache: cache}
}

func (s *LanternService) Illuminate(_ context.Context, request *IlluminateRequest) (*IlluminateResponse, error) {
	g := s.cache.Neighbor(request.Seed, int(request.Step), int(request.K), request.Tfidf)

	switch request.Optimization {
	case Optimization_OPTIMIZATION_UNSPECIFIED:
		// do nothing
	case Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE:
		g = g.MinimumSpanningTree(request.Seed, false)
	case Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE:
		g = g.MinimumSpanningTree(request.Seed, true)
	case Optimization_OPTIMIZATION_SHORTEST_PATH_TREE:
		g = g.ShortestPathTree(request.Seed, func(weight float32) float32 { return weight })
	case Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE:
		g = g.ShortestPathTree(request.Seed, func(weight float32) float32 {
			if weight == 0 {
				return math.MaxFloat32
			}
			return 1 / weight
		})
	}

	vertices := make([]*Vertex, 0, len(g.Vertices))
	for k, v := range g.Vertices {
		if v == nil {
			vertices = append(vertices, &Vertex{Key: k, Value: &Vertex_Nil{Nil: true}})
		} else {
			vertices = append(vertices, v)
		}
	}

	var edges []*Edge
	for tail, heads := range g.Edges {
		for head, weight := range heads {
			edges = append(edges, &Edge{Tail: tail, Head: head, Weight: weight})
		}
	}

	return &IlluminateResponse{
		Graph:  &Graph{Vertices: vertices, Edges: edges},
		Status: Status_STATUS_OK,
	}, nil
}

func (s *LanternService) GetVertex(_ context.Context, request *GetVertexRequest) (*GetVertexResponse, error) {
	v, ok := s.cache.GetVertex(request.GetKey())
	if !ok {
		return nil, status.Error(codes.NotFound, "Vertex not found")
	}
	if v == nil {
		return &GetVertexResponse{
			Vertex: &Vertex{Key: request.GetKey(), Value: &Vertex_Nil{Nil: true}},
			Status: Status_STATUS_OK,
		}, nil
	}
	return &GetVertexResponse{Vertex: v, Status: Status_STATUS_OK}, nil
}

func (s *LanternService) PutVertex(_ context.Context, request *PutVertexRequest) (*PutVertexResponse, error) {
	for _, v := range request.Vertices {
		s.cache.AddVertexWithExpiration(v.Key, v, v.Expiration.AsTime())
	}
	return &PutVertexResponse{Status: Status_STATUS_OK}, nil
}

func (s *LanternService) DeleteVertex(_ context.Context, in *DeleteVertexRequest) (*DeleteVertexResponse, error) {
	s.cache.DeleteVertex(in.GetKey())
	return &DeleteVertexResponse{Status: Status_STATUS_OK}, nil
}

func (s *LanternService) GetEdge(_ context.Context, request *GetEdgeRequest) (*GetEdgeResponse, error) {
	w, ok := s.cache.GetWeight(request.Tail, request.Head)
	if !ok {
		return nil, status.Error(codes.NotFound, "Edge not found")
	}
	return &GetEdgeResponse{
		Edge: &Edge{Tail: request.Tail, Head: request.Head, Weight: w},
	}, nil
}

func (s *LanternService) AddEdge(_ context.Context, request *AddEdgeRequest) (*AddEdgeResponse, error) {
	for _, e := range request.Edges {
		s.cache.AddEdgeWithExpiration(e.Tail, e.Head, e.Weight, e.Expiration.AsTime())
	}
	return &AddEdgeResponse{Status: Status_STATUS_OK}, nil
}

func (s *LanternService) PutEdge(_ context.Context, request *PutEdgeRequest) (*PutEdgeResponse, error) {
	for _, e := range request.Edges {
		s.cache.DeleteEdge(e.Tail, e.Head)
		s.cache.AddEdgeWithExpiration(e.Tail, e.Head, e.Weight, e.Expiration.AsTime())
	}
	return &PutEdgeResponse{Status: Status_STATUS_OK}, nil
}

func (s *LanternService) DeleteEdge(_ context.Context, in *DeleteEdgeRequest) (*DeleteEdgeResponse, error) {
	s.cache.DeleteEdge(in.Tail, in.Head)
	return &DeleteEdgeResponse{Status: Status_STATUS_OK}, nil
}

// LanternServer ties the gRPC server, its listener, and the cache GC loop
// into a single lifecycle.

type LanternServer struct {
	service    *LanternService
	server     *grpc.Server
	listener   net.Listener
	logger     *slog.Logger
	gcInterval time.Duration
}

func NewLanternServer(
	service *LanternService,
	server *grpc.Server,
	listener net.Listener,
	logger *slog.Logger,
	gcInterval time.Duration,
) *LanternServer {
	return &LanternServer{
		service:    service,
		server:     server,
		listener:   listener,
		logger:     logger,
		gcInterval: gcInterval,
	}
}

// Run registers the gRPC service, starts the cache GC loop, and serves until
// ctx is canceled (then GracefulStop drains in-flight RPCs).
func (s *LanternServer) Run(ctx context.Context) error {
	RegisterLanternServiceServer(s.server, s.service)

	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down grpc server")
		s.server.GracefulStop()
	}()

	go s.service.cache.Watch(ctx, s.gcInterval)

	s.logger.Info("grpc server starting",
		slog.String("addr", s.listener.Addr().String()),
		slog.Duration("gc_interval", s.gcInterval),
	)
	return s.server.Serve(s.listener)
}
