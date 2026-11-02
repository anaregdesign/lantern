package dataruntime

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
)

func TestGraphMutationDefiniteAbortThenOtherOrigin(t *testing.T) {
	_, _, origin := graphMutationCommittedFixture(t)
	originTime := hlcFromProto(origin.Mutation.GetHlc())
	receiverID := originTime.NodeID
	receiverID[0] ^= 0x80
	attempts := 0
	receiver := newReceiptEdgeDeleteFixtureWithLimits(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		attempts++
		if attempts == 1 {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
		}
		return nil
	}), receiverID, 16, 1<<20)
	bindPublicReceiptFixtureForConcurrencyTest(t, receiver)
	before := receiver.cache.SnapshotReplication()
	if err := receiver.service.ApplyMutation(t.Context(), origin.Mutation); connect.CodeOf(err) != connect.CodeUnavailable || attempts != 1 {
		t.Fatalf("first attempt: err=%v WAL attempts=%d", err, attempts)
	}
	if !reflect.DeepEqual(before, receiver.cache.SnapshotReplication()) || receiver.log.Len() != 0 || receiver.coordinator.store.Stats().Entries != 0 || receiver.service.LocalSeq(originTime.NodeID) != 0 {
		t.Fatal("definite abort published graph, receipts, WAL or origin prefix")
	}
	provisional := receiver.service.pendingMutations[originTime.NodeID][1].receiptWAL.(*graphMutationEnvelope)
	newer := originTime
	newer.Logical++
	newer.NodeID[0] ^= 0x40
	overwrite := &pb.Mutation{Origin: append([]byte(nil), newer.NodeID[:]...), Seq: 1, Hlc: hlcToProto(newer),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{
			Entries: []*pb.ReplicatedPutVertex{{Outcome: &pb.ReplicatedPutVertex_Live{Live: &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "newer"}}}}},
		}}}}
	if err := receiver.service.ApplyMutation(t.Context(), overwrite); err != nil {
		t.Fatal("other origin", err)
	}
	if err := receiver.service.ApplyMutation(t.Context(), origin.Mutation); err != nil {
		t.Fatal("healthy retry pinned provisional projection", err)
	}
	if attempts != 3 || receiver.log.Len() != 2 || receiver.service.LocalSeq(originTime.NodeID) != 1 || receiver.service.LocalSeq(newer.NodeID) != 1 || receiver.coordinator.store.Stats().Entries != 3 {
		t.Fatal("retry did not publish one complete decision")
	}
	local := receiver.log.RetainedEntries()[1].Op.(*graphMutationEnvelope)
	if local == provisional || !sameGraphMutationIntent(origin, local) || !reflect.DeepEqual(origin.Receipts, local.Receipts) || len(local.Mutation.GetOp().GetGraphMutation().GetProjection()) != 2 {
		t.Fatal("retry changed origin proof or retained obsolete receiver projection")
	}
	for _, original := range origin.Receipts {
		status, receipt, err := receiver.coordinator.Lookup(original.ID, time.Now())
		if err != nil || status != mutationreceipt.Confirmed || !reflect.DeepEqual(receipt, original) {
			t.Fatal("original receipt row/result changed", status, err)
		}
	}
	if err := receiver.service.ApplyMutation(t.Context(), origin.Mutation); err != nil || attempts != 3 || receiver.log.Len() != 2 || receiver.service.LocalSeq(originTime.NodeID) != 1 {
		t.Fatal("duplicate republished", err)
	}
	for key, want := range map[string]string{"a": "newer", "b": "second"} {
		if v, live := receiver.cache.GetVertex(key); !live || v.GetString_() != want {
			t.Fatalf("%s = %v, live=%v", key, v, live)
		}
	}
	if weight, live := receiver.cache.GetWeight("a", "b"); !live || weight != 2 {
		t.Fatal("missing or duplicated contribution", weight, live)
	}
}

func TestGraphMutationUnattemptedProjectionDoesNotDropOriginEffects(t *testing.T) {
	_, _, origin := graphMutationCommittedFixture(t)
	ts := hlcFromProto(origin.Mutation.GetHlc())
	receiver := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x57}, 16, 1<<20)
	bindPublicReceiptFixtureForConcurrencyTest(t, receiver)
	// Seed the pending state left by a pre-WAL budget rejection: a provisional
	// plan omitted a, but no WAL decision exists. With that blocker now absent,
	// the next healthy attempt must reconsider the original Put of a as well.
	m := proto.Clone(origin.Mutation).(*pb.Mutation)
	m.GetOp().GetGraphMutation().Projection = m.GetOp().GetGraphMutation().Projection[1:]
	provisional, err := decodeGraphMutation(m)
	if err != nil {
		t.Fatal(err)
	}
	pending := &pendingMutation{receipt: origin, receiptWAL: provisional}
	if err := receiver.service.commitReplicatedGraphMutation(t.Context(), ts.NodeID, 1, ts, pending); err != nil {
		t.Fatal(err)
	}
	local := receiver.log.RetainedEntries()[0].Op.(*graphMutationEnvelope)
	if !proto.Equal(local.Mutation, origin.Mutation) || receiver.log.Len() != 1 || receiver.service.LocalSeq(ts.NodeID) != 1 || receiver.coordinator.store.Stats().Entries != 3 {
		t.Fatal("unattempted projection constrained the committed decision")
	}
	if v, live := receiver.cache.GetVertex("a"); !live || v.GetString_() != "first" {
		t.Fatal("provisional omission lost an original effect", v, live)
	}
	if weight, live := receiver.cache.GetWeight("a", "b"); !live || weight != 2 {
		t.Fatal(weight, live)
	}
}

