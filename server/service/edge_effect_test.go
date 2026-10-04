package service

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

func TestEdgeOnlyOriginConstraints(t *testing.T) {
	for _, method := range []string{"Add", "Put"} {
		t.Run(method, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			cache.RetainDanglingEdgeHistory()
			ttl := time.Now().Add(time.Minute)
			for _, key := range []string{"tail", "head"} {
				v := &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: key}, Expiration: timestamppb.New(ttl)}
				if err := cache.PutVertexWithExpiration(key, v, ttl); err != nil {
					t.Fatal(err)
				}
			}
			svc := NewLanternService(cache)
			svc.dataAuthorization = true // trusted policy composition; wire admission has separate coverage
			write := func(edges []*pb.Edge) error {
				if method == "Add" {
					_, err := svc.AddEdges(context.Background(), &pb.AddEdgesRequest{Edges: edges})
					return err
				}
				_, err := svc.PutEdges(context.Background(), &pb.PutEdgesRequest{Edges: edges})
				return err
			}
			live := &pb.Edge{Tail: "tail", Head: "head", Weight: 2, Expiration: timestamppb.New(time.Now().Add(time.Hour))}
			if err := write([]*pb.Edge{live}); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"tail", "head"} {
				value, ok := cache.GetVertex(key)
				if !ok || value.GetString_() != key || !value.GetExpiration().AsTime().Equal(ttl) {
					t.Fatal("Edge extended or changed endpoint", key)
				}
			}
			for _, row := range cache.SnapshotVertices() {
				if !row.Expiration.Equal(ttl) {
					t.Fatal("Edge extended physical endpoint TTL", row.Key)
				}
			}
			err := write([]*pb.Edge{{Tail: "tail", Head: "head", Weight: 7}, {Tail: "tail", Head: "missing", Weight: 3}})
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, graphcache.ErrEdgeEndpointNotLive) {
				t.Fatal("missing endpoint error", err)
			}
			if weight, ok := cache.GetWeight("tail", "head"); !ok || weight != 2 {
				t.Fatal("partial constrained batch", weight, ok)
			}
			if _, ok := cache.GetVertex("missing"); ok {
				t.Fatal("fabricated endpoint")
			}
			if !blindReceiptDisposition(err) {
				t.Fatal("private liveness escaped blind disposition")
			}
		})
	}
	t.Run("OFF preserves implicit endpoints", func(t *testing.T) {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		svc := NewLanternService(cache)
		if _, err := svc.AddEdges(context.Background(), &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tail", Head: "head", Weight: 1}}}); err != nil {
			t.Fatal(err)
		}
		if _, ok := cache.GetVertex("head"); !ok {
			t.Fatal("OFF legacy endpoint creation lost")
		}
	})
}

func TestEdgeEndpointEffectRejectsOtherArms(t *testing.T) {
	if err := validateEdgeEndpointEffect(&pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{}}}); err == nil {
		t.Fatal("misplaced effect accepted")
	}
	if err := validateEdgeEndpointEffect(&pb.MutationOp{NoEndpointCreation: true, Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{}}}); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedPeerRejectsLegacyEndpointEffectsBeforeProgress(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewLanternService(cache)
	svc.dataAuthorization = true
	m := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "tail", Head: "head", Weight: 1}}}})
	if err := svc.ApplyMutation(t.Context(), m); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("legacy endpoint effect admitted", err)
	}
	if len(cache.SnapshotVertices()) != 0 || len(cache.SnapshotEdges()) != 0 {
		t.Fatal("rejected legacy peer changed graph")
	}
}
