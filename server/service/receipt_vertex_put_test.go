package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type receiptVertexPutWALFunc func(mutationlog.Entry) error

func (f receiptVertexPutWALFunc) Write(entry mutationlog.Entry) error { return f(entry) }

type receiptVertexPutFixture struct {
	coordinator *vertexPutReceiptCoordinator
	service     *LanternService
	cache       *graphcache.GraphCache[string, *pb.Vertex]
	log         *mutationlog.Log
	store       *mutationreceipt.Store
	epoch       mutationreceipt.Epoch
}

func newReceiptVertexPutFixture(
	t *testing.T,
	wal mutationlog.WAL,
	node hlc.NodeID,
	maxEntries int,
	configure func(*graphcache.GraphCache[string, *pb.Vertex]),
) receiptVertexPutFixture {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x72}
	store, err := mutationreceipt.New(mutationreceipt.Config{
		Epoch: epoch, Retention: time.Hour, MaxEntries: maxEntries, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	if configure != nil {
		configure(cache)
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	service := NewLanternService(cache).
		WithReplication(log, hlc.New(node, hlc.Options{}), nil).
		WithTombstoneTTL(time.Hour)
	coordinator, err := newVertexPutReceiptCoordinator(service, store)
	if err != nil {
		t.Fatal(err)
	}
	return receiptVertexPutFixture{coordinator, service, cache, log, store, epoch}
}

func receiptVertexPutTestCall(
	t *testing.T,
	epoch mutationreceipt.Epoch,
	groupSeed byte,
	ifAbsent bool,
	vertices ...*pb.Vertex,
) receiptVertexPutCall {
	t.Helper()
	call := receiptVertexPutCall{
		Group: mutationreceipt.GroupID{groupSeed}, IfAbsent: ifAbsent,
		Items: make([]receiptVertexPutItem, len(vertices)),
	}
	issued := time.Now().Add(-time.Second)
	for i, vertex := range vertices {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{groupSeed, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		call.Items[i] = receiptVertexPutItem{ID: id, Vertex: vertex}
	}
	return call
}

func TestVertexPutReceiptCoordinatorPreservesExactOutcomesAndRetry(t *testing.T) {
	f := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x73}, 32, nil)
	now := time.Now()
	if err := f.cache.PutVertexWithExpiration(
		"existing",
		&pb.Vertex{Key: "existing", Value: &pb.Vertex_String_{String_: "old"}},
		now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	f.cache.ApplyVertexCausalBarrierHLC("superseded", hlc.Timestamp{
		WallNs: now.Add(time.Minute).UnixNano(), NodeID: hlc.NodeID{0x7f},
	})
	first := &pb.Vertex{
		Key: "new", Value: &pb.Vertex_String_{String_: "first"},
		Expiration: timestamppb.New(now.Add(time.Hour)),
	}
	call := receiptVertexPutTestCall(t, f.epoch, 0x11, true,
		first,
		&pb.Vertex{Key: "existing", Value: &pb.Vertex_String_{String_: "blocked"}},
		&pb.Vertex{
			Key: "expired", Value: &pb.Vertex_String_{String_: "dead"},
			Expiration: timestamppb.New(now.Add(-time.Minute)),
		},
		&pb.Vertex{Key: "superseded", Value: &pb.Vertex_String_{String_: "stale"}},
		&pb.Vertex{Key: "new", Value: &pb.Vertex_String_{String_: "duplicate"}},
	)
	want := []pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
		pb.PutOutcome_PUT_OUTCOME_EXPIRED,
		pb.PutOutcome_PUT_OUTCOME_SUPERSEDED,
		pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
	}
	response, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || !slices.Equal(response.GetOutcomes(), want) {
		t.Fatalf("Commit = (%v, %v), want %v", response, err, want)
	}
	if f.log.Len() != 1 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 {
		t.Fatalf("publication = log %d seq %d, want 1/1",
			f.log.Len(), f.service.LocalSeq(f.service.clock.NodeID()))
	}
	envelope, ok := f.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope)
	if !ok || len(envelope.Accepted) != 2 ||
		envelope.Accepted[0].Index != 0 ||
		envelope.Accepted[0].Outcome != graphcache.PutOutcomeAppliedAndLive ||
		envelope.Accepted[1].Index != 2 ||
		envelope.Accepted[1].Outcome != graphcache.PutOutcomeExpired {
		t.Fatalf("accepted projection = %#v", envelope)
	}
	retry, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || !slices.Equal(retry.GetOutcomes(), want) || f.log.Len() != 1 {
		t.Fatalf("duplicate retry = (%v, %v), log=%d", retry, err, f.log.Len())
	}
	first.GetValue().(*pb.Vertex_String_).String_ = "caller-mutated"
	if got, live := f.cache.GetVertex("new"); !live || got.GetString_() != "first" ||
		envelope.Original[0].GetString_() != "first" {
		t.Fatalf("caller alias changed committed value or receipt: live=%v value=%v envelope=%v",
			live, got, envelope.Original[0])
	}
	conflict := call
	conflict.Items = append([]receiptVertexPutItem(nil), call.Items...)
	conflict.Items[0].Vertex = &pb.Vertex{Key: "new", Value: &pb.Vertex_String_{String_: "changed"}}
	if _, err := f.coordinator.Commit(context.Background(), conflict); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("intent conflict = %v, want InvalidArgument", err)
	}

	noOp := receiptVertexPutTestCall(t, f.epoch, 0x12, true,
		&pb.Vertex{Key: "existing", Value: &pb.Vertex_String_{String_: "still-blocked"}},
	)
	noOpResponse, err := f.coordinator.Commit(context.Background(), noOp)
	if err != nil || !slices.Equal(noOpResponse.GetOutcomes(),
		[]pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET}) {
		t.Fatalf("all-no-op Commit = (%v, %v)", noOpResponse, err)
	}
	noOpEnvelope, ok := f.log.RetainedEntries()[1].Op.(*vertexPutReceiptEnvelope)
	if !ok || len(noOpEnvelope.Accepted) != 0 ||
		f.service.LocalSeq(f.service.clock.NodeID()) != 2 || f.log.Len() != 2 {
		t.Fatalf("receipt-only publication = %#v seq=%d log=%d",
			noOpEnvelope, f.service.LocalSeq(f.service.clock.NodeID()), f.log.Len())
	}
}

