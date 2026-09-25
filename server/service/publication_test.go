package service

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestSnapshotInstall_FailedReplayKeepsCDCGapUntilVerifiedRetry(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	oldGeneration, faulted := svc.publicationStatus()
	if faulted {
		t.Fatal("fresh service started with a CDC gap")
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BeginSnapshotInstall(); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("concurrent Snapshot install error = %v", err)
	}
	select {
	case <-oldGeneration:
	default:
		t.Fatal("Snapshot install did not gap existing CDC generation")
	}
	finish(false)
	if _, faulted := svc.publicationStatus(); !faulted {
		t.Fatal("failed Snapshot replay cleared its CDC gap")
	}
	retry, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	retry(true)
	newGeneration, faulted := svc.publicationStatus()
	if faulted || newGeneration == oldGeneration {
		t.Fatal("verified Snapshot replay did not create a healthy CDC generation")
	}
	select {
	case <-newGeneration:
		t.Fatal("verified Snapshot replay left new CDC generation closed")
	default:
	}
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
	pendingEffect := svc.pendingLocalMutation.walOp
	if _, ok := pendingEffect.(*graphPutEffectEnvelope); !ok {
		t.Fatalf("pending local WAL evidence = %T, want graph Put effect", pendingEffect)
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
	repairedEntry := <-entries
	if repairedEntry.Op != pendingEffect {
		t.Fatal("repair replaced the exact retained WAL envelope")
	}
	mutation := mustGraphMutation(t, repairedEntry.Op)
	live := mutation.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive()
	if mutation.GetSeq() != 1 || live.GetKey() != "first" || live.GetString_() != "original" {
		t.Fatalf("repaired original mutation = %v", mutation)
	}
}

func TestPublishLocalDeleteRepairKeepsOriginalDeadline(t *testing.T) {
	ctx := context.Background()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	cache.PutVertexWithExpiration("victim", &pb.Vertex{Key: "victim"}, time.Now().Add(time.Hour))
	wal := &heldPublicationWAL{}
	wal.failed.Store(true)
	log := mutationlog.New(mutationlog.Options{Capacity: 8, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("delete-repair")
	svc := NewLanternService(cache).WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"victim"}}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Delete WAL failure = %v, want Unavailable", err)
	}
	pending := svc.pendingLocalMutation
	tombstones := cache.SnapshotReplication().Tombstones.Vertices
	if pending == nil || pending.mutation.GetTombstoneExpiration() == nil || len(tombstones) != 1 ||
		!pending.mutation.GetTombstoneExpiration().AsTime().Equal(tombstones[0].Expiration) {
		t.Fatalf("pending Delete deadline differs from applied tombstone: pending=%v tombstones=%+v", pending, tombstones)
	}
	wal.failed.Store(false)
	if _, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "repair"}}); err != nil {
		t.Fatalf("repairing next write: %v", err)
	}
	entries, cancel, err := log.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	repaired := mustGraphMutation(t, (<-entries).Op)
	if repaired.GetSeq() != 1 || !repaired.GetTombstoneExpiration().AsTime().Equal(tombstones[0].Expiration) {
		t.Fatalf("repaired Delete renewed deadline: %v", repaired)
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
	first := mustGraphMutation(t, (<-entries).Op)
	if first.GetSeq() != 1 || first.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetCausalBarrier().GetKey() != "expired" {
		t.Fatalf("repaired born-expired mutation = %v", first)
	}
	second := mustGraphMutation(t, (<-entries).Op)
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
	pending := svc.pendingMutations[origin][1]
	if pending == nil {
		t.Fatal("failed remote append did not retain pending mutation")
	}
	pendingEffect := pending.walOp
	if _, ok := pendingEffect.(*graphAddEffectEnvelope); !ok {
		t.Fatalf("pending remote WAL evidence = %T, want graph Add effect", pendingEffect)
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
	if entry := mustMutationLogEntry(t, log, 1); entry.Op != pendingEffect {
		t.Fatal("remote retry replaced the exact retained WAL envelope")
	}
}

func TestPublishRemoteMutationAdvancesClockAfterContiguousCommit(t *testing.T) {
	base := time.Now()
	local, origin := bytes16("local-clock"), bytes16("remote-clock")
	clock := hlc.New(local, hlc.Options{Now: func() int64 { return base.UnixNano() }})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewLanternService(cache).WithReplication(log, clock, nil)
	mutation := func(seq uint64, stamp hlc.Timestamp, key string) *pb.Mutation {
		return &pb.Mutation{
			Seq: seq, Origin: origin[:], Hlc: hlcToProto(stamp),
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
				Vertex: &pb.Vertex{Key: key, Expiration: timestamppb.New(base.Add(time.Hour))},
			}}},
		}
	}
	firstStamp := hlc.Timestamp{WallNs: base.Add(100 * time.Millisecond).UnixNano(), Logical: 1, NodeID: origin}
	secondStamp := hlc.Timestamp{WallNs: base.Add(200 * time.Millisecond).UnixNano(), Logical: 2, NodeID: origin}
	if err := svc.ApplyMutation(context.Background(), mutation(2, secondStamp, "second")); err != nil {
		t.Fatal(err)
	}
	if queuedClock := clock.Now(); !queuedClock.Less(secondStamp) {
		t.Fatalf("queued remote mutation advanced clock to %v before publication", queuedClock)
	}
	if err := svc.ApplyMutation(context.Background(), mutation(1, firstStamp, "first")); err != nil {
		t.Fatal(err)
	}
	if got := svc.LocalSeq(origin); got != 2 {
		t.Fatalf("contiguous remote frontier = %d, want 2", got)
	}
	if next := clock.Now(); !secondStamp.Less(next) {
		t.Fatalf("local clock %v did not advance beyond published remote HLC %v", next, secondStamp)
	}
	futureStamp := hlc.Timestamp{
		WallNs: base.Add(2 * hlc.DefaultMaxSkew).UnixNano(),
		NodeID: origin,
	}
	if err := svc.ApplyMutation(context.Background(), mutation(3, futureStamp, "graph-only-future")); err != nil {
		t.Fatalf("graph-only future HLC changed behavior: %v", err)
	}
	if next := clock.Now(); !next.Less(futureStamp) {
		t.Fatalf("graph-only clock %v did not preserve skew clamping below %v", next, futureStamp)
	}
}

