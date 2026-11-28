package dataruntime

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStoredContributionDeleteWALFailureAndRetry(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome tarp.Outcome
	}{{"definite rejection", tarp.NotApplied}, {"unknown commit", tarp.Unknown}} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			runtime, executor := newStagedGraphFirstVertexRuntime(t, dir)
			executor.WithTombstoneTTL(time.Hour)
			target := seedStoredContributionDelete(t, executor)
			origin := runtime.clock.NodeID()
			before := executor.LocalSeq(origin)
			request := &pb.DeleteEdgeContributionsRequest{Contributions: []*pb.EdgeContributionKey{target, target}}
			injected := &tarp.OpError{Operation: "apply", Phase: "publish", Outcome: test.outcome, Err: errors.New("injected contribution Delete WAL failure")}
			restore := failStoredContributionDeleteStage(t, runtime, injected)
			if _, err := executor.DeleteEdgeContributions(t.Context(), request); connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("failed local contribution Delete = %v", err)
			}
			restore()
			if got := executor.LocalSeq(origin); got != before || executor.pendingLocalMutation != nil {
				t.Fatalf("failed local stage advanced origin to %d or retained a graph-first pending effect", got)
			}
			assertStoredContributionWeight(t, runtime, 5)
			if test.outcome == tarp.Unknown {
				if _, err := executor.DeleteEdgeContributions(t.Context(), request); connect.CodeOf(err) != connect.CodeFailedPrecondition || !executor.receiptCommitFaulted {
					t.Fatalf("unknown WAL failure allowed a second attempt: %v, faulted=%t", err, executor.receiptCommitFaulted)
				}
				return
			}
			response, err := executor.DeleteEdgeContributions(t.Context(), request)
			if err != nil || response.GetDeleted() != 1 || !slices.Equal(response.GetExisted(), []bool{true, false}) {
				t.Fatalf("retried local contribution Delete = %v, %v", response, err)
			}
			assertStoredContributionWeight(t, runtime, 3)
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			runtime, executor = newStagedGraphFirstVertexRuntime(t, dir)
			if got := executor.LocalSeq(origin); got != before+1 {
				t.Fatalf("restored local origin = %d, want %d", got, before+1)
			}
			assertStoredContributionWeight(t, runtime, 3)
		})
	}
}

