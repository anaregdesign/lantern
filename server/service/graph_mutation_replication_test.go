package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

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
	peer, err := runtime.NewLanternReplicationService(svc)
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