func TestPublishRemoteMutationDurableClockFloorAfterWallRollback(t *testing.T) {
	base := time.Now()
	highWater := base.Add(hlc.DefaultMaxSkew).Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: highWater,
	}
	origin := hlc.NodeID{0x58}
	f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x59}, config,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	stamp := hlc.Timestamp{
		WallNs:  highWater.Add(hlc.DefaultMaxSkew / 2).UnixNano(),
		Logical: 7,
		NodeID:  origin,
	}
	future := timestamppb.New(time.Unix(0, stamp.WallNs).Add(time.Hour))
	first := &pb.Mutation{
		Seq: 1, Origin: origin[:], Hlc: hlcToProto(stamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "rollback-put", Expiration: future},
		}}},
	}
	lastStamp := stamp
	lastStamp.Logical++
	second := &pb.Mutation{
		Seq: 2, Origin: origin[:], Hlc: hlcToProto(lastStamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "rollback", Head: "add", Weight: 1, Expiration: future},
		}}},
	}
	if err := f.service.ApplyMutation(context.Background(), second); err != nil {
		t.Fatalf("queue durable remote mutation after wall rollback: %v", err)
	}
	if got := f.coordinator.store.Stats().HighWaterMillis; got != highWater.UnixMilli() {
		t.Fatalf("queued remote mutation advanced Store high-water to %d, want %d", got, highWater.UnixMilli())
	}
	hostile := lastStamp
	hostile.WallNs = highWater.Add(hlc.DefaultMaxSkew + time.Nanosecond).UnixNano()
	hostile.Logical++
	err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
		Seq: 3, Origin: origin[:], Hlc: hlcToProto(hostile),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "hostile-future", Expiration: future},
		}}},
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("hostile future HLC after queued frame = %v, want InvalidArgument", err)
	}
	if err := f.service.ApplyMutation(context.Background(), first); err != nil {
		t.Fatalf("drain durable remote mutations after wall rollback: %v", err)
	}
	if got := f.coordinator.store.Stats().HighWaterMillis; got < lastStamp.WallNs/int64(time.Millisecond) {
		t.Fatalf("committed remote mutation left Store high-water at %d, want at least %d", got, lastStamp.WallNs/int64(time.Millisecond))
	}
	states := f.service.OriginStates()
	if len(states) != 1 || states[0].Origin != origin || states[0].LastSeq != 2 ||
		states[0].LastHLC != lastStamp {
		t.Fatalf("durable remote origin state = %+v", states)
	}

	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source(context.Background(), config)
	if err != nil {
		t.Fatalf("receipt capture after generic remote publication: %v", err)
	}
	if len(capture.Graph) == 0 || capture.Graph[0].GetHeader() == nil {
		t.Fatalf("receipt capture has no header: %+v", capture.Graph)
	}
	cutoff := hlcFromProto(capture.Graph[0].GetHeader().GetCutoffHlc())
	if !lastStamp.Less(cutoff) {
		t.Fatalf("receipt capture cutoff %v did not exceed generic origin HLC %v", cutoff, lastStamp)
	}
}