func TestVertexPutReceiptRelayMaximumBoundsSparseFollower(t *testing.T) {
	origin := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x91}, 8, nil)
	follower := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x92}, 8, nil)
	vertex := &pb.Vertex{
		Key:        "sparse-relay",
		Value:      &pb.Vertex_Bytes{Bytes: make([]byte, 512)},
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	}
	call := receiptVertexPutTestCall(t, origin.epoch, 0x91, false, vertex)
	if _, err := origin.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	wire, err := origin.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	if !follower.cache.ApplyVertexCausalBarrierHLC(vertex.Key, hlc.Timestamp{
		WallNs: time.Now().Add(time.Minute).UnixNano(), NodeID: follower.service.clock.NodeID(),
	}) {
		t.Fatal("cannot install follower causal barrier")
	}
	if err := follower.service.ApplyMutation(context.Background(), proto.Clone(wire).(*pb.Mutation)); err != nil {
		t.Fatal(err)
	}
	retained := follower.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope)
	if len(retained.Accepted) != 0 ||
		retained.Receipts[0].Result[0] != byte(pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE) {
		t.Fatalf("receiver-local Vertex Put envelope = %+v", retained)
	}
	sparseSize, err := validateReplicationFrameSize(retained, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximalSize, err := validateReplicationRelayFrameSize(retained, 0)
	if err != nil || maximalSize <= sparseSize {
		t.Fatalf("sparse/maximal Vertex Put frames = %d/%d, %v", sparseSize, maximalSize, err)
	}
	if _, err := validateReplicationRelayFrameSize(retained, maximalSize-1); err == nil {
		t.Fatal("one-byte-under maximal Vertex Put relay was admitted")
	}
	if size, err := validateReplicationRelayFrameSize(retained, maximalSize); err != nil || size != maximalSize {
		t.Fatalf("exact-fit maximal Vertex Put relay = %d, %v", size, err)
	}
}

func TestVertexPutReceiptCoordinatorRejectsBeforeGraphOrLog(t *testing.T) {
	t.Run("Hard batch limit", func(t *testing.T) {
		f := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x70}, 1, nil)
		call := receiptVertexPutCall{
			Group: mutationreceipt.GroupID{0x20},
			Items: make([]receiptVertexPutItem, receiptVertexWALMaxItems+1),
		}
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("batch-limit rejection = %v, want InvalidArgument", err)
		}
		if f.log.Len() != 0 || f.store.Stats().Entries != 0 {
			t.Fatal("batch-limit rejection changed log or Store")
		}
	})

	t.Run("Store capacity", func(t *testing.T) {
		f := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x74}, 1, nil)
		call := receiptVertexPutTestCall(t, f.epoch, 0x21, false,
			&pb.Vertex{Key: "one"}, &pb.Vertex{Key: "two"})
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("capacity rejection = %v, want ResourceExhausted", err)
		}
		if _, live := f.cache.GetVertex("one"); live || f.log.Len() != 0 ||
			f.service.LocalSeq(f.service.clock.NodeID()) != 0 {
			t.Fatal("capacity rejection changed graph, log, or origin")
		}
	})

	t.Run("Search preparation", func(t *testing.T) {
		f := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x75}, 8,
			func(cache *graphcache.GraphCache[string, *pb.Vertex]) {
				cache.EnableSearchIndex(
					func(_ string, value *pb.Vertex) search.Document {
						return search.Text(value.GetString_())
					},
					strings.Compare,
					graphcache.WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 4}),
				)
			})
		call := receiptVertexPutTestCall(t, f.epoch, 0x22, false,
			&pb.Vertex{Key: "search", Value: &pb.Vertex_String_{String_: "oversized"}})
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("search rejection = %v, want ResourceExhausted", err)
		}
		if _, live := f.cache.GetVertex("search"); live || f.log.Len() != 0 ||
			f.store.Stats().Entries != 0 {
			t.Fatal("search rejection changed graph, receipt Store, or log")
		}
	})
}

