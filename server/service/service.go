package service

import (
	"context"
	. "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"github.com/anaregdesign/papaya/cache/graph"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"log"
	"net"
	"time"
)

// Avoiding bug of `wire`. Generic type is not supported.

type LanternService struct {
	UnimplementedLanternServiceServer
	cache *graph.GraphCache[string, *Vertex]
}

func NewLanternService(cache *graph.GraphCache[string, *Vertex]) *LanternService {
	return &LanternService{
		cache: cache,
	}
}

func (s *LanternService) Illuminate(ctx context.Context, request *IlluminateRequest) (*IlluminateResponse, error) {
	log.Printf("Illuminate: %v", request)
	g := s.cache.Neighbor(request.Seed, int(request.Step), int(request.Step), request.Tfidf)

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
		g = g.ShortestPathTree(request.Seed, func(weight float32) float32 { return 1 / weight })
	}

	var vertices []*Vertex
	for k, v := range g.Vertices {
		if v == nil {
			vertices = append(vertices, &Vertex{
				Key: k,
				Value: &Vertex_Nil{
					Nil: true,
				},
			})
		} else {
			vertices = append(vertices, v)
		}
	}

	var edges []*Edge
	for tail, heads := range g.Edges {
		for head, weight := range heads {
			edges = append(edges, &Edge{
				Tail:   tail,
				Head:   head,
				Weight: weight,
			})
		}
	}

	return &IlluminateResponse{
		Graph: &Graph{
			Vertices: vertices,
			Edges:    edges,
		},
	}, nil
}

func (s *LanternService) GetVertex(ctx context.Context, request *GetVertexRequest) (*GetVertexResponse, error) {
	log.Printf("GetVertex: %v", request)
	if v, ok := s.cache.GetVertex(request.GetKey()); ok {
		return &GetVertexResponse{
			Vertex: v,
		}, nil
	}
	return nil, status.Error(404, "Vertex not found")
}

func (s *LanternService) PutVertex(ctx context.Context, request *PutVertexRequest) (*PutVertexResponse, error) {
	log.Printf("PutVertex: %v", request)
	for _, v := range request.Vertices {
		s.cache.AddVertexWithExpiration(v.Key, v, v.Expiration.AsTime())
	}
	return &PutVertexResponse{}, nil
}

func (s *LanternService) GetEdge(ctx context.Context, request *GetEdgeRequest) (*GetEdgeResponse, error) {
	log.Printf("GetEdge: %v", request)
	w, ok := s.cache.GetWeight(request.Tail, request.Head)
	if !ok {
		return &GetEdgeResponse{
			Edge: &Edge{
				Tail:   request.Tail,
				Head:   request.Head,
				Weight: 0,
			},
		}, nil
	}
	return &GetEdgeResponse{
		Edge: &Edge{
			Tail:   request.Tail,
			Head:   request.Head,
			Weight: w,
		},
	}, nil
}

func (s *LanternService) PutEdge(ctx context.Context, request *AddEdgeRequest) (*AddEdgeResponse, error) {
	log.Printf("PutEdge: %v", request)
	for _, e := range request.Edges {
		s.cache.AddEdgeWithExpiration(e.Tail, e.Head, e.Weight, e.Expiration.AsTime())
	}
	return &AddEdgeResponse{}, nil
}

type LanternServer struct {
	service  *LanternService
	server   *grpc.Server
	listener net.Listener
}

func NewLanternServer(service *LanternService, server *grpc.Server, listener net.Listener) *LanternServer {
	return &LanternServer{
		service:  service,
		server:   server,
		listener: listener,
	}
}

func (s *LanternServer) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		log.Println("Shutting down server")
		s.server.GracefulStop()
	}()

	go s.service.cache.Watch(ctx, 1*time.Minute)

	RegisterLanternServiceServer(s.server, s.service)
	return s.server.Serve(s.listener)
}