func TestPublishRemoteMutationDurablePreflightPreservesClockHighWater(t *testing.T) {
	base := time.Now().Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: base,
	}
	f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x69}, config,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	future := timestamppb.New(base.Add(time.Hour))
	invalidExpiration := &timestamppb.Timestamp{Seconds: 253402300800}
	tests := []struct {
		name   string
		origin hlc.NodeID
		op     *pb.MutationOp
	}{
		{
			name:   "nil singular payload",
			origin: hlc.NodeID{0x70},
			op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{},
			}},
		},
		{
			name:   "empty plural payload",
			origin: hlc.NodeID{0x71},
			op: &pb.MutationOp{Op: &pb.MutationOp_AddEdges{
				AddEdges: &pb.AddEdgesRequest{},
			}},
		},
		{
			name:   "invalid expiration",
			origin: hlc.NodeID{0x72},
			op: &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
				PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{
					Tail: "invalid", Head: "expiration", Expiration: invalidExpiration,
				}},
			}},
		},
		{
			name:   "invalid contribution identity",
			origin: hlc.NodeID{0x73},
			op: &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
				AddEdge: &pb.AddEdgeRequest{
					Edge:      &pb.Edge{Tail: "invalid", Head: "contribution", Expiration: future},
					ContribId: []byte{1},
				},
			}},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stamp := hlc.Timestamp{
				WallNs:  base.Add(time.Duration(i+1) * 50 * time.Millisecond).UnixNano(),
				Logical: uint32(i + 1),
				NodeID:  tt.origin,
			}
			err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
				Seq: 1, Origin: tt.origin[:], Hlc: hlcToProto(stamp), Op: tt.op,
			})
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid durable mutation error = %v, want InvalidArgument", err)
			}
			if got := f.coordinator.store.Stats().HighWaterMillis; got != base.UnixMilli() {
				t.Fatalf("invalid durable mutation advanced Store high-water to %d, want %d", got, base.UnixMilli())
			}
			if got := f.service.LocalSeq(tt.origin); got != 0 {
				t.Fatalf("invalid durable mutation advanced origin to %d", got)
			}
			if pending := f.service.pendingMutations[tt.origin]; len(pending) != 0 {
				t.Fatalf("invalid durable mutation remained queued: %+v", pending)
			}
		})
	}
	if got := f.log.Len(); got != 0 {
		t.Fatalf("invalid durable mutations appended %d relay rows", got)
	}
	if _, live := f.cache.GetWeight("invalid", "expiration"); live {
		t.Fatal("invalid durable mutation changed the graph")
	}
	if _, err := f.service.AddEdges(context.Background(), &pb.AddEdgesRequest{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty local durable AddEdges error = %v, want InvalidArgument", err)
	}
	if _, err := f.service.AddEdge(context.Background(), &pb.AddEdgeRequest{
		Edge:      &pb.Edge{Tail: "local", Head: "invalid-contribution", Expiration: future},
		ContribId: []byte{1},
	}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid local durable contribution identity error = %v, want InvalidArgument", err)
	}
	if got := f.log.Len(); got != 0 {
		t.Fatalf("invalid local durable mutations appended %d relay rows", got)
	}

	origin := hlc.NodeID{0x74}
	stamp := hlc.Timestamp{
		WallNs:  base.Add(250 * time.Millisecond).UnixNano(),
		Logical: 5,
		NodeID:  origin,
	}
	if err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
		Seq: 1, Origin: origin[:], Hlc: hlcToProto(stamp),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "valid", Expiration: future}},
		}},
	}); err != nil {
		t.Fatalf("valid durable mutation after rejected frames: %v", err)
	}
	if got := f.coordinator.store.Stats().HighWaterMillis; got < stamp.WallNs/int64(time.Millisecond) {
		t.Fatalf("valid durable mutation left Store high-water at %d, want at least %d", got, stamp.WallNs/int64(time.Millisecond))
	}
	if _, live := f.cache.GetVertex("valid"); !live {
		t.Fatal("valid durable mutation did not change the graph")
	}
	if got := f.service.LocalSeq(origin); got != 1 {
		t.Fatalf("valid durable mutation advanced origin to %d, want 1", got)
	}
	if got := f.log.Len(); got != 1 {
		t.Fatalf("valid durable mutation appended %d relay rows, want 1", got)
	}
}

