package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A fixed-work diagnostic: 19 ordinary receipt RPCs per eight-item cycle,
// represented either by one mixed call or two homogeneous receipt calls.
// This serial microbenchmark does not qualify offered-load or HA latency SLOs.
func BenchmarkGraphMutationCoexistence(b *testing.B) {
	for _, hot := range []bool{false, true} {
		for _, mixed := range []bool{false, true} {
			b.Run(fmt.Sprintf("hot=%v/mixed=%v", hot, mixed), func(b *testing.B) {
				runtime, svc, _ := newActivatedReceiptServiceWithLimits(b, 8*b.N+8, uint64(8*b.N+8)*4096)
				calls := make([]func() error, b.N)
				ordinary := make([]int64, 0, b.N)
				mixedTimes := make([]int64, 0, b.N/20+1)
				issued := time.Now().Add(-time.Second)
				items := 0
				for i := range calls {
					prefix := fmt.Sprintf("cycle:%d:", i)
					if hot {
						prefix = "hot:"
					}
					vertices := make([]*pb.Vertex, 4)
					edges := make([]*pb.Edge, 4)
					ids := make([][]byte, 4)
					for j := range vertices {
						vertices[j] = &pb.Vertex{Key: fmt.Sprintf("%s%d", prefix, j), Value: &pb.Vertex_String_{String_: "bounded record"}}
						if err := runtime.graph.PutVertex(vertices[j].Key, vertices[j]); err != nil {
							b.Fatal(err)
						}
						edges[j] = &pb.Edge{Tail: vertices[j].Key, Head: fmt.Sprintf("%s%d", prefix, (j+1)%4), Weight: 1}
						ids[j] = make([]byte, 24)
						binary.BigEndian.PutUint64(ids[j][16:], uint64(4*i+j+1))
					}
					identity := receiptBenchmarkContext(b, runtime, issued, uint64(8*i))
					if i%20 != 19 {
						items++
						if i%2 == 0 {
							calls[i] = func() error {
								_, err := svc.PutVertex(b.Context(), &pb.PutVertexRequest{Vertex: vertices[0], ReceiptContext: identity})
								return err
							}
						} else {
							calls[i] = func() error {
								_, err := svc.AddEdge(b.Context(), &pb.AddEdgeRequest{Edge: edges[0], ContribId: ids[0], ReceiptContext: identity})
								return err
							}
						}
						continue
					}
					items += 8
					for j := 1; j < 8; j++ {
						identity.OperationIds = append(identity.OperationIds, receiptBenchmarkContext(b, runtime, issued, uint64(8*i+j)).OperationIds[0])
					}
					if mixed {
						request := &pb.MutateGraphRequest{ReceiptContext: identity}
						for _, v := range vertices {
							request.Items = append(request.Items, &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: v}})
						}
						for j, e := range edges {
							request.Items = append(request.Items, &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: e, ContribId: ids[j]}}})
						}
						calls[i] = func() error { _, err := svc.MutateGraph(b.Context(), request); return err }
					} else {
						putIdentity := proto.Clone(identity).(*pb.MutationReceiptContext)
						putIdentity.OperationIds = putIdentity.OperationIds[:4]
						addIdentity := proto.Clone(identity).(*pb.MutationReceiptContext)
						addIdentity.OperationIds = addIdentity.OperationIds[4:]
						addIdentity.LogicalCallId = receiptBenchmarkContext(b, runtime, issued, uint64(8*i+4)).LogicalCallId
						calls[i] = func() error {
							if _, err := svc.PutVertices(b.Context(), &pb.PutVerticesRequest{Vertices: vertices, ReceiptContext: putIdentity}); err != nil {
								return err
							}
							_, err := svc.AddEdges(b.Context(), &pb.AddEdgesRequest{Edges: edges, ContribIds: ids, ReceiptContext: addIdentity})
							return err
						}
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i, call := range calls {
					start := time.Now()
					if err := call(); err != nil {
						b.Fatal(err)
					}
					elapsed := time.Since(start).Nanoseconds()
					if i%20 == 19 {
						mixedTimes = append(mixedTimes, elapsed)
					} else {
						ordinary = append(ordinary, elapsed)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(items)/b.Elapsed().Seconds(), "items/s")
				if len(ordinary) > 0 {
					slices.Sort(ordinary)
					b.ReportMetric(float64(ordinary[(len(ordinary)-1)*95/100]), "ordinary-p95-ns")
					b.ReportMetric(float64(ordinary[(len(ordinary)-1)*99/100]), "ordinary-p99-ns")
				}
				if len(mixedTimes) > 0 {
					slices.Sort(mixedTimes)
					b.ReportMetric(float64(mixedTimes[len(mixedTimes)/2]), "eight-item-p50-ns")
				}
			})
		}
	}
}

