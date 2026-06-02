package client

import (
	"context"
	"net"
	"strconv"
	"time"

	model "github.com/anaregdesign/lantern/core/graph"
	pb "github.com/anaregdesign/lantern/gen/go/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

func (l *Lantern) GetVertex(ctx context.Context, key string) (*Vertex, error) {
	result, err := l.client.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
	if err != nil {
		return nil, err
	}
	p := &Vertex{}
	p.Key = result.Vertex.Key
	p.Value = result.Vertex.Value
	return p, nil
}

func (l *Lantern) PutVertex(ctx context.Context, key string, value any, ttl time.Duration) error {
	v, err := nativeVertex{
		key:        key,
		value:      value,
		expiration: time.Now().Add(ttl),
	}.asVertex()
	if err != nil {
		return err
	}

	request := &pb.PutVertexRequest{
		Vertices: []*pb.Vertex{v},
	}
	if _, err := l.client.PutVertex(ctx, request); err != nil {
		return err
	}
	return nil
}

func (l *Lantern) DeleteVertex(ctx context.Context, key string) error {
	request := &pb.DeleteVertexRequest{
		Key: key,
	}
	if _, err := l.client.DeleteVertex(ctx, request); err != nil {
		return err
	}
	return nil
}

func (l *Lantern) GetEdge(ctx context.Context, tail string, head string) (float32, error) {
	result, err := l.client.GetEdge(ctx, &pb.GetEdgeRequest{Tail: tail, Head: head})
	if err != nil {
		return 0, err
	}
	return result.Edge.Weight, nil
}

func (l *Lantern) AddEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	request := &pb.AddEdgeRequest{
		Edges: []*pb.Edge{
			{
				Tail:       tail,
				Head:       head,
				Weight:     weight,
				Expiration: timestamppb.New(time.Now().Add(ttl)),
			},
		},
	}
	if _, err := l.client.AddEdge(ctx, request); err != nil {
		return err
	}
	return nil
}

func (l *Lantern) PutEdge(ctx context.Context, tail string, head string, weight float32, ttl time.Duration) error {
	request := &pb.PutEdgeRequest{
		Edges: []*pb.Edge{
			{
				Tail:       tail,
				Head:       head,
				Weight:     weight,
				Expiration: timestamppb.New(time.Now().Add(ttl)),
			},
		},
	}
	if _, err := l.client.PutEdge(ctx, request); err != nil {
		return err
	}
	return nil
}

func (l *Lantern) DeleteEdge(ctx context.Context, tail string, head string) error {
	request := &pb.DeleteEdgeRequest{
		Tail: tail,
		Head: head,
	}
	if _, err := l.client.DeleteEdge(ctx, request); err != nil {
		return err
	}
	return nil
}

func (l *Lantern) Illuminate(ctx context.Context, seed string, step int, k int, tfidf bool) (*model.Graph[string, *Vertex], error) {
	// In go-client, optimization is not implemented, but there is a native implementation in core.
	result, err := l.client.Illuminate(ctx, &pb.IlluminateRequest{
		Seed:  seed,
		Step:  uint32(step),
		K:     uint32(k),
		Tfidf: tfidf,
	})
	if err != nil {
		return nil, err
	}
	g := model.NewGraph[string, *Vertex]()
	for _, v := range result.Graph.Vertices {
		g.Vertices[v.Key] = &Vertex{
			Key:   v.Key,
			Value: v.Value,
		}
	}

	for _, e := range result.Graph.Edges {
		if _, ok := g.Edges[e.Tail]; !ok {
			g.Edges[e.Tail] = make(map[string]float32)
		}
		g.Edges[e.Tail][e.Head] = e.Weight
	}

	return g, nil

}
