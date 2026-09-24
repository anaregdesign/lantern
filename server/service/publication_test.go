package service

import (
	"context"
	"encoding/hex"
	"errors"
	"sync/atomic"
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

type heldPublicationWAL struct{ failed atomic.Bool }

func (w *heldPublicationWAL) Write(mutationlog.Entry) error {
	if w.failed.Load() {
		return errors.New("injected persistent WAL failure")
	}
	return nil
}

func TestPublishLocalMutation_FaultBlocksLaterWritesAndRepairsOriginal(t *testing.T) {
	ctx := context.Background()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	wal := &heldPublicationWAL{}
	wal.failed.Store(true)
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("local")
	svc := NewLanternService(cache).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	first := &pb.PutVerticesRequest{IfAbsent: true, Vertices: []*pb.Vertex{{
		Key: "first", Value: &pb.Vertex_String_{String_: "original"}, Expiration: futureTs(time.Minute),
	}}}
	if _, err := svc.PutVertices(ctx, first); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("first PutVertices error = %v, want Unavailable", err)
	}
	if value, ok := cache.GetVertex("first"); !ok || value.GetString_() != "original" {
		t.Fatalf("first graph effect = (%v,%v), want original", value, ok)
	}
	if svc.pendingLocalMutation == nil || svc.pendingLocalMutation.mutation.GetSeq() != 1 {
		t.Fatalf("pending local mutation = %v, want seq 1", svc.pendingLocalMutation)
	}
	if got := svc.LocalSeq(origin); got != 0 {
		t.Fatalf("fault advanced origin seq to %d", got)
	}
	if got := log.Len(); got != 0 {
		t.Fatalf("fault published %d entries", got)
	}
	if err := svc.ApplySnapshotWatermarks(map[string]uint64{hex.EncodeToString(origin[:]): 1}, hlc.Timestamp{}); err == nil {
		t.Fatal("snapshot watermark skipped an unpublished local mutation")
	}
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"first"}}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("write while repair fails = %v, want Unavailable", err)
	}
	if _, ok := cache.GetVertex("first"); !ok {
		t.Fatal("later Delete changed graph before repair")
	}
	first.Vertices[0] = &pb.Vertex{Key: "caller-mutated", Value: &pb.Vertex_String_{String_: "caller-mutated"}}
	wal.failed.Store(false)
	if err := svc.ApplyMutation(ctx, cloneQueuedMutation(svc.pendingLocalMutation.mutation)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("remote self-echo crossed local publication fault: %v", err)
	}
	response, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{IfAbsent: true, Vertices: []*pb.Vertex{{
		Key: "first", Value: &pb.Vertex_String_{String_: "replacement"}, Expiration: futureTs(time.Minute),
	}}})
	if err != nil {
		t.Fatalf("repairing conditional Put: %v", err)
	}
	if got := response.GetOutcomes(); len(got) != 1 || got[0] != pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET {
		t.Fatalf("retry outcomes = %v, want CONDITION_NOT_MET", got)
	}
	if value, ok := cache.GetVertex("first"); !ok || value.GetString_() != "original" {
		t.Fatalf("repair reapplied conditional Put: (%v,%v)", value, ok)
	}
	if svc.pendingLocalMutation != nil || svc.publicationFaultCount != 0 {
		t.Fatal("repair left local publication fault set")
	}
	if got := svc.LocalSeq(origin); got != 1 {
		t.Fatalf("repaired origin seq = %d, want 1", got)
	}
	entries, cancel, err := log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cancel() }()
	mutation := (<-entries).Op.(*pb.Mutation)
	live := mutation.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive()
	if mutation.GetSeq() != 1 || live.GetKey() != "first" || live.GetString_() != "original" {
		t.Fatalf("repaired original mutation = %v", mutation)
	}
}

func TestPublishLocalMutation_BornExpiredPutKeepsBarrier(t *testing.T) {
	ctx := context.Background()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	wal := &failOncePublicationWAL{}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("local")
	svc := NewLanternService(cache).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
		Key: "expired", Expiration: futureTs(-time.Second),
	}}})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("born-expired Put error = %v, want Unavailable", err)
	}
	if _, ok := cache.GetVertex("expired"); ok {
		t.Fatal("born-expired Put became live")
	}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "next"}}}); err != nil {
		t.Fatalf("repair and next Put: %v", err)
	}
	if got := svc.LocalSeq(origin); got != 2 {
		t.Fatalf("origin seq = %d, want 2", got)
	}
	entries, cancel, err := log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cancel() }()
	first := (<-entries).Op.(*pb.Mutation)
	if first.GetSeq() != 1 || first.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetCausalBarrier().GetKey() != "expired" {
		t.Fatalf("repaired born-expired mutation = %v", first)
	}
	second := (<-entries).Op.(*pb.Mutation)
	if second.GetSeq() != 2 || second.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive().GetKey() != "next" {
		t.Fatalf("next mutation = %v", second)
	}
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