func graphMutationTestRequest(t testing.TB, runtime *ServingRuntime, seed byte) *pb.MutateGraphRequest {
	id := graphcache.ContribID{1}
	return &pb.MutateGraphRequest{ReceiptContext: publicReceiptContext(t, runtime, seed, 3), Items: []*pb.GraphMutationItem{
		{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "first"}}}},
		{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: &pb.Vertex{Key: "b", Value: &pb.Vertex_String_{String_: "second"}}}},
		{Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 2}, ContribId: id[:]}}},
	}}
}

func TestGraphMutationPublicOneDecision(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 16)
	req := graphMutationTestRequest(t, runtime, 0x51)
	before := runtime.log.Len()
	response, err := svc.MutateGraph(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetResults()) != 3 || response.Results[0].GetPutVertex() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE || response.Results[2].GetAddEdgeEffectiveWeight() != 2 {
		t.Fatal(response)
	}
	if weight, ok := runtime.graph.GetWeight("a", "b"); !ok || weight != 2 {
		t.Fatal(weight, ok)
	}
	if runtime.log.Len() != before+1 || svc.LocalSeq(svc.clock.NodeID()) != 1 {
		t.Fatal("not one WAL/origin decision")
	}
	if err := runtime.graph.PutVertex("a", &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "later"}}); err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.MutateGraph(t.Context(), proto.Clone(req).(*pb.MutateGraphRequest))
	if err != nil || !proto.Equal(response, duplicate) || runtime.log.Len() != before+1 {
		t.Fatal(duplicate, err)
	}
	statuses, err := svc.GetReceiptStatuses(t.Context(), &pb.GetReceiptStatusesRequest{OperationIds: req.ReceiptContext.OperationIds})
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range statuses.GetStatuses() {
		r := status.GetReceipt()
		if r == nil || r.GetItemIndex() != uint32(i) || r.GetItemCount() != 3 {
			t.Fatal(status)
		}
	}
	changed := proto.Clone(req).(*pb.MutateGraphRequest)
	changed.Items[2].GetAddEdge().Edge.Weight = 3
	if _, err := svc.MutateGraph(t.Context(), changed); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
}