func TestGraphOnlyPublicationPreservesEmptyAddBatch(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = log.Close() })
	service := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).
		WithReplication(log, hlc.New(hlc.NodeID{0x75}, hlc.Options{}), nil)
	if _, err := service.AddEdges(context.Background(), &pb.AddEdgesRequest{}); err != nil {
		t.Fatalf("graph-only empty AddEdges: %v", err)
	}
	if got := log.Len(); got != 1 {
		t.Fatalf("graph-only empty AddEdges appended %d rows, want 1", got)
	}
}

func TestPublishRemoteMutationDurableRevalidatesOwnedQueuedFrame(t *testing.T) {
	base := time.Now().Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: base,
	}
	f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x76}, config,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	origin := hlc.NodeID{0x77}
	future := timestamppb.New(base.Add(time.Hour))
	mutation := func(seq uint64, offset time.Duration, key string) *pb.Mutation {
		stamp := hlc.Timestamp{
			WallNs: base.Add(offset).UnixNano(), Logical: uint32(seq), NodeID: origin,
		}
		return &pb.Mutation{
			Seq: seq, Origin: origin[:], Hlc: hlcToProto(stamp),
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Expiration: future}},
			}},
		}
	}
	if err := f.service.ApplyMutation(context.Background(), mutation(2, 200*time.Millisecond, "second")); err != nil {
		t.Fatalf("queue future durable mutation: %v", err)
	}
	queued := f.service.pendingMutations[origin][2]
	if queued == nil {
		t.Fatal("future durable mutation was not queued")
	}
	queued.mutation.GetOp().GetPutVertex().Vertex = nil

	err := f.service.ApplyMutation(context.Background(), mutation(1, 100*time.Millisecond, "first"))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("owned queued revalidation error = %v, want InvalidArgument", err)
	}
	firstStamp := base.Add(100 * time.Millisecond).UnixMilli()
	if got := f.coordinator.store.Stats().HighWaterMillis; got != firstStamp {
		t.Fatalf("rejected queued frame advanced Store high-water to %d, want %d", got, firstStamp)
	}
	if got := f.service.LocalSeq(origin); got != 1 {
		t.Fatalf("rejected queued frame advanced origin to %d, want 1", got)
	}
	if got := f.log.Len(); got != 1 {
		t.Fatalf("rejected queued frame appended %d relay rows, want 1", got)
	}
	if _, live := f.cache.GetVertex("first"); !live {
		t.Fatal("first contiguous frame did not commit")
	}
	if _, live := f.cache.GetVertex("second"); live {
		t.Fatal("rejected queued frame changed the graph")
	}
	if pending := f.service.pendingMutations[origin][2]; pending != queued {
		t.Fatal("rejected queued frame did not retain exact pending evidence")
	}
}

