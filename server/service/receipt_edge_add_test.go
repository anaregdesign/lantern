package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
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

func TestPublicReceiptEdgeAddPreservesFiniteOverflowResultBits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		weight float32
		bits   uint32
	}{
		{"positive infinity", math.MaxFloat32, 0x7f800000},
		{"negative infinity", -math.MaxFloat32, 0xff800000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, svc, _ := newActivatedReceiptService(t, 8)
			request := publicReceiptEdgeAddRequest(t, runtime, 0x52,
				&pb.Edge{Tail: "overflow", Head: "edge", Weight: tc.weight},
				&pb.Edge{Tail: "overflow", Head: "edge", Weight: tc.weight},
			)
			response, err := svc.AddEdges(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			weights := response.GetEffectiveWeights()
			if len(weights) != 2 || math.Float32bits(weights[0]) != math.Float32bits(tc.weight) ||
				math.Float32bits(weights[1]) != tc.bits {
				t.Fatalf("finite Add sources returned %+v, want %v then result bits %08x", weights, tc.weight, tc.bits)
			}
			snapshot, err := runtime.receipt.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, receipt := range snapshot.Receipts {
				if bytes.Equal(receipt.ID[:], request.GetReceiptContext().GetOperationIds()[1]) {
					found = true
					if len(receipt.Result) != 4 || binary.BigEndian.Uint32(receipt.Result) != tc.bits {
						t.Fatalf("original receipt RESULT = %x, want %08x", receipt.Result, tc.bits)
					}
				}
			}
			if !found {
				t.Fatal("overflow receipt was not committed")
			}
			retry, err := svc.AddEdges(t.Context(), proto.Clone(request).(*pb.AddEdgesRequest))
			if err != nil || len(retry.GetEffectiveWeights()) != 2 ||
				math.Float32bits(retry.GetEffectiveWeights()[1]) != tc.bits {
				t.Fatalf("receipt retry lost original result bits: %+v, %v", retry, err)
			}
		})
	}
}

func TestPublicReceiptEdgeAddPreservesNaNResultFromPriorGraph(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	// Seed a value admitted before source ingress validation was enforced.
	runtime.graph.PutEdgeWithExpiration(
		"legacy", "edge", math.Float32frombits(0x7fc00001), time.Now().Add(time.Hour),
	)
	request := publicReceiptEdgeAddRequest(t, runtime, 0x53,
		&pb.Edge{Tail: "legacy", Head: "edge", Weight: 1},
	)
	response, err := svc.AddEdges(t.Context(), request)
	if err != nil || len(response.GetEffectiveWeights()) != 1 ||
		!math.IsNaN(float64(response.GetEffectiveWeights()[0])) {
		t.Fatalf("finite Add over prior NaN graph = %+v, %v", response, err)
	}
	originalBits := math.Float32bits(response.GetEffectiveWeights()[0])
	snapshot, err := runtime.receipt.store.Snapshot()
	if err != nil || len(snapshot.Receipts) != 1 || len(snapshot.Receipts[0].Result) != 4 ||
		binary.BigEndian.Uint32(snapshot.Receipts[0].Result) != originalBits {
		t.Fatalf("original NaN RESULT bits = %+v, %v, want %08x", snapshot.Receipts, err, originalBits)
	}
	if _, err := svc.PutEdge(t.Context(), &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "legacy", Head: "edge", Weight: 2},
	}); err != nil {
		t.Fatal(err)
	}
	retry, err := svc.AddEdges(t.Context(), proto.Clone(request).(*pb.AddEdgesRequest))
	if err != nil || len(retry.GetEffectiveWeights()) != 1 ||
		math.Float32bits(retry.GetEffectiveWeights()[0]) != originalBits {
		t.Fatalf("retry after finite Put changed authoritative NaN bits: %+v, %v", retry, err)
	}
}