func TestGraphMutationMaximumItemsAndPreflightBytes(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 128)
	request := graphMutationTestRequest(t, runtime, 0x68)
	request.ReceiptContext = publicReceiptContext(t, runtime, 0x68, 64)
	for i := 1; i < 62; i++ {
		item := proto.Clone(request.Items[2]).(*pb.GraphMutationItem)
		item.GetAddEdge().ContribId[0] = byte(i + 1)
		request.Items = append(request.Items, item)
	}
	if vertices, err := validateGraphMutationItems(request.Items, ""); err != nil || vertices != 2 {
		t.Fatal("64-item shape rejected", vertices, err)
	}
	before := runtime.graph.SnapshotReplication()
	response, err := svc.MutateGraph(t.Context(), request)
	wantEntries, wantDecisions := 64, 1
	if err != nil {
		// The item limit is not a latency guarantee. Race/coverage overhead
		// may exhaust the unchanged pre-WAL budget; that must abort everything.
		if connect.CodeOf(err) != connect.CodeResourceExhausted || !errors.Is(err, graphcache.ErrGraphMutationBudget) || response != nil {
			t.Fatal(response, err)
		}
		if !reflect.DeepEqual(before, runtime.graph.SnapshotReplication()) {
			t.Fatal("budget rejection published graph state")
		}
		wantEntries, wantDecisions = 0, 0
	} else if len(response.GetResults()) != 64 || response.Results[63].GetAddEdgeEffectiveWeight() != 124 {
		t.Fatal(response)
	}
	if runtime.log.Len() != wantDecisions || runtime.receipt.store.Stats().Entries != wantEntries || svc.LocalSeq(svc.clock.NodeID()) != uint64(wantDecisions) {
		t.Fatal("maximum-size call published an incomplete decision")
	}
	t.Logf("64-item call: decisions=%d receipts=%d", wantDecisions, wantEntries)
	extra := proto.Clone(request.Items[2]).(*pb.GraphMutationItem)
	extra.GetAddEdge().ContribId[0] = 64
	request.Items = append(request.Items, extra)
	request.ReceiptContext = publicReceiptContext(t, runtime, 0x69, 65)
	if _, err := validateGraphMutationItems(request.Items, ""); err == nil {
		t.Fatal("65-item shape accepted")
	}
	if _, err := svc.MutateGraph(t.Context(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("65 items not rejected during preflight", err)
	}
	request = graphMutationTestRequest(t, runtime, 0x6a)
	request.Items[0].GetPutVertex().Value = &pb.Vertex_Bytes{Bytes: make([]byte, graphMutationMaxBytes)}
	if _, err := svc.MutateGraph(t.Context(), request); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("oversized request not rejected during preflight", err)
	}
	if runtime.log.Len() != wantDecisions || runtime.receipt.store.Stats().Entries != wantEntries {
		t.Fatal("preflight rejection published")
	}
}

func TestGraphMutationPublicRejectsBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*pb.MutateGraphRequest)
	}{
		{"missing receipt", func(r *pb.MutateGraphRequest) { r.ReceiptContext = nil }},
		{"missing endpoint", func(r *pb.MutateGraphRequest) { r.Items[2].GetAddEdge().Edge.Head = "missing" }},
		{"implicit contribution", func(r *pb.MutateGraphRequest) { r.Items[2].GetAddEdge().ContribId = nil }},
		{"duplicate vertex", func(r *pb.MutateGraphRequest) { r.Items[1].GetPutVertex().Key = "a" }},
		{"interleaved", func(r *pb.MutateGraphRequest) { r.Items[1], r.Items[2] = r.Items[2], r.Items[1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, svc, _ := newActivatedReceiptService(t, 16)
			r := graphMutationTestRequest(t, runtime, 0x52)
			tc.edit(r)
			if _, err := svc.MutateGraph(t.Context(), r); err == nil {
				t.Fatal("accepted")
			}
			if runtime.graph.VertexCount() != 0 || runtime.log.Len() != 0 || runtime.receipt.store.Stats().Entries != 0 {
				t.Fatal("rejection published state")
			}
		})
	}
}

func TestGraphMutationDefiniteAbort(t *testing.T) {
	var attempts []mutationlog.Entry
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(e mutationlog.Entry) error {
		attempts = append(attempts, e)
		return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected")}
	}))
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	before := f.cache.SnapshotReplication()
	_, err := f.service.MutateGraph(t.Context(), graphMutationTestRequest(t, runtime, 0x53))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	after := f.cache.SnapshotReplication()
	if !reflect.DeepEqual(before, after) || f.coordinator.store.Stats().Entries != 0 || f.log.Len() != 0 {
		t.Fatal("abort leaked state")
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts=%d", len(attempts))
	}
	if _, ok := attempts[0].Op.(*graphMutationEnvelope); !ok {
		t.Fatalf("WAL type %T", attempts[0].Op)
	}
}

