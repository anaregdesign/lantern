package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func receiptEdgeAddTestCall(
	t *testing.T,
	epoch mutationreceipt.Epoch,
	seed byte,
	edges ...*pb.Edge,
) receiptEdgeAddCall {
	t.Helper()
	issued := time.Now().Add(-time.Minute)
	call := receiptEdgeAddCall{
		Group: mutationreceipt.GroupID{seed},
		Items: make([]receiptEdgeAddItem, len(edges)),
	}
	for i, edge := range edges {
		id, err := mutationreceipt.NewID(
			epoch,
			issued,
			[24]byte{seed, byte(i + 1)},
		)
		if err != nil {
			t.Fatal(err)
		}
		call.Items[i] = receiptEdgeAddItem{
			ID: id, Edge: edge,
			ContribID: graphcache.ContribID{seed, byte(i + 1), 0xa5},
		}
	}
	return call
}

func publicReceiptEdgeAddRequest(
	t *testing.T,
	runtime *ServingRuntime,
	seed byte,
	edges ...*pb.Edge,
) *pb.AddEdgesRequest {
	t.Helper()
	contribIDs := make([][]byte, len(edges))
	for i := range edges {
		id := graphcache.ContribID{seed, byte(i + 1), 0x5a}
		contribIDs[i] = append([]byte(nil), id[:]...)
	}
	return &pb.AddEdgesRequest{
		Edges: edges, ContribIds: contribIDs,
		ReceiptContext: publicReceiptContext(t, runtime, seed, len(edges)),
	}
}