func TestPublicReceiptEdgeAddSurvivesDeleteAndRestart(t *testing.T) {
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipts.wal"))
	install := func(t *testing.T, runtime *ServingRuntime) (*LanternService, *LanternReplicationService) {
		t.Helper()
		primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
		replication, err := runtime.NewLanternReplicationService(primary)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.CertifyInstallation(primary, replication); err != nil {
			t.Fatal(err)
		}
		if err := runtime.CertifyReceiptBackup(primary, replication); err != nil {
			t.Fatal(err)
		}
		if err := runtime.ActivatePublicReceipts(primary, replication); err != nil {
			t.Fatal(err)
		}
		return primary, replication
	}

	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary, _ := install(t, runtime)
	request := publicReceiptEdgeAddRequest(t, runtime, 0x44, &pb.Edge{
		Tail: "restart", Head: "edge", Weight: 7,
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	})
	response, err := primary.AddEdges(context.Background(), proto.Clone(request).(*pb.AddEdgesRequest))
	if err != nil || response.GetEffectiveWeights()[0] != 7 {
		t.Fatalf("initial Add = %+v, %v", response, err)
	}
	if _, err := primary.DeleteEdges(context.Background(), &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: "restart", Head: "edge"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary, _ := install(t, restarted)
	duplicate, err := restartedPrimary.AddEdges(
		context.Background(),
		proto.Clone(request).(*pb.AddEdgesRequest),
	)
	if err != nil || duplicate.GetEffectiveWeights()[0] != 7 {
		t.Fatalf("post-restart duplicate = %+v, %v", duplicate, err)
	}
	if _, live := restarted.graph.GetWeight("restart", "edge"); live {
		t.Fatal("post-restart duplicate re-applied the deleted contribution")
	}
	status, err := restartedPrimary.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: request.GetReceiptContext().GetOperationIds()[0],
	})
	if err != nil || status.GetStatus().GetState() !=
		pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		status.GetStatus().GetReceipt().GetOriginalResult().GetAddEdgeEffectiveWeight() != 7 {
		t.Fatalf("post-restart status = %+v, %v", status, err)
	}
}

func TestPublicReceiptAddEdgeForwardsOneReceiptItem(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	batch := publicReceiptEdgeAddRequest(t, runtime, 0x47, &pb.Edge{
		Tail: "singular", Head: "edge", Weight: 2.5,
	})
	request := &pb.AddEdgeRequest{
		Edge:           batch.GetEdges()[0],
		ContribId:      batch.GetContribIds()[0],
		ReceiptContext: batch.GetReceiptContext(),
	}
	response, err := svc.AddEdge(t.Context(), request)
	if err != nil || response.GetEffectiveWeight() != 2.5 {
		t.Fatalf("AddEdge receipt = %+v, %v", response, err)
	}
	duplicate, err := svc.AddEdge(t.Context(), proto.Clone(request).(*pb.AddEdgeRequest))
	if err != nil || duplicate.GetEffectiveWeight() != 2.5 {
		t.Fatalf("AddEdge receipt duplicate = %+v, %v", duplicate, err)
	}
	if weight, live := runtime.graph.GetWeight("singular", "edge"); !live || weight != 2.5 {
		t.Fatalf("singular duplicate changed graph = (%v, %v)", weight, live)
	}
}