func TestGraphMutationCancellationDuringWALCannotAbortCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		cancel()
		return nil
	}))
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	request := graphMutationTestRequest(t, runtime, 0x66)
	response, err := f.service.MutateGraph(ctx, request)
	if err != nil || len(response.GetResults()) != 3 || f.log.Len() != 1 || runtime.receipt.store.Stats().Entries != 3 {
		t.Fatal("successful WAL was rolled back after cancellation", response, err)
	}
	if weight, live := f.cache.GetWeight("a", "b"); !live || weight != 2 {
		t.Fatal("committed graph incomplete", weight, live)
	}
}

func TestGraphMutationAuthorizationNamespaceBlindAndLifecycle(t *testing.T) {
	clock, contexts := dataAccessFixture(t, []security.PermissionRule{
		dataAccessRule("read-tail", security.Allow, security.VertexRead, "tails:"),
		dataAccessRule("write-tail", security.Allow, security.VertexWrite, "tails:"),
		dataAccessRule("write-head", security.Allow, security.VertexWrite, "heads:"),
		dataAccessRule("receipts", security.Allow, security.ReceiptRead, ""),
	})
	f := newReceiptEdgeDeleteFixture(t, nil)
	now := clock()
	f.service.WithDataNamespace().WithDataAuthorization(func() time.Time { return now })
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	if err := f.cache.PutVertex("data:heads:b", &pb.Vertex{Key: "data:heads:b"}); err != nil {
		t.Fatal(err)
	}
	id := graphcache.ContribID{9}
	req := &pb.MutateGraphRequest{ReceiptContext: publicReceiptContext(t, runtime, 0x61, 2), Items: []*pb.GraphMutationItem{
		{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: &pb.Vertex{Key: "tails:a", Value: &pb.Vertex_String_{String_: "data:literal"}}}},
		{Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: &pb.Edge{Tail: "tails:a", Head: "heads:b", Weight: 2}, ContribId: id[:]}}},
	}}
	handler := &lanternServiceConnect{svc: f.service}
	ctx := contexts("reader", now)
	denied := proto.Clone(req).(*pb.MutateGraphRequest)
	denied.Items[0].GetPutVertex().Key = "outside:x"
	if _, err := handler.MutateGraph(ctx, connect.NewRequest(denied)); connect.CodeOf(err) != connect.CodePermissionDenied || f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 {
		t.Fatal("unauthorized member observed/published", err)
	}
	response, err := handler.MutateGraph(ctx, connect.NewRequest(req))
	if err != nil || response.Msg.GetAcceptance() == nil || len(response.Msg.GetResults()) != 0 {
		t.Fatal("blind whole-call effect leaked", response, err)
	}
	if v, live := f.cache.GetVertex("data:tails:a"); !live || v.GetString_() != "data:literal" {
		t.Fatal("mapped value/key drift", v, live)
	}
	if _, live := f.cache.GetVertex("tails:a"); live {
		t.Fatal("logical key bypassed namespace")
	}
	e := f.log.RetainedEntries()[0].Op.(*graphMutationEnvelope)
	if e.Receipts[0].Resource.Key != "tails:a" || e.Receipts[1].Resource.Head != "heads:b" {
		t.Fatal("receipt provenance is not logical")
	}
	reduced := proto.Clone(req).(*pb.MutateGraphRequest)
	reduced.ReceiptContext = publicReceiptContext(t, runtime, 0x71, 2)
	reduced.Items[0].GetPutVertex().Expiration = timestamppb.New(time.Now().Add(time.Minute))
	reduced.Items[1].GetAddEdge().ContribId[0]++
	if _, err := handler.MutateGraph(ctx, connect.NewRequest(reduced)); connect.CodeOf(err) != connect.CodePermissionDenied || f.log.Len() != 1 {
		t.Fatal("lifecycle reduction escaped", err)
	}
	// A stale admission cannot disclose even the same call's handled result.
	_, err = dataUnary(ctx, connect.NewRequest(req), f.service, func(c context.Context, r *pb.MutateGraphRequest) (*pb.MutateGraphResponse, error) {
		response, err := f.service.MutateGraph(c, r)
		now = now.Add(2 * time.Hour)
		return response, err
	})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("expired output authority disclosed", err)
	}
}