func TestLocalGraphWritesPublishEffectCompleteEnvelopes(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 16, SubscriberBuffer: 16})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("local-effects")
	svc := NewLanternService(cache).WithTombstoneTTL(time.Hour).
		WithReplication(log, hlc.New(origin, hlc.Options{}), nil)
	ctx := context.Background()
	future := timestamppb.New(time.Now().Add(time.Hour))
	past := timestamppb.New(time.Now().Add(-time.Hour))

	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "vertex-duplicate", Expiration: future},
		{Key: "vertex-duplicate", Expiration: past},
	}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if _, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "put", Head: "duplicate", Weight: 1, Expiration: future},
		{Tail: "put", Head: "duplicate", Weight: 1, Expiration: past},
	}}); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}
	add, err := svc.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "add-0", Head: "head", Weight: 1, Expiration: future},
		nil,
		{Tail: "add-2", Head: "head", Weight: 2, Expiration: future},
	}})
	if err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	if add.GetWritten() != 2 || !slices.Equal(add.GetEffectiveWeights(), []float32{1, 0, 2}) {
		t.Fatalf("nil-slot Add response = %+v", add)
	}
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"absent", "absent"}}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if _, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "missing", Head: "edge"}, {Tail: "missing", Head: "edge"},
	}}); err != nil {
		t.Fatalf("DeleteEdges: %v", err)
	}

	for seq, want := range []struct {
		kind     string
		indexes  []uint32
		putKinds []graphPutEffectKind
	}{
		{"put", []uint32{0, 1}, []graphPutEffectKind{graphPutEffectLive, graphPutEffectBarrier}},
		{"put", []uint32{0, 1}, []graphPutEffectKind{graphPutEffectLive, graphPutEffectBarrier}},
		{"add", []uint32{0, 2}, nil},
		{"delete", []uint32{0, 1}, nil},
		{"delete", []uint32{0, 1}, nil},
	} {
		entry := mustMutationLogEntry(t, log, uint64(seq+1))
		var got []uint32
		switch effect := entry.Op.(type) {
		case *graphPutEffectEnvelope:
			if want.kind != "put" {
				t.Fatalf("entry %d = %T, want %s effect", seq+1, entry.Op, want.kind)
			}
			for _, accepted := range effect.Accepted {
				got = append(got, accepted.Index)
			}
			kinds := make([]graphPutEffectKind, len(effect.Accepted))
			for i, accepted := range effect.Accepted {
				kinds[i] = accepted.Kind
			}
			if !slices.Equal(kinds, want.putKinds) {
				t.Fatalf("entry %d Put effect kinds = %v, want %v", seq+1, kinds, want.putKinds)
			}
		case *graphAddEffectEnvelope:
			if want.kind != "add" {
				t.Fatalf("entry %d = %T, want %s effect", seq+1, entry.Op, want.kind)
			}
			got = effect.AcceptedIndexes
		case *graphDeleteEffectEnvelope:
			if want.kind != "delete" {
				t.Fatalf("entry %d = %T, want %s effect", seq+1, entry.Op, want.kind)
			}
			got = effect.AcceptedIndexes
		default:
			t.Fatalf("entry %d = %T, want effect-complete envelope", seq+1, entry.Op)
		}
		if !slices.Equal(got, want.indexes) {
			t.Fatalf("entry %d accepted indexes = %v, want %v", seq+1, got, want.indexes)
		}
	}

	var addZero, addTwo bool
	for _, edge := range cache.SnapshotEdges() {
		if len(edge.Contributions) != 1 {
			continue
		}
		switch edge.Tail {
		case "add-0":
			addZero = edge.Contributions[0].ContribID == contribIDFor(origin[:], 3, 0)
		case "add-2":
			addTwo = edge.Contributions[0].ContribID == contribIDFor(origin[:], 3, 2)
		}
	}
	if !addZero || !addTwo {
		t.Fatal("local Add nil-slot synthesis did not retain original wire indexes")
	}
}