func TestPublicReceiptAddEdgeRejectsInvalidEdge(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	for i, edge := range []*pb.Edge{
		nil,
		{Head: "head", Weight: 1},
		{Tail: "tail", Weight: 1},
		{Tail: "tail", Head: "head", Weight: float32(math.NaN())},
		{Tail: "tail", Head: "head", Weight: float32(math.Inf(1))},
	} {
		batch := publicReceiptEdgeAddRequest(t, runtime, byte(0x48+i), &pb.Edge{
			Tail: "valid", Head: "valid", Weight: 1,
		})
		request := &pb.AddEdgeRequest{
			Edge:           edge,
			ContribId:      batch.GetContribIds()[0],
			ReceiptContext: batch.GetReceiptContext(),
		}
		if _, err := svc.AddEdge(t.Context(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid singular receipt Add %d = %v, want InvalidArgument", i, err)
		}
	}
	unknown := publicReceiptEdgeAddRequest(t, runtime, 0x4f, &pb.Edge{
		Tail: "unknown", Head: "field", Weight: 1,
	})
	request := &pb.AddEdgeRequest{
		Edge:           unknown.GetEdges()[0],
		ContribId:      unknown.GetContribIds()[0],
		ReceiptContext: unknown.GetReceiptContext(),
	}
	request.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
	if _, err := svc.AddEdge(t.Context(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unknown singular receipt Add = %v, want InvalidArgument", err)
	}
	if svc.log.Len() != 0 || runtime.receipt.store.Stats().Entries != 0 {
		t.Fatal("invalid singular receipt Adds changed the log or Store")
	}
	if _, live := runtime.graph.GetWeight("unknown", "field"); live {
		t.Fatal("unknown singular receipt Add changed the graph")
	}
}

func TestPublicReceiptEdgeAddValidationAndConflicts(t *testing.T) {
	t.Run("Explicit ContribID contract", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*pb.AddEdgesRequest)
		}{
			{"invalid operation ID", func(r *pb.AddEdgesRequest) {
				r.ReceiptContext.OperationIds[0] = []byte{1, 2, 3}
			}},
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

	t.Run("Batch bound", func(t *testing.T) {
		runtime, svc, _ := newActivatedReceiptService(t, 16)
		count := receiptVertexWALMaxItems + 1
		request := &pb.AddEdgesRequest{
			Edges:          make([]*pb.Edge, count),
			ContribIds:     make([][]byte, count),
			ReceiptContext: publicReceiptContext(t, runtime, 0x73, 1),
		}
		request.ReceiptContext.OperationIds = make([][]byte, count)
		if _, err := svc.AddEdges(context.Background(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("AddEdges = %v, want InvalidArgument", err)
		}
		if svc.log.Len() != 0 || runtime.receipt.store.Stats().Entries != 0 {
			t.Fatal("oversized request changed log or Store")
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
		newOperationDifferentIntent := proto.Clone(request).(*pb.AddEdgesRequest)
		newOperationDifferentIntent.ReceiptContext = publicReceiptContext(t, runtime, 0x77, 1)
		newOperationDifferentIntent.Edges[0].Head = "different"
		if _, err := svc.AddEdges(context.Background(), newOperationDifferentIntent); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("ContribID reuse with different intent = %v, want InvalidArgument", err)
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
	t.Run("Intrinsic WAL capacity", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x60}, 2, 1<<20)
		coordinator, err := newEdgeAddReceiptCoordinator(f.service, f.coordinator.store)
		if err != nil {
			t.Fatal(err)
		}
		call := receiptEdgeAddTestCall(t, f.epoch, 0x30, &pb.Edge{
			Tail:   "oversized",
			Head:   strings.Repeat("x", receiptVertexWALMaxBytes),
			Weight: 1,
		})
		if _, err := coordinator.Commit(t.Context(), call); connect.CodeOf(err) != connect.CodeResourceExhausted ||
			!errors.Is(err, errReceiptEdgeAddWireCapacity) {
			t.Fatalf("intrinsic capacity rejection = %v, want ResourceExhausted", err)
		}
		if f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 ||
			f.service.LocalSeq(f.service.clock.NodeID()) != 0 {
			t.Fatal("intrinsic capacity rejection changed log, Store, or origin")
		}
	})

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

func TestReceiptEdgeAddFrameAdmissionIsPrePublication(t *testing.T) {
	const itemCount = 16
	type prepared struct {
		fixture     receiptEdgeDeleteFixture
		coordinator *edgeAddReceiptCoordinator
		call        receiptEdgeAddCall
	}
	prepare := func(t *testing.T, node, seed byte) prepared {
		t.Helper()
		f := newReceiptEdgeDeleteFixtureWithLimits(
			t, nil, hlc.NodeID{node}, itemCount*2, 1<<20,
		)
		coordinator, err := newEdgeAddReceiptCoordinator(f.service, f.coordinator.store)
		if err != nil {
			t.Fatal(err)
		}
		edges := make([]*pb.Edge, itemCount)
		for i := range edges {
			tail := string(rune('a'+i)) + strings.Repeat("k", 48)
			edges[i] = &pb.Edge{Tail: tail, Head: "frame", Weight: 1}
			if i%2 == 0 && !f.cache.ApplyEdgeCausalBarrierHLC(
				tail,
				"frame",
				hlc.Timestamp{
					WallNs: time.Now().Add(time.Minute).UnixNano(),
					NodeID: f.service.clock.NodeID(),
				},
			) {
				t.Fatal("cannot install receipt Add causal barrier")
			}
		}
		return prepared{
			fixture:     f,
			coordinator: coordinator,
			call:        receiptEdgeAddTestCall(t, f.epoch, seed, edges...),
		}
	}

	reference := prepare(t, 0x66, 0x41)
	if _, err := reference.coordinator.Commit(t.Context(), reference.call); err != nil {
		t.Fatal(err)
	}
	envelope := reference.fixture.log.RetainedEntries()[0].Op.(*graphAddEffectEnvelope)
	if len(envelope.AcceptedIndexes) != itemCount/2 {
		t.Fatalf("sparse Add accepted indexes = %v", envelope.AcceptedIndexes)
	}
	sparseSize, err := validateReplicationFrameSize(envelope, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximal, err := maximalReplicationRelayEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	maximalSize, err := validateReplicationFrameSize(maximal, 0)
	if err != nil || maximalSize <= sparseSize {
		t.Fatalf("sparse/maximal Add frame sizes = %d/%d, %v", sparseSize, maximalSize, err)
	}

	rejected := prepare(t, 0x67, 0x42)
	rejected.fixture.service.replicationFrameCertified = true
	rejected.fixture.service.replicationSendMaxBytes = maximalSize - 1
	beforeGraph := rejected.fixture.cache.SnapshotReplication()
	canonicalizeReceiptReplicationSnapshot(&beforeGraph)
	_, err = rejected.coordinator.Commit(t.Context(), rejected.call)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("origin Add at maximal max-1 = %v, want ResourceExhausted", err)
	}
	afterGraph := rejected.fixture.cache.SnapshotReplication()
	canonicalizeReceiptReplicationSnapshot(&afterGraph)
	if !reflect.DeepEqual(beforeGraph, afterGraph) {
		t.Fatal("rejected origin Add changed graph or causal state")
	}
	origin := rejected.fixture.service.clock.NodeID()
	if rejected.fixture.log.Len() != 0 ||
		rejected.fixture.service.LocalSeq(origin) != 0 ||
		rejected.fixture.coordinator.store.Stats().Entries != 0 {
		t.Fatal("rejected origin Add changed log, origin, or Store")
	}
	for _, item := range rejected.call.Items {
		status, _, lookupErr := rejected.coordinator.Lookup(item.ID, time.Now())
		if lookupErr != nil || status != mutationreceipt.NotYetObserved {
			t.Fatalf("rejected origin Add status = %v, %v", status, lookupErr)
		}
	}

	exact := prepare(t, 0x68, 0x43)
	exact.fixture.service.replicationFrameCertified = true
	exact.fixture.service.replicationSendMaxBytes = maximalSize
	if _, err := exact.coordinator.Commit(t.Context(), exact.call); err != nil {
		t.Fatalf("origin Add at exact maximal bound: %v", err)
	}

	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	follower := newReceiptEdgeDeleteFixtureWithLimits(
		t, nil, hlc.NodeID{0x69}, itemCount*2, 1<<20,
	)
	followerAdd, err := newEdgeAddReceiptCoordinator(
		follower.service,
		follower.coordinator.store,
	)
	if err != nil {
		t.Fatal(err)
	}
	follower.service.replicationFrameCertified = true
	follower.service.replicationSendMaxBytes = maximalSize - 1
	err = follower.service.ApplyMutation(t.Context(), wire)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("follower Add at maximal max-1 = %v, want ResourceExhausted", err)
	}
	if follower.log.Len() != 0 ||
		follower.service.LocalSeq(envelope.Origin) != 0 ||
		follower.coordinator.store.Stats().Entries != 0 ||
		follower.service.pendingCount != 1 {
		t.Fatalf("rejected follower Add changed publication state: log=%d seq=%d entries=%d pending=%d",
			follower.log.Len(),
			follower.service.LocalSeq(envelope.Origin),
			follower.coordinator.store.Stats().Entries,
			follower.service.pendingCount,
		)
	}
	for _, edge := range envelope.Original {
		if _, live := follower.cache.GetWeight(edge.GetTail(), edge.GetHead()); live {
			t.Fatal("rejected follower Add changed graph")
		}
	}
	status, _, err := followerAdd.Lookup(envelope.Receipts[0].ID, time.Now())
	if err != nil || status != mutationreceipt.NotYetObserved {
		t.Fatalf("rejected follower Add status = %v, %v", status, err)
	}

	follower.service.replicationSendMaxBytes = maximalSize
	if err := follower.service.ApplyMutation(t.Context(), wire); err != nil {
		t.Fatalf("follower Add retry at exact maximal bound: %v", err)
	}
	if follower.log.Len() != 1 ||
		follower.service.LocalSeq(envelope.Origin) != 1 ||
		follower.coordinator.store.Stats().Entries != itemCount ||
		follower.service.pendingCount != 0 {
		t.Fatalf("exact follower Add publication state: log=%d seq=%d entries=%d pending=%d",
			follower.log.Len(),
			follower.service.LocalSeq(envelope.Origin),
			follower.coordinator.store.Stats().Entries,
			follower.service.pendingCount,
		)
	}
	for _, edge := range envelope.Original {
		if weight, live := follower.cache.GetWeight(edge.GetTail(), edge.GetHead()); !live || weight != 1 {
			t.Fatalf("exact follower Add graph = (%v, %v), want (1, true)", weight, live)
		}
	}
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

func TestReceiptEdgeAddCombinedBaselineSuffixRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}

	origin := hlc.NodeID{0x79}
	stamp := hlc.Timestamp{
		WallNs: image.cutoff.WallNs + int64(time.Millisecond),
		NodeID: origin,
	}
	issued := time.Unix(0, stamp.WallNs).Add(-time.Minute)
	id, err := mutationreceipt.NewID(config.Receipt.Epoch, issued, [24]byte{0x79, 1})
	if err != nil {
		t.Fatal(err)
	}
	contribID := graphcache.ContribID{0x79, 1, 0xa5}
	edge := &pb.Edge{
		Tail: "baseline", Head: "suffix", Weight: 6,
		Expiration: timestamppb.New(time.Unix(0, stamp.WallNs).Add(time.Hour)),
	}
	digest, err := receiptEdgeAddDigest(edge, contribID)
	if err != nil {
		t.Fatal(err)
	}
	var receiptContrib mutationreceipt.ContribID
	copy(receiptContrib[:], contribID[:])
	envelope := &graphAddEffectEnvelope{
		Origin: origin, OriginSeq: 1, HLC: stamp,
		Epoch:             config.Receipt.Epoch,
		PolicyFingerprint: runtime.receipt.store.PolicyFingerprint(),
		Original:          []*pb.Edge{edge},
		ContribIDs:        []graphcache.ContribID{contribID},
		AcceptedIndexes:   []uint32{0},
		Receipts: []mutationreceipt.Receipt{{
			Intent: mutationreceipt.Intent{
				ID: id, Group: mutationreceipt.GroupID{0x79}, Index: 0, Count: 1,
				Kind: mutationreceipt.AddEdge, Digest: digest,
				HasContrib: true, ContribID: receiptContrib,
			},
			Result:         receiptEdgeAddResults([]float32{6})[0],
			DeadlineMillis: issued.Add(config.Receipt.Retention).UnixMilli(),
		}},
	}
	envelope.Mutation = receiptEdgeAddMutation(envelope)
	if err := validateGraphAddEffectEnvelope(envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.log.Append(envelope, envelope.HLC); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if weight, live := restarted.graph.GetWeight("baseline", "suffix"); !live || weight != 6 {
		t.Fatalf("combined-baseline Add = (%v, %v), want (6, true)", weight, live)
	}
	status, receipt, err := restarted.receipt.store.Lookup(id, time.Now())
	if err != nil || status != mutationreceipt.Confirmed ||
		!sameReceiptWALDecision(receipt, envelope.Receipts[0]) {
		t.Fatalf("combined-baseline receipt = %v, %+v, %v", status, receipt, err)
	}
}

func TestReceiptEdgeAddReceiptSnapshotAndBackupContinuity(t *testing.T) {
	runtime, primary, replication := newActivatedReceiptService(t, 32)
	request := publicReceiptEdgeAddRequest(t, runtime, 0x7a, &pb.Edge{
		Tail: "snapshot", Head: "edge", Weight: 8,
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	})
	if _, err := primary.AddEdges(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	backup, err := replication.receiptSnapshotSource.CaptureForBackup(
		t.Context(),
		runtime.receipt.policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Generation != runtime.receipt.generation ||
		backup.NodeID != runtime.clock.NodeID() ||
		len(backup.WholeState.Receipts.Receipts) != 1 ||
		backup.WholeState.Receipts.Receipts[0].Kind != mutationreceipt.AddEdge ||
		!backup.WholeState.Receipts.Receipts[0].HasContrib {
		t.Fatalf("backup capture lost Add receipt evidence: %+v", backup)
	}

	recorder := &replicationSnapshotRecorder{}
	if err := replication.Snapshot(t.Context(), &pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
	}, recorder); err != nil {
		t.Fatal(err)
	}
	retiredConfig, _, err := retiredCatalogConfig(
		runtime.receipt.policy,
		backup.WholeState.Receipts.ClockHighWaterMillis,
	)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := DecodeReceiptSnapshotFrames(
		recorder.frames,
		runtime.receipt.policy,
		retiredConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Receipts.Receipts) != 1 ||
		capture.Receipts.Receipts[0].Kind != mutationreceipt.AddEdge ||
		!capture.Receipts.Receipts[0].HasContrib {
		t.Fatalf("RECEIPT Snapshot lost Add evidence: %+v", capture.Receipts)
	}

	id, err := mutationreceipt.DecodeID(request.GetReceiptContext().GetOperationIds()[0])
	if err != nil {
		t.Fatal(err)
	}
	if capture.Receipts.Receipts[0].ID != id ||
		!sameReceiptWALDecision(
			capture.Receipts.Receipts[0],
			backup.WholeState.Receipts.Receipts[0],
		) {
		t.Fatalf("Snapshot/backup Add evidence differs: snapshot=%+v backup=%+v",
			capture.Receipts.Receipts[0],
			backup.WholeState.Receipts.Receipts[0])
	}
}

func TestReceiptEdgeAddRetiredEpochStatusContinuity(t *testing.T) {
	oldConfig := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x91}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20,
	}
	old := newReceiptEdgeDeleteFixtureWithStoreConfig(
		t,
		nil,
		hlc.NodeID{0x91},
		oldConfig,
	)
	oldAdd, err := newEdgeAddReceiptCoordinator(old.service, old.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	call := receiptEdgeAddTestCall(t, old.epoch, 0x51,
		&pb.Edge{Tail: "retired", Head: "edge", Weight: 9},
	)
	if _, err := oldAdd.Commit(t.Context(), call); err != nil {
		t.Fatal(err)
	}

	activeRuntime, active, _ := newActivatedReceiptService(t, 32)
	highWater := max(
		old.coordinator.store.Stats().HighWaterMillis,
		activeRuntime.receipt.store.Stats().HighWaterMillis,
	)
	effective := time.UnixMilli(highWater)
	if _, _, err := old.coordinator.store.ObserveMany(nil, effective); err != nil {
		t.Fatal(err)
	}
	oldState, err := old.coordinator.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	retired := mutationreceipt.RetiredCatalogSnapshot{
		Version:              1,
		ClockHighWaterMillis: highWater,
		Epochs: []mutationreceipt.RetiredEpochSnapshot{{
			Policy: mutationreceipt.RetiredEpochPolicy{
				Epoch: oldConfig.Epoch, Retention: oldConfig.Retention,
				MaxEntries: oldConfig.MaxEntries, MaxBytes: oldConfig.MaxBytes,
			},
			State: oldState,
		}},
	}
	mustReplaceRetiredCatalog(
		t,
		activeRuntime.receipt.retired,
		activeRuntime.receipt.policy,
		highWater,
		retired,
	)
	status, err := active.GetReceiptStatus(t.Context(), &pb.GetReceiptStatusRequest{
		OperationId: call.Items[0].ID.Bytes(),
	})
	if err != nil || status.GetStatus().GetState() !=
		pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		status.GetStatus().GetReceipt().GetOriginalResult().GetAddEdgeEffectiveWeight() != 9 {
		t.Fatalf("retired Add status = %+v, %v", status, err)
	}
}

func TestPublicReceiptEdgeAddBornExpiredLiveResultIsStable(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	request := publicReceiptEdgeAddRequest(t, runtime, 0x7d, &pb.Edge{
		Tail: "expired", Head: "edge", Weight: 11,
		Expiration: timestamppb.New(time.Now().Add(-time.Minute)),
	})
	response, err := svc.AddEdges(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEffectiveWeights()) != 1 || response.GetEffectiveWeights()[0] != 0 {
		t.Fatalf("born-expired Add result = %+v, want live effective weight 0", response)
	}
	if _, live := runtime.graph.GetWeight("expired", "edge"); live {
		t.Fatal("born-expired Add became graph-visible")
	}
	duplicate, err := svc.AddEdges(t.Context(), proto.Clone(request).(*pb.AddEdgesRequest))
	if err != nil || duplicate.GetEffectiveWeights()[0] != 0 {
		t.Fatalf("born-expired duplicate = %+v, %v", duplicate, err)
	}
	status, err := svc.GetReceiptStatus(t.Context(), &pb.GetReceiptStatusRequest{
		OperationId: request.GetReceiptContext().GetOperationIds()[0],
	})
	if err != nil ||
		status.GetStatus().GetReceipt().GetOriginalResult().GetAddEdgeEffectiveWeight() != 0 {
		t.Fatalf("born-expired status = %+v, %v", status, err)
	}
}