func TestPublicReceiptEdgeAddReturnsOriginalResultAcrossDeleteAndRetry(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 16)
	ctx := context.Background()
	if _, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{{
		Tail: "tail", Head: "head", Weight: 3,
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	}}}); err != nil {
		t.Fatal(err)
	}
	request := publicReceiptEdgeAddRequest(t, runtime, 0x41, &pb.Edge{
		Tail: "tail", Head: "head", Weight: 2,
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	})
	first, err := svc.AddEdges(ctx, proto.Clone(request).(*pb.AddEdgesRequest))
	if err != nil {
		t.Fatal(err)
	}
	if first.GetWritten() != 1 || len(first.GetEffectiveWeights()) != 1 ||
		first.GetEffectiveWeights()[0] != 5 {
		t.Fatalf("first Add = %+v, want effective weight 5", first)
	}
	duplicate, err := svc.AddEdges(ctx, proto.Clone(request).(*pb.AddEdgesRequest))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.GetWritten() != 1 || duplicate.GetEffectiveWeights()[0] != 5 {
		t.Fatalf("duplicate Add = %+v, want original effective weight 5", duplicate)
	}
	if weight, live := runtime.graph.GetWeight("tail", "head"); !live || weight != 5 {
		t.Fatalf("duplicate changed graph = (%v, %v), want (5, true)", weight, live)
	}
	if _, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: "tail", Head: "head"}},
	}); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := svc.AddEdges(ctx, proto.Clone(request).(*pb.AddEdgesRequest))
	if err != nil {
		t.Fatal(err)
	}
	if afterDelete.GetEffectiveWeights()[0] != 5 {
		t.Fatalf("retry after Delete = %+v, want original effective weight 5", afterDelete)
	}
	if _, live := runtime.graph.GetWeight("tail", "head"); live {
		t.Fatal("retry after Delete re-applied the contribution")
	}
	status, err := svc.GetReceiptStatus(ctx, &pb.GetReceiptStatusRequest{
		OperationId: request.GetReceiptContext().GetOperationIds()[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := status.GetStatus().GetReceipt().GetOriginalResult().GetResult().(*pb.ReceiptResult_AddEdgeEffectiveWeight)
	if !ok || result.AddEdgeEffectiveWeight != 5 {
		t.Fatalf("status result = %+v, want Add effective weight 5", status)
	}
}

func TestPublicReceiptEdgeAddValidationAndConflicts(t *testing.T) {
	t.Run("Explicit ContribID contract", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*pb.AddEdgesRequest)
		}{
			{"missing", func(r *pb.AddEdgesRequest) { r.ContribIds = nil }},
			{"mixed", func(r *pb.AddEdgesRequest) { r.ContribIds[1] = nil }},
			{"wrong size", func(r *pb.AddEdgesRequest) { r.ContribIds[0] = []byte{1, 2, 3} }},
			{"zero", func(r *pb.AddEdgesRequest) { r.ContribIds[0] = make([]byte, 24) }},
			{"duplicate", func(r *pb.AddEdgesRequest) { r.ContribIds[1] = append([]byte(nil), r.ContribIds[0]...) }},
		}
		for i, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				runtime, svc, _ := newActivatedReceiptService(t, 16)
				request := publicReceiptEdgeAddRequest(t, runtime, byte(0x50+i*3),
					&pb.Edge{Tail: "a", Head: "b", Weight: 1},
					&pb.Edge{Tail: "c", Head: "d", Weight: 1},
				)
				test.mutate(request)
				if _, err := svc.AddEdges(context.Background(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("AddEdges = %v, want InvalidArgument", err)
				}
				if _, live := runtime.graph.GetWeight("a", "b"); live || svc.log.Len() != 0 ||
					runtime.receipt.store.Stats().Entries != 0 {
					t.Fatal("rejected request changed graph, log, or Store")
				}
			})
		}
	})

	t.Run("Duplicate operation IDs", func(t *testing.T) {
		runtime, svc, _ := newActivatedReceiptService(t, 16)
		request := publicReceiptEdgeAddRequest(t, runtime, 0x71,
			&pb.Edge{Tail: "a", Head: "b", Weight: 1},
			&pb.Edge{Tail: "c", Head: "d", Weight: 1},
		)
		request.ReceiptContext.OperationIds[1] = append(
			[]byte(nil),
			request.ReceiptContext.OperationIds[0]...,
		)
		if _, err := svc.AddEdges(context.Background(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("AddEdges = %v, want InvalidArgument", err)
		}
	})

	t.Run("Operation and contribution reuse", func(t *testing.T) {
		runtime, svc, _ := newActivatedReceiptService(t, 16)
		request := publicReceiptEdgeAddRequest(t, runtime, 0x75,
			&pb.Edge{Tail: "a", Head: "b", Weight: 1},
		)
		if _, err := svc.AddEdges(context.Background(), proto.Clone(request).(*pb.AddEdgesRequest)); err != nil {
			t.Fatal(err)
		}
		changedIntent := proto.Clone(request).(*pb.AddEdgesRequest)
		changedIntent.Edges[0].Weight = 9
		if _, err := svc.AddEdges(context.Background(), changedIntent); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("operation intent reuse = %v, want InvalidArgument", err)
		}
		newOperation := proto.Clone(request).(*pb.AddEdgesRequest)
		newOperation.ReceiptContext = publicReceiptContext(t, runtime, 0x76, 1)
		if _, err := svc.AddEdges(context.Background(), newOperation); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("ContribID reuse = %v, want InvalidArgument", err)
		}
		if weight, live := runtime.graph.GetWeight("a", "b"); !live || weight != 1 {
			t.Fatalf("conflicts changed graph = (%v, %v)", weight, live)
		}
	})

	t.Run("Receipt-less remains direct", func(t *testing.T) {
		_, svc, _ := newActivatedReceiptService(t, 16)
		valid := graphcache.ContribID{1}
		response, err := svc.AddEdges(context.Background(), &pb.AddEdgesRequest{
			Edges: []*pb.Edge{
				{Tail: "a", Head: "b", Weight: 1},
				{Tail: "c", Head: "d", Weight: 2},
			},
			ContribIds: [][]byte{valid[:], nil},
		})
		if err != nil || response.GetWritten() != 2 {
			t.Fatalf("receipt-less Add = %+v, %v", response, err)
		}
	})
}

func TestReceiptEdgeAddCapacityAndWALFailureArePreMutation(t *testing.T) {
	t.Run("Store capacity", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x61}, 1, 1<<20)
		coordinator, err := newEdgeAddReceiptCoordinator(f.service, f.coordinator.store)
		if err != nil {
			t.Fatal(err)
		}
		call := receiptEdgeAddTestCall(t, f.epoch, 0x31,
			&pb.Edge{Tail: "a", Head: "b", Weight: 1},
			&pb.Edge{Tail: "c", Head: "d", Weight: 1},
		)
		if _, err := coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("capacity rejection = %v, want ResourceExhausted", err)
		}
		if _, live := f.cache.GetWeight("a", "b"); live || f.log.Len() != 0 ||
			f.coordinator.store.Stats().Entries != 0 {
			t.Fatal("capacity rejection changed graph, log, or Store")
		}
	})

	t.Run("Definite WAL abort", func(t *testing.T) {
		writes := 0
		f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
			writes++
			if writes == 1 {
				return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
			}
			return nil
		}))
		coordinator, err := newEdgeAddReceiptCoordinator(f.service, f.coordinator.store)
		if err != nil {
			t.Fatal(err)
		}
		f.cache.AddEdgeWithExpiration("a", "b", 10, time.Now().Add(time.Hour))
		call := receiptEdgeAddTestCall(t, f.epoch, 0x32,
			&pb.Edge{Tail: "a", Head: "b", Weight: 2},
		)
		if _, err := coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("definite WAL abort = %v, want Unavailable", err)
		}
		if weight, live := f.cache.GetWeight("a", "b"); !live || weight != 10 ||
			f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 ||
			f.service.receiptCommitFaulted {
			t.Fatalf("definite abort leaked state: weight/live=%v/%v log=%d entries=%d faulted=%v",
				weight, live, f.log.Len(), f.coordinator.store.Stats().Entries,
				f.service.receiptCommitFaulted)
		}
		response, err := coordinator.Commit(context.Background(), call)
		if err != nil {
			t.Fatal(err)
		}
		if response.GetEffectiveWeights()[0] != 12 {
			t.Fatalf("retry result = %+v, want 12", response)
		}
		if weight, live := f.cache.GetWeight("a", "b"); !live || weight != 12 {
			t.Fatalf("retry graph = (%v, %v), want (12, true)", weight, live)
		}
	})
}

