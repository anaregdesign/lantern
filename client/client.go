package client

import (
	"context"
	"errors"
	pb "github.com/anaregdesign/lantern-proto/go/graph/v1"
	model "github.com/anaregdesign/papaya/graph"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strconv"
	"time"
)

type Lantern struct {
	conn   *grpc.ClientConn
	client pb.LanternServiceClient
}

func NewLantern(hostname string, port int) (*Lantern, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	chConn := make(chan *grpc.ClientConn)
	chErr := make(chan error)

	go func() {
		conn, err := grpc.DialContext(ctx, hostname+":"+strconv.Itoa(port), grpc.WithInsecure())
		if err != nil {
			chErr <- err
			return
		}
		chConn <- conn
	}()
	select {
	case <-ctx.Done():
		return nil, errors.New("grpc connection timeout")

	case err := <-chErr:
		return nil, err

	case conn := <-chConn:
		return &Lantern{
			conn:   conn,
			client: pb.NewLanternServiceClient(conn),
		}, nil
	}
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

func (l *Lantern) PutVertex(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
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
	if _, err := l.client.PutEdge(ctx, request); err != nil {
		return err
	}
	return nil
}

func (l *Lantern) Illuminate(ctx context.Context, seed string, step int, k int, tfidf bool) (*model.Graph[string, *Vertex], error) {
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
		var vv Vertex
		vv.Value = v.Value
		g.Vertices[v.Key] = &vv
	}

	for _, e := range result.Graph.Edges {
		if _, ok := g.Edges[e.Tail]; !ok {
			g.Edges[e.Tail] = make(map[string]float32)
		}
		g.Edges[e.Tail][e.Head] = e.Weight
	}

	return g, nil

}
