package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type failOncePublicationWAL struct{ writes int }

func (w *failOncePublicationWAL) Write(mutationlog.Entry) error {
	w.writes++
	if w.writes == 1 {
		return errors.New("injected WAL failure")
	}
	return nil
}

func TestPublishRemoteMutation_FailedAppendRetriesWithoutDoubleApply(t *testing.T) {
	// fakeBackend's AddEdge is deliberately non-idempotent, so a repeated
	// backend call after the WAL failure would visibly double the weight.
	cache := newFakeBackend()
	wal := &failOncePublicationWAL{}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	local, origin := bytes16("local"), bytes16("remote")
	svc := NewLanternService(cache).WithReplication(log, hlc.New(local, hlc.Options{}), nil)
	exp := futureTs(time.Hour)
	m := &pb.Mutation{Seq: 1, Origin: origin[:], Hlc: newHLC(1, origin), Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
		AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 2, Expiration: exp}},
	}}}
	if code := connect.CodeOf(svc.ApplyMutation(context.Background(), m)); code != connect.CodeUnavailable {
		t.Fatalf("failed append code = %v, want Unavailable", code)
	}
	if got := svc.LocalSeq(origin); got != 0 {
		t.Fatalf("failed append advanced cursor to %d", got)
	}
	if got := log.Len(); got != 0 {
		t.Fatalf("failed append published %d entries", got)
	}
	if weight := cache.edges["a"]["b"]; weight != 2 {
		t.Fatalf("first graph apply weight = %v, want 2", weight)
	}
	if err := svc.ApplyMutation(context.Background(), m); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := svc.LocalSeq(origin); got != 1 {
		t.Fatalf("retried cursor = %d, want 1", got)
	}
	if got := log.Len(); got != 1 {
		t.Fatalf("retried log len = %d, want 1", got)
	}
	if weight := cache.edges["a"]["b"]; weight != 2 {
		t.Fatalf("retry applied Add twice: weight=%v", weight)
	}
}

func TestPublishRemoteMutation_BoundedFutureAndConflictingDuplicate(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewLanternService(cache)
	origin := bytes16("remote")
	makeMutation := func(seq uint64, key string) *pb.Mutation {
		return &pb.Mutation{Seq: seq, Origin: origin[:], Hlc: newHLC(int64(seq), origin), Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}},
		}}}
	}
	far := makeMutation(maxPendingSeqGap+1, "far")
	if code := connect.CodeOf(svc.ApplyMutation(context.Background(), far)); code != connect.CodeResourceExhausted {
		t.Fatalf("unbounded gap code = %v, want ResourceExhausted", code)
	}
	if got := svc.pendingCount; got != 0 {
		t.Fatalf("rejected gap retained %d pending entries", got)
	}
	if _, ok := cache.GetVertex("far"); ok {
		t.Fatal("rejected future entry changed graph")
	}
	buffered := makeMutation(2, "one")
	if err := svc.ApplyMutation(context.Background(), buffered); err != nil {
		t.Fatal(err)
	}
	buffered.GetOp().GetPutVertex().Vertex.Key = "caller-mutated"
	if code := connect.CodeOf(svc.ApplyMutation(context.Background(), makeMutation(2, "conflict"))); code != connect.CodeFailedPrecondition {
		t.Fatalf("conflicting duplicate code = %v, want FailedPrecondition", code)
	}
	if err := svc.ApplyMutation(context.Background(), makeMutation(1, "first")); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.GetVertex("one"); !ok {
		t.Fatal("original buffered mutation did not commit")
	}
	if _, ok := cache.GetVertex("conflict"); ok {
		t.Fatal("conflicting duplicate changed graph")
	}
	if _, ok := cache.GetVertex("caller-mutated"); ok {
		t.Fatal("queued mutation retained caller-owned mutable payload")
	}
}

func TestPublishRemoteMutation_GlobalPendingBudgetRejectsBeforeApply(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewLanternService(cache)
	origin := bytes16("budget")
	m := &pb.Mutation{Seq: 2, Origin: origin[:], Hlc: newHLC(2, origin), Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
		PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "budget-key"}},
	}}}
	for _, budget := range []struct {
		name  string
		count int
		bytes int
	}{
		{"count", maxPendingMutations, 0},
		{"bytes", 0, maxPendingBytes - 1},
	} {
		t.Run(budget.name, func(t *testing.T) {
			svc.pendingCount, svc.pendingBytes = budget.count, budget.bytes
			if code := connect.CodeOf(svc.ApplyMutation(context.Background(), m)); code != connect.CodeResourceExhausted {
				t.Fatalf("pending budget code = %v, want ResourceExhausted", code)
			}
			if len(svc.pendingMutations) != 0 {
				t.Fatal("rejected mutation allocated a queue")
			}
			if _, ok := cache.GetVertex("budget-key"); ok {
				t.Fatal("rejected mutation changed graph")
			}
		})
	}
}
