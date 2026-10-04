package service

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateEdgesAtomicOutcomesAndValidation(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 32)
	ctx := context.Background()
	future := timestamppb.New(time.Now().Add(time.Hour))
	_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "a", Expiration: future}, {Key: "b", Expiration: future}}})
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.graph.SnapshotVertices()
	edges := []*pb.Edge{{Tail: "a", Head: "b", Weight: 2}, {Tail: "a", Head: "b", Weight: 9}, {Tail: "a", Head: "missing", Weight: 2}, {Tail: "x", Head: "y", Weight: 1, Expiration: timestamppb.New(time.Now().Add(-time.Minute))}}
	req := &pb.CreateEdgesRequest{Edges: edges, ReceiptContext: publicReceiptContext(t, runtime, 0x71, len(edges))}
	first, err := svc.CreateEdges(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	want := []pb.CreateEdgeOutcome{pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EDGE_EXISTS, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_ENDPOINT_NOT_LIVE, pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EXPIRED}
	if !reflect.DeepEqual(first.GetOutcomes(), want) {
		t.Fatal(first)
	}
	if runtime.graph.VertexCount() != len(before) {
		t.Fatal("Create fabricated Vertices")
	}
	for _, v := range before {
		current, found := runtime.graph.GetVertex(v.Key)
		if !found || !proto.Equal(current, v.Value) {
			t.Fatal("endpoint changed")
		}
	}
	if _, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.CreateEdges(ctx, proto.Clone(req).(*pb.CreateEdgesRequest))
	if err != nil || !proto.Equal(first, duplicate) {
		t.Fatal(duplicate, err)
	}
	if _, found := runtime.graph.GetWeight("a", "b"); found {
		t.Fatal("receipt retry resurrected deleted Edge")
	}
	status, err := svc.GetReceiptStatus(ctx, &pb.GetReceiptStatusRequest{OperationId: req.ReceiptContext.OperationIds[0]})
	if err != nil || status.GetStatus().GetReceipt().GetOriginalResult().GetCreateEdgeOutcome() != want[0] {
		t.Fatal(status, err)
	}
	for _, weight := range []float32{0, float32(math.NaN()), float32(math.Inf(1))} {
		_, err := svc.CreateEdges(ctx, &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1}, {Tail: "b", Head: "a", Weight: weight}}})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("invalid source accepted", weight, err)
		}
		if _, live := runtime.graph.GetWeight("a", "b"); live {
			t.Fatal("invalid batch partially committed")
		}
	}
	empty, err := svc.CreateEdges(ctx, nil)
	if err != nil || len(empty.GetOutcomes()) != 0 {
		t.Fatal(empty, err)
	}
	single, err := svc.CreateEdge(ctx, &pb.CreateEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 3}})
	if err != nil || single.GetOutcome() != want[0] {
		t.Fatal(single, err)
	}
}

func TestCreateEdgesHAAndWALAbortFailClosed(t *testing.T) {
	t.Run("HA disables creation before storage", func(t *testing.T) {
		_, svc, _ := newActivatedReceiptService(t, 16)
		svc.WithEdgeCreateHA()
		_, err := svc.CreateEdges(t.Context(), &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1}}})
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal(err)
		}
		cap, err := svc.GetReceiptCapability(t.Context(), &pb.GetReceiptCapabilityRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range cap.GetSupportedMutations() {
			if kind == pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_CREATE_EDGE {
				t.Fatal("HA advertised unsupported Create")
			}
		}
	})
	t.Run("definite WAL abort restores graph receipts and origin", func(t *testing.T) {
		abort := errors.New("injected definite abort")
		f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(entry mutationlog.Entry) error { return &mutationlog.DefiniteWALAbort{Cause: abort} }))
		runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
		for _, key := range []string{"a", "b"} {
			f.cache.PutVertex(key, &pb.Vertex{Key: key})
		}
		req := &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1}}, ReceiptContext: publicReceiptContext(t, runtime, 0x31, 1)}
		_, err := f.service.CreateEdges(t.Context(), req)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal(err)
		}
		if _, found := f.cache.GetWeight("a", "b"); found {
			t.Fatal("aborted Create changed graph")
		}
		if f.coordinator.store.Stats().Entries != 0 || f.service.origins.LocalSeq(f.service.clock.NodeID()) != 0 {
			t.Fatal("aborted Create leaked receipt/origin")
		}
	})
	t.Run("unstaged Backend rejected", func(t *testing.T) {
		svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
		if _, err := svc.CreateEdge(t.Context(), &pb.CreateEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1}}); err == nil {
			t.Fatal("unstaged storage admitted Create")
		}
	})
}
