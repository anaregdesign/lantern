package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// When WithTombstoneTTL is set, RPCs that accept a per-entry Expiration
// must reject values that exceed the clamp with codes.InvalidArgument and
// an error message that names LANTERN_TOMBSTONE_TTL (so operators can
// trace it back to the env knob).
func TestExpirationClamp_RejectsBeyondTTL(t *testing.T) {
	const ttl = time.Hour
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl)
	ctx := context.Background()

	tooFar := timestamppb.New(time.Now().Add(2 * ttl))

	cases := []struct {
		name string
		call func() error
	}{
		{"PutVertices", func() error {
			_, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
				Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: tooFar}},
			})
			return err
		}},
		{"AddEdges", func() error {
			_, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
				Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: tooFar}},
			})
			return err
		}},
		{"PutEdges", func() error {
			_, err := s.PutEdges(ctx, &pb.PutEdgesRequest{
				Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: tooFar}},
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Fatalf("code: got %v, want InvalidArgument", code)
			}
			if !strings.Contains(err.Error(), "LANTERN_TOMBSTONE_TTL") {
				t.Errorf("message should reference LANTERN_TOMBSTONE_TTL; got %q", err.Error())
			}
		})
	}
}

// Within the clamp, the same RPCs succeed.
func TestExpirationClamp_AcceptsWithinTTL(t *testing.T) {
	const ttl = time.Hour
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl)
	ctx := context.Background()

	ok := timestamppb.New(time.Now().Add(ttl / 2))

	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: ok}},
	}); err != nil {
		t.Fatalf("PutVertices within TTL: %v", err)
	}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: ok}},
	}); err != nil {
		t.Fatalf("AddEdges within TTL: %v", err)
	}
}

// Zero expiration (= no expiration) is always accepted regardless of TTL.
func TestExpirationClamp_ZeroAlwaysAllowed(t *testing.T) {
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(time.Minute)
	ctx := context.Background()
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}}},
	}); err != nil {
		t.Fatalf("PutVertices with zero expiration: %v", err)
	}
}