func TestStoredReplicatedContributionDeleteWALFailureAndNoOp(t *testing.T) {
	dir := t.TempDir()
	runtime, executor := newStagedGraphFirstVertexRuntime(t, dir)
	executor.WithTombstoneTTL(time.Hour)
	target := seedStoredContributionDelete(t, executor)
	stamp := time.Now()
	peer := hlc.NodeID{0x91}
	mutation := &pb.Mutation{
		Origin: peer[:], Seq: 1, Hlc: newHLC(stamp.UnixNano(), peer),
		TombstoneExpiration: timestamppb.New(stamp.Add(time.Hour)),
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgeContribution{DeleteEdgeContribution: &pb.DeleteEdgeContributionRequest{
			Tail: target.GetTail(), Head: target.GetHead(), ContribId: target.GetContribId(),
		}}},
	}
	injected := &tarp.OpError{Operation: "apply", Phase: "publish", Outcome: tarp.NotApplied, Err: errors.New("injected peer Delete WAL rejection")}
	restore := failStoredContributionDeleteStage(t, runtime, injected)
	if err := executor.ApplyMutation(t.Context(), mutation); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("failed peer contribution Delete = %v", err)
	}
	restore()
	if pending := executor.pendingMutations[peer][1]; pending == nil || pending.applied || pending.walOp != nil || executor.LocalSeq(peer) != 0 {
		t.Fatalf("definite peer failure retained an applied stage: %v", pending)
	}
	assertStoredContributionWeight(t, runtime, 5)
	if err := executor.ApplyMutation(t.Context(), mutation); err != nil {
		t.Fatalf("retried peer contribution Delete = %v", err)
	}
	assertStoredContributionWeight(t, runtime, 3)
	// A different origin's earlier contribution Delete has no accepted graph
	// effect. Its sequenced envelope still advances that origin durably.
	delayed := hlc.NodeID{0x92}
	older := stamp.Add(-time.Second)
	noOp := &pb.Mutation{
		Origin: delayed[:], Seq: 1, Hlc: newHLC(older.UnixNano(), delayed),
		TombstoneExpiration: timestamppb.New(older.Add(time.Hour)),
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgeContributions{DeleteEdgeContributions: &pb.DeleteEdgeContributionsRequest{
			Contributions: []*pb.EdgeContributionKey{target},
		}}},
	}
	if err := executor.ApplyMutation(t.Context(), noOp); err != nil {
		t.Fatalf("peer no-op contribution Delete = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, executor = newStagedGraphFirstVertexRuntime(t, dir)
	if first, second := executor.LocalSeq(peer), executor.LocalSeq(delayed); first != 1 || second != 1 {
		t.Fatalf("restored peer frontiers = %d, %d", first, second)
	}
	assertStoredContributionWeight(t, runtime, 3)
}

func TestStoredContributionDeleteKeepsAppliedPeerEffectDuringRepair(t *testing.T) {
	runtime, executor := newStagedGraphFirstVertexRuntime(t, t.TempDir())
	executor.WithTombstoneTTL(time.Hour)
	seedStoredContributionDelete(t, executor)
	pendingID := graphcache.ContribID{0x83}
	peer := hlc.NodeID{0x94}
	now := time.Now()
	mutation := &pb.Mutation{Origin: peer[:], Seq: 1, Hlc: newHLC(now.UnixNano(), peer), Op: &pb.MutationOp{
		Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge:      &pb.Edge{Tail: "tail", Head: "head", Weight: 7, Expiration: timestamppb.New(now.Add(time.Hour))},
			ContribId: pendingID[:],
		}},
	}}
	original := runtime.tarp.stage
	runtime.tarp.stage = func(context.Context, uint64, *tarp.Snapshot, graphstore.Writer, graphcache.StorageKeys[string], graphcache.StorageRowWriter[string, *pb.Vertex]) error {
		return &tarp.OpError{Operation: "apply", Phase: "publish", Outcome: tarp.NotApplied, Err: errors.New("injected peer Add WAL rejection")}
	}
	if err := executor.ApplyMutation(t.Context(), mutation); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("failed peer Add = %v", err)
	}
	runtime.tarp.stage = original
	pending := executor.pendingMutations[peer][1]
	if pending == nil || !pending.applied || pending.walOp == nil || executor.LocalSeq(peer) != 0 {
		t.Fatalf("peer Add did not leave an applied pending effect: %v", pending)
	}
	assertStoredContributionWeight(t, runtime, 12)
	response, err := executor.DeleteEdgeContribution(t.Context(), &pb.DeleteEdgeContributionRequest{
		Tail: "tail", Head: "head", ContribId: pendingID[:],
	})
	if err != nil || !response.GetExisted() || executor.LocalSeq(peer) != 0 {
		t.Fatalf("Delete missed the pending applied source: %v, %v", response, err)
	}
	if err := executor.ApplyMutation(t.Context(), mutation); err != nil {
		t.Fatalf("repair of the exact older Add = %v", err)
	}
	if executor.LocalSeq(peer) != 1 || executor.publicationFaultCount != 0 {
		t.Fatal("original peer sequence did not repair")
	}
	assertStoredContributionWeight(t, runtime, 5)
}

func seedStoredContributionDelete(t *testing.T, executor *Executor) *pb.EdgeContributionKey {
	t.Helper()
	target, other := graphcache.ContribID{0x81}, graphcache.ContribID{0x82}
	expiration := timestamppb.New(time.Now().Add(time.Hour))
	_, err := executor.AddEdges(t.Context(), &pb.AddEdgesRequest{
		Edges: []*pb.Edge{
			{Tail: "tail", Head: "head", Weight: 2, Expiration: expiration},
			{Tail: "tail", Head: "head", Weight: 3, Expiration: expiration},
		},
		ContribIds: [][]byte{target[:], other[:]},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &pb.EdgeContributionKey{Tail: "tail", Head: "head", ContribId: target[:]}
}

func assertStoredContributionWeight(t *testing.T, runtime *ServingRuntime, expected float32) {
	t.Helper()
	if weight, found := runtime.graph.GetWeight("tail", "head"); !found || weight != expected {
		t.Fatalf("tail -> head = %v (found %t), want %v", weight, found, expected)
	}
}

func failStoredContributionDeleteStage(t *testing.T, runtime *ServingRuntime, failure error) func() {
	t.Helper()
	original := runtime.tarp.stage
	runtime.tarp.stage = func(ctx context.Context, gen uint64, snapshot *tarp.Snapshot, batch graphstore.Writer, keys graphcache.StorageKeys[string], writer graphcache.StorageRowWriter[string, *pb.Vertex]) error {
		if _, ok := writer.(*graphcache.EdgeContributionDeleteTransaction[string, *pb.Vertex]); !ok {
			t.Fatal("contribution Delete bypassed its staged WAL writer")
		}
		if err := original(ctx, gen, snapshot, batch, keys, writer); err != nil {
			return err
		}
		return failure
	}
	return func() { runtime.tarp.stage = original }
}