func TestProductionGraphPublicationStrictRestartWithoutTombstones(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publication.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	local := bytes16("local-restart")
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	svc := NewLanternService(cache).WithReplication(log, hlc.New(local, hlc.Options{}), nil)
	ctx := context.Background()
	future := timestamppb.New(time.Now().Add(time.Hour))

	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "keep", Expiration: future}, {Key: "remove", Expiration: future},
	}}); err != nil {
		t.Fatalf("local Put: %v", err)
	}
	if _, err := svc.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{{
		Tail: "local-add", Head: "head", Weight: 2, Expiration: future,
	}}}); err != nil {
		t.Fatalf("local Add: %v", err)
	}
	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"remove"}}); err != nil {
		t.Fatalf("local Delete: %v", err)
	}

	remote := bytes16("remote-restart")
	remoteOps := []*pb.MutationOp{
		{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{
			Edge: &pb.Edge{Tail: "remote-put", Head: "head", Weight: 3, Expiration: future},
		}}},
		{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "remote-add", Head: "head", Weight: 4, Expiration: future},
		}}},
		{Op: &pb.MutationOp_DeleteEdge{DeleteEdge: &pb.DeleteEdgeRequest{Tail: "remote-put", Head: "head"}}},
	}
	for i, op := range remoteOps {
		seq := uint64(i + 1)
		if err := svc.ApplyMutation(ctx, &pb.Mutation{
			Origin: remote[:], Seq: seq,
			Hlc: &pb.HLCTimestamp{WallNs: time.Now().UnixNano() + int64(seq), NodeId: remote[:]},
			Op:  op,
		}); err != nil {
			t.Fatalf("remote mutation %d: %v", seq, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		rows++
		switch entry.Op.(type) {
		case *graphPutEffectEnvelope, *graphAddEffectEnvelope, *graphDeleteEffectEnvelope:
			return nil
		default:
			return errors.New("production graph publication wrote a raw WAL row")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 6 {
		t.Fatalf("WAL rows = %d, want 6", rows)
	}

	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{1}, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	}
	candidate, err := stageEffectCompleteReceiptWALCandidate(
		lease, config, time.Now(), mutationlog.Options{Capacity: 32}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil },
	)
	if err != nil {
		t.Fatalf("strict restart: %v", err)
	}
	if last, ok := candidate.log.LastSeq(); !ok || last != 6 {
		t.Fatalf("strict restart log frontier = %d, %v", last, ok)
	}
	if got := candidate.origins.LocalSeq(local); got != 3 {
		t.Fatalf("strict restart local origin = %d, want 3", got)
	}
	if got := candidate.origins.LocalSeq(remote); got != 3 {
		t.Fatalf("strict restart remote origin = %d, want 3", got)
	}
	if _, ok := candidate.graph.GetVertex("keep"); !ok {
		t.Fatal("strict restart lost accepted local Put")
	}
	if _, ok := candidate.graph.GetVertex("remove"); ok {
		t.Fatal("strict restart lost local physical Delete")
	}
	if weight, ok := candidate.graph.GetWeight("local-add", "head"); !ok || weight != 2 {
		t.Fatalf("strict restart local Add = %v, %v", weight, ok)
	}
	if _, ok := candidate.graph.GetWeight("remote-put", "head"); ok {
		t.Fatal("strict restart lost remote physical Delete")
	}
	if weight, ok := candidate.graph.GetWeight("remote-add", "head"); !ok || weight != 4 {
		t.Fatalf("strict restart remote Add = %v, %v", weight, ok)
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