func TestReceiptEdgeAddFollowerUsesLocalProjectionAndRelaysEvidence(t *testing.T) {
	source := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x71}, 16, 1<<20)
	sourceAdd, err := newEdgeAddReceiptCoordinator(source.service, source.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	call := receiptEdgeAddTestCall(t, source.epoch, 0x41,
		&pb.Edge{Tail: "tail", Head: "head", Weight: 4,
			Expiration: timestamppb.New(time.Now().Add(time.Hour))},
	)
	response, err := sourceAdd.Commit(context.Background(), call)
	if err != nil || response.GetEffectiveWeights()[0] != 4 {
		t.Fatalf("source commit = %+v, %v", response, err)
	}
	sourceEnvelope := source.log.RetainedEntries()[0].Op.(*graphAddEffectEnvelope)
	wire, err := sourceEnvelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}

	follower := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x72}, 16, 1<<20)
	followerAdd, err := newEdgeAddReceiptCoordinator(follower.service, follower.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := followerAdd.Lookup(call.Items[0].ID, time.Now())
	if err != nil || status != mutationreceipt.NotYetObserved {
		t.Fatalf("unconverged status = %v, %v, want NotYetObserved", status, err)
	}
	newer := sourceEnvelope.HLC
	newer.WallNs++
	newer.NodeID = follower.service.clock.NodeID()
	follower.cache.DeleteEdgesHLCDecisions(
		[]graphcache.EdgeKey[string]{{Tail: "tail", Head: "head"}},
		newer,
		time.Now().Add(time.Hour),
	)
	if err := follower.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatal(err)
	}
	if _, live := follower.cache.GetWeight("tail", "head"); live {
		t.Fatal("follower applied Add below its local Delete floor")
	}
	status, receipt, err := followerAdd.Lookup(call.Items[0].ID, time.Now())
	if err != nil || status != mutationreceipt.Confirmed || len(receipt.Result) != 4 {
		t.Fatalf("converged follower status = %v, %+v, %v", status, receipt, err)
	}
	relay := follower.log.RetainedEntries()[0].Op.(*graphAddEffectEnvelope)
	if len(relay.AcceptedIndexes) != 0 ||
		relay.Receipts[0].Result[0] != sourceEnvelope.Receipts[0].Result[0] {
		t.Fatalf("relay lost local rejection or origin result: %+v", relay)
	}
	relayWire, err := relay.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	if relayWire.GetOp().GetReplicatedReceiptEdgeAdd().GetItems()[0].GetCausallyAccepted() {
		t.Fatal("relay advertised origin acceptance instead of receiver-local rejection")
	}

	third := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x73}, 16, 1<<20)
	thirdAdd, err := newEdgeAddReceiptCoordinator(third.service, third.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.service.ApplyMutation(context.Background(), relayWire); err != nil {
		t.Fatal(err)
	}
	if weight, live := third.cache.GetWeight("tail", "head"); !live || weight != 4 {
		t.Fatalf("third-hop receiver did not recompute acceptance = (%v, %v)", weight, live)
	}
	status, receipt, err = thirdAdd.Lookup(call.Items[0].ID, time.Now())
	if err != nil || status != mutationreceipt.Confirmed ||
		!sameReceiptWALDecision(receipt, sourceEnvelope.Receipts[0]) {
		t.Fatalf("third-hop receipt = %v, %+v, %v", status, receipt, err)
	}
}