func TestGraphMutationCommittedProjectionRejectsDrift(t *testing.T) {
	_, _, committed := graphMutationCommittedFixture(t)
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	newer := hlcFromProto(committed.Mutation.GetHlc())
	newer.Logical++
	graph.PutVertexWithExpirationHLC("a", &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "newer"}}, time.Time{}, newer)
	before := graph.SnapshotReplication()
	if err := replayGraphMutation(graph, committed); err == nil {
		t.Fatal("committed WAL projection was silently recomputed")
	}
	if !reflect.DeepEqual(before, graph.SnapshotReplication()) {
		t.Fatal("failed committed replay leaked a partial decision")
	}
}

func TestGraphMutationReceiverScratchStallKeepsPrefixAndWholeGraph(t *testing.T) {
	_, _, origin := graphMutationCommittedFixture(t)
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x56}, 16, 1<<20)
	bindPublicReceiptFixtureForConcurrencyTest(t, f)
	f.cache.EnableSearchIndex(func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) }, strings.Compare)
	if err := f.cache.PutVertex("a", &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: strings.Repeat("old ", 2048)}}); err != nil {
		t.Fatal(err)
	}
	err := f.service.ApplyMutation(t.Context(), origin.Mutation)
	if connect.CodeOf(err) != connect.CodeResourceExhausted || f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 || f.service.LocalSeq(hlcFromProto(origin.Mutation.Hlc).NodeID) != 0 {
		t.Fatal("receiver advanced over an unrepresentable mixed plan", err)
	}
	if _, live := f.cache.GetVertex("b"); live {
		t.Fatal("receiver published half a call")
	}
	if _, live := f.cache.GetWeight("a", "b"); live {
		t.Fatal("receiver published Add while stalled")
	}
}

func TestGraphMutationSubscribeRequiresExplicitAcceptance(t *testing.T) {
	runtime, svc, original := graphMutationCommittedFixture(t)
	peer, err := runtime.NewReplication(svc)
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan *pb.SubscribeResponse, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = peer.Subscribe(ctx, &pb.SubscribeRequest{AcceptReceiptEnvelopes: true}, &replicationSubscribeRecorder{frames: frames})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || len(frames) != 0 {
		t.Fatal("old peer received/advanced over a mixed envelope", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- peer.Subscribe(ctx, &pb.SubscribeRequest{AcceptReceiptEnvelopes: true, AcceptGraphMutations: true}, &replicationSubscribeRecorder{frames: frames})
	}()
	select {
	case frame := <-frames:
		if !proto.Equal(frame.GetMutation(), original.Mutation) {
			t.Fatal("split or altered envelope")
		}
	case <-ctx.Done():
		t.Fatal("accepted peer did not receive envelope")
	}
	cancel()
	<-done
}

func TestGraphMutationRelayPreservesOriginAndRecoveryProjection(t *testing.T) {
	originRuntime, _, origin := graphMutationCommittedFixture(t)
	receiver := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x52}, 16, 1<<20)
	bindPublicReceiptFixtureForConcurrencyTest(t, receiver)
	// A local newer Vertex blocks only that Put; the contribution still wins.
	newer := hlcFromProto(origin.Mutation.GetHlc())
	newer.Logical++
	receiver.cache.PutVertexWithExpirationHLC("a", &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "newer"}}, time.Time{}, newer)
	if err := receiver.service.ApplyMutation(t.Context(), proto.Clone(origin.Mutation).(*pb.Mutation)); err != nil {
		t.Fatal(err)
	}
	if v, _ := receiver.cache.GetVertex("a"); v.GetString_() != "newer" {
		t.Fatal(v)
	}
	if w, live := receiver.cache.GetWeight("a", "b"); !live || w != 2 {
		t.Fatal(w, live)
	}
	local := receiver.log.RetainedEntries()[0].Op.(*graphMutationEnvelope)
	if !sameGraphMutationIntent(origin, local) || len(local.Mutation.GetOp().GetGraphMutation().GetProjection()) != 2 {
		t.Fatal("origin evidence/projection drift")
	}
	// Relay to an unblocked node reconstructs the origin effect, not local loss.
	final := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x53}, 16, 1<<20)
	bindPublicReceiptFixtureForConcurrencyTest(t, final)
	wire, err := local.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	if err := final.service.ApplyMutation(t.Context(), wire); err != nil {
		t.Fatal(err)
	}
	if v, live := final.cache.GetVertex("a"); !live || v.GetString_() != "first" {
		t.Fatal(v, live)
	}
	// Recovery uses only recorded effects: a missing old blocker cannot bring
	// back a Put omitted at the receiver. Edge-only replay never creates a.
	replay := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	if err := replayGraphMutation(replay, local); err != nil {
		t.Fatal(err)
	}
	if _, live := replay.GetVertex("a"); live {
		t.Fatal("replay reconsidered omitted effect")
	}
	if v, live := replay.GetVertex("b"); !live || v.GetString_() != "second" {
		t.Fatal(v, live)
	}
	before := receiver.log.Len()
	if err := receiver.service.ApplyMutation(t.Context(), origin.Mutation); err != nil || receiver.log.Len() != before {
		t.Fatal("duplicate origin reapplied", err)
	}
	if originRuntime.receipt.store.Stats().Entries != receiver.coordinator.store.Stats().Entries {
		t.Fatal("receipt half lost")
	}
}