func TestVertexPutReceiptCoordinatorWALFailureSemantics(t *testing.T) {
	t.Run("Definite rollback and retry", func(t *testing.T) {
		var writes atomic.Int32
		wal := receiptVertexPutWALFunc(func(mutationlog.Entry) error {
			if writes.Add(1) == 1 {
				return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
			}
			return nil
		})
		f := newReceiptVertexPutFixture(t, wal, hlc.NodeID{0x76}, 8, nil)
		call := receiptVertexPutTestCall(t, f.epoch, 0x31, false,
			&pb.Vertex{Key: "definite", Value: &pb.Vertex_String_{String_: "value"}})
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("definite WAL failure = %v, want Unavailable", err)
		}
		if _, live := f.cache.GetVertex("definite"); live || f.log.Len() != 0 ||
			f.service.LocalSeq(f.service.clock.NodeID()) != 0 || f.store.Stats().Entries != 0 ||
			f.service.receiptCommitFaulted {
			t.Fatal("definite WAL abort did not roll back exactly")
		}
		if _, err := f.coordinator.Commit(context.Background(), call); err != nil {
			t.Fatalf("retry after definite abort: %v", err)
		}
	})

	t.Run("Ambiguous failure fail-stops", func(t *testing.T) {
		f := newReceiptVertexPutFixture(t,
			receiptVertexPutWALFunc(func(mutationlog.Entry) error {
				return errors.New("lost WAL acknowledgement")
			}),
			hlc.NodeID{0x77}, 8, nil,
		)
		call := receiptVertexPutTestCall(t, f.epoch, 0x32, false,
			&pb.Vertex{Key: "ambiguous", Value: &pb.Vertex_String_{String_: "value"}})
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("ambiguous WAL failure = %v, want Unavailable", err)
		}
		if _, live := f.cache.GetVertex("ambiguous"); live || !f.service.receiptCommitFaulted ||
			f.store.Stats().Entries != 0 || f.log.Len() != 0 {
			t.Fatal("ambiguous WAL failure leaked state or did not fail-stop")
		}
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("post-ambiguity write = %v, want FailedPrecondition", err)
		}
		if _, _, err := f.coordinator.Lookup(call.Items[0].ID, time.Now()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("post-ambiguity lookup = %v, want FailedPrecondition", err)
		}
	})
}

func TestVertexPutReceiptMultiHopReconstructsOriginEffects(t *testing.T) {
	a := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x61}, 16, nil)
	b := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x62}, 16, nil)
	c := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0x63}, 16, nil)
	shortExpiration := time.Now().Add(750 * time.Millisecond)
	call := receiptVertexPutTestCall(t, a.epoch, 0x41, true,
		&pb.Vertex{
			Key: "live", Value: &pb.Vertex_String_{String_: "origin"},
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		},
		&pb.Vertex{
			Key: "becomes-barrier", Value: &pb.Vertex_String_{String_: "origin"},
			Expiration: timestamppb.New(shortExpiration),
		},
	)
	if _, err := a.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	originEnvelope := a.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope)
	wire, err := originEnvelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	newer := originEnvelope.HLC
	newer.WallNs++
	b.cache.ApplyVertexCausalBarrierHLC("live", newer)
	b.cache.ApplyVertexCausalBarrierHLC("becomes-barrier", newer)
	if err := b.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("B apply: %v", err)
	}
	bEnvelope := b.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope)
	if len(bEnvelope.Accepted) != 0 {
		t.Fatalf("B receiver-local projection = %#v, want receipt-only", bEnvelope.Accepted)
	}
	bWire, err := bEnvelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(shortExpiration) + 20*time.Millisecond)
	if err := c.service.ApplyMutation(context.Background(), bWire); err != nil {
		t.Fatalf("C apply: %v", err)
	}
	if vertex, live := c.cache.GetVertex("live"); !live || vertex.GetString_() != "origin" {
		t.Fatalf("C lost origin-authoritative live effect: live=%v vertex=%v", live, vertex)
	}
	if _, live := c.cache.GetVertex("becomes-barrier"); live {
		t.Fatal("C reclassified expired origin value as live")
	}
	cEnvelope := c.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope)
	if len(cEnvelope.Accepted) != 2 ||
		cEnvelope.Accepted[0].Outcome != graphcache.PutOutcomeAppliedAndLive ||
		cEnvelope.Accepted[1].Outcome != graphcache.PutOutcomeExpired ||
		!cEnvelope.Accepted[1].Item.CausalBarrier {
		t.Fatalf("C receiver-local projection = %#v", cEnvelope.Accepted)
	}
	barriers := c.cache.SnapshotCausalBarriers()
	if len(barriers.Vertices) != 1 || barriers.Vertices[0].Key != "becomes-barrier" {
		t.Fatalf("C causal barriers = %#v", barriers.Vertices)
	}
}
