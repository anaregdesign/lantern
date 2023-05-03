package service

import (
	"context"
	. "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"github.com/anaregdesign/papaya/cache/graph"
	"google.golang.org/grpc/status"
)

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
	g := s.cache.Neighbor(request.Seed, int(request.Step), int(request.Step), request.Tfidf)

	var vertices []*Vertex
	for _, v := range g.Vertices {
		vertices = append(vertices, v)
	}

	var edges []*Edge
	for tail, heads := range g.Edges {
		for head, weight := range heads {
			edges = append(edges, &Edge{
				Tail:   tail,
				Head:   head,
				Weight: float32(weight),
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
	if v, ok := s.cache.GetVertex(request.GetKey()); ok {
		return &GetVertexResponse{
			Vertex: v,
		}, nil
	}
	return nil, status.Error(404, "Vertex not found")
}

func (s *LanternService) PutVertex(ctx context.Context, request *PutVertexRequest) (*PutVertexResponse, error) {
	for _, v := range request.Vertices {
		s.cache.AddVertex(v.Key, v)
	}
	return &PutVertexResponse{}, nil
}

func (s *LanternService) GetEdge(ctx context.Context, request *GetEdgeRequest) (*GetEdgeResponse, error) {
	return nil, status.Error(404, "Edge not found")
}

func (s *LanternService) PutEdge(ctx context.Context, request *PutEdgeRequest) (*PutEdgeResponse, error) {
	for _, e := range request.Edges {
		s.cache.AddEdge(e.Tail, e.Head, float64(e.Weight))
	}
	return &PutEdgeResponse{}, nil
}
