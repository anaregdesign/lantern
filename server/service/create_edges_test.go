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
	"github.com/anaregdesign/lantern/core/mutationreceipt"
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

func TestCreateEdgesPublicationLifecycle(t *testing.T) {
	for _, withReceipt := range []bool{false, true} {
		name := "without receipt"
		if withReceipt {
			name = "with receipt"
		}
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{
				"definite abort and retry",
				"indeterminate failure",
				"WAL panic",
				"cancellation before WAL",
				"cancellation during successful WAL",
			} {
				t.Run(scenario, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					cause := errors.New("injected Create publication failure")
					writes := 0
					f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
						writes++
						if writes > 1 {
							return nil
						}
						switch scenario {
						case "definite abort and retry":
							return &mutationlog.DefiniteWALAbort{Cause: cause}
						case "indeterminate failure":
							return cause
						case "WAL panic":
							panic(cause)
						case "cancellation during successful WAL":
							cancel()
						}
						return nil
					}))
					runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
					for _, key := range []string{"a", "b"} {
						if err := f.cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
							t.Fatal(err)
						}
					}
					req := &pb.CreateEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 2}}}
					if withReceipt {
						req.ReceiptContext = publicReceiptContext(t, runtime, 0x39, 1)
					}
					type result struct {
						response *pb.CreateEdgesResponse
						err      error
						panic    any
					}
					invoke := func() (got result) {
						defer func() { got.panic = recover() }()
						got.response, got.err = f.service.CreateEdges(ctx, req)
						return got
					}
					var got result
					if scenario == "cancellation before WAL" {
						f.service.receiptOriginCutMu.Lock()
						cutHeld := true
						t.Cleanup(func() {
							if cutHeld {
								f.service.receiptOriginCutMu.Unlock()
							}
						})
						done := make(chan result, 1)
						go func() { done <- invoke() }()
						deadline := time.Now().Add(time.Second)
						for f.service.replicationCutMu.TryRLock() {
							f.service.replicationCutMu.RUnlock()
							if time.Now().After(deadline) {
								t.Fatal("Create did not reach the publication cut")
							}
							time.Sleep(time.Millisecond)
						}
						cancel()
						f.service.receiptOriginCutMu.Unlock()
						cutHeld = false
						got = waitReceiptTest(t, "canceled Create", done)
					} else {
						got = invoke()
					}

					if scenario == "cancellation during successful WAL" {
						if got.panic != nil || got.err != nil || len(got.response.GetOutcomes()) != 1 ||
							got.response.GetOutcomes()[0] != pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE {
							t.Fatalf("committed Create after cancellation = %+v", got)
						}
						if weight, live := f.cache.GetWeight("a", "b"); !live || weight != 2 {
							t.Fatalf("committed graph = (%v, %v)", weight, live)
						}
						wantReceipts := 0
						if withReceipt {
							wantReceipts = 1
						}
						if writes != 1 || f.log.Len() != 1 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 ||
							f.coordinator.store.Stats().Entries != wantReceipts || f.service.receiptCommitFaulted {
							t.Fatal("cancellation changed the committed publication cut")
						}
						return
					}

					wantFault := scenario == "indeterminate failure" || scenario == "WAL panic"
					wantWrites := 1
					switch scenario {
					case "WAL panic":
						if got.panic != cause {
							t.Fatalf("Create panic = %v, want original panic", got.panic)
						}
					case "cancellation before WAL":
						wantWrites = 0
						if got.panic != nil || connect.CodeOf(got.err) != connect.CodeCanceled {
							t.Fatalf("canceled Create = %+v", got)
						}
					default:
						if got.panic != nil || connect.CodeOf(got.err) != connect.CodeUnavailable || !errors.Is(got.err, cause) {
							t.Fatalf("failed Create = %+v", got)
						}
						if errors.Is(got.err, mutationlog.ErrWALIndeterminate) != wantFault {
							t.Fatalf("WAL classification = %v", got.err)
						}
					}
					if _, live := f.cache.GetWeight("a", "b"); live || writes != wantWrites || f.log.Len() != 0 ||
						f.service.LocalSeq(f.service.clock.NodeID()) != 0 || f.coordinator.store.Stats().Entries != 0 {
						t.Fatal("failed Create leaked graph, receipts, origin, or log state")
					}
					if f.service.receiptCommitFaulted != wantFault {
						t.Fatalf("service fail-stop = %t, want %t", f.service.receiptCommitFaulted, wantFault)
					}
					generation, faulted := f.service.publicationStatus()
					if faulted != wantFault {
						t.Fatalf("publication fault = %t, want %t", faulted, wantFault)
					}
					select {
					case <-generation:
						if !wantFault {
							t.Fatal("definitely uncommitted Create closed the publication generation")
						}
					default:
						if wantFault {
							t.Fatal("uncertain Create left the publication generation open")
						}
					}
					if withReceipt {
						id, err := mutationreceipt.DecodeID(req.ReceiptContext.OperationIds[0])
						if err != nil {
							t.Fatal(err)
						}
						status, _, err := f.coordinator.Lookup(id, time.Now())
						if wantFault {
							if connect.CodeOf(err) != connect.CodeFailedPrecondition {
								t.Fatalf("receipt lookup after uncertain Create = %v", err)
							}
						} else if err != nil || status != mutationreceipt.NotYetObserved {
							t.Fatalf("receipt after rollback = (%v, %v)", status, err)
						}
					}
					if wantFault {
						if _, err := f.service.CreateEdges(t.Context(), req); connect.CodeOf(err) != connect.CodeFailedPrecondition {
							t.Fatalf("Create after uncertain publication = %v", err)
						}
						if err := f.replication.Snapshot(t.Context(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
							t.Fatalf("Snapshot after uncertain Create = %v", err)
						}
					} else if scenario == "definite abort and retry" {
						retry, err := f.service.CreateEdges(t.Context(), req)
						if err != nil || len(retry.GetOutcomes()) != 1 || retry.GetOutcomes()[0] != pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE ||
							writes != 2 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 || f.log.Len() != 1 {
							t.Fatalf("retry after definite abort = (%+v, %v), WAL writes %d", retry, err, writes)
						}
					}
				})
			}
		})
	}
}
