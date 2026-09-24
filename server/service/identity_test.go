package service

import (
	"context"
	"encoding/binary"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func testIdentityMutation(seq uint64, op *pb.MutationOp) *pb.Mutation {
	return &pb.Mutation{Seq: seq, Origin: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Hlc: &pb.HLCTimestamp{NodeId: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}, Op: op}
}

func TestProjectMutationIdentities(t *testing.T) {
	t.Run("values and contribution IDs never enter identity frames", func(t *testing.T) {
		vertex := &pb.Vertex{Key: "safe-key", Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}}
		mutation := testIdentityMutation(1, &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{{Outcome: &pb.ReplicatedPutVertex_Live{Live: vertex}}, {Outcome: &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: "barrier"}}}}}}})
		var frames []*pb.SubscribeResponse
		if err := projectMutationIdentities(mutation, func(frame *pb.SubscribeResponse) error { frames = append(frames, frame); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 || frames[0].GetMutation() != nil || frames[0].GetCheckpoint() != nil {
			t.Fatalf("unexpected frame shape: %+v", frames)
		}
		chunk := frames[0].GetIdentityChunk()
		if chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX || !chunk.GetIsLast() || len(chunk.GetVertexKeys()) != 2 || chunk.GetVertexKeys()[0] != "safe-key" || chunk.GetVertexKeys()[1] != "barrier" {
			t.Fatalf("wrong projected identities: %+v", chunk)
		}
		encoded, err := protojson.Marshal(frames[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "PRIVATE-VALUE") {
			t.Fatalf("identity frame leaked graph value: %s", encoded)
		}
	})
	t.Run("Add hides weight and ContribID", func(t *testing.T) {
		mutation := testIdentityMutation(2, &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "tail", Head: "head", Weight: 72.5}}, ContribIds: [][]byte{[]byte("PRIVATE-CONTRIBUTION-ID")}}}})
		var frame *pb.SubscribeResponse
		if err := projectMutationIdentities(mutation, func(v *pb.SubscribeResponse) error { frame = v; return nil }); err != nil {
			t.Fatal(err)
		}
		chunk := frame.GetIdentityChunk()
		if chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE || len(chunk.GetEdgeKeys()) != 1 || chunk.GetEdgeKeys()[0].GetTail() != "tail" || chunk.GetEdgeKeys()[0].GetHead() != "head" {
			t.Fatalf("wrong edge identity: %+v", chunk)
		}
		encoded, err := protojson.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "PRIVATE-CONTRIBUTION-ID") || strings.Contains(string(encoded), "72.5") {
			t.Fatalf("identity frame leaked edge payload: %s", encoded)
		}
	})
	t.Run("nil repeated items still invalidate their zero-value identity", func(t *testing.T) {
		for _, op := range []*pb.MutationOp{
			{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{nil}}}},
			{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{nil}}}},
			{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{Edges: []*pb.Edge{nil}}}},
		} {
			var frame *pb.SubscribeResponse
			if err := projectMutationIdentities(testIdentityMutation(2, op), func(v *pb.SubscribeResponse) error { frame = v; return nil }); err != nil {
				t.Fatal(err)
			}
			chunk := frame.GetIdentityChunk()
			if chunk == nil || len(chunk.GetVertexKeys())+len(chunk.GetEdgeKeys()) != 1 {
				t.Fatalf("malformed repeated item silently omitted: %+v", chunk)
			}
		}
	})
	t.Run("1025 victims span two bounded chunks", func(t *testing.T) {
		keys := make([]string, 1025)
		for i := range keys {
			keys[i] = "victim"
		}
		mutation := testIdentityMutation(3, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: keys}}})
		var frames []*pb.SubscribeResponse
		if err := projectMutationIdentities(mutation, func(v *pb.SubscribeResponse) error { frames = append(frames, v); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(frames) != 2 || frames[0].GetIdentityChunk().GetIsLast() || !frames[1].GetIdentityChunk().GetIsLast() || len(frames[0].GetIdentityChunk().GetVertexKeys()) != 1024 || frames[1].GetIdentityChunk().GetFirstItemIndex() != 1024 || frames[1].GetIdentityChunk().GetChunkIndex() != 1 {
			t.Fatalf("wrong chunk boundary: %+v", frames)
		}
		for _, frame := range frames {
			if proto.Size(frame) > maxIdentityFrameBytes {
				t.Fatalf("oversize identity frame: %d", proto.Size(frame))
			}
		}
	})
	t.Run("byte cap and final empty marker", func(t *testing.T) {
		keys := []string{strings.Repeat("x", 700_000), strings.Repeat("y", 700_000)}
		mutation := testIdentityMutation(4, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: keys}}})
		var frames []*pb.SubscribeResponse
		if err := projectMutationIdentities(mutation, func(v *pb.SubscribeResponse) error { frames = append(frames, v); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(frames) != 2 || proto.Size(frames[0]) > maxIdentityFrameBytes || proto.Size(frames[1]) > maxIdentityFrameBytes {
			t.Fatalf("byte-capped frames = %d", len(frames))
		}
		tooLarge := testIdentityMutation(5, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{strings.Repeat("z", maxIdentityFrameBytes)}}}})
		if err := projectMutationIdentities(tooLarge, func(*pb.SubscribeResponse) error { t.Fatal("sent oversized frame"); return nil }); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("oversized item error = %v", err)
		}
		var empty *pb.IdentityChunk
		if err := projectMutationIdentities(testIdentityMutation(6, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{}}}), func(v *pb.SubscribeResponse) error { empty = v.GetIdentityChunk(); return nil }); err != nil {
			t.Fatal(err)
		}
		if empty == nil || !empty.GetIsLast() || len(empty.GetVertexKeys()) != 0 {
			t.Fatalf("missing final empty marker: %+v", empty)
		}
	})
	t.Run("legacy predicate fails closed", func(t *testing.T) {
		mutation := testIdentityMutation(7, &pb.MutationOp{Op: &pb.MutationOp_DeleteVerticesByPrefix{DeleteVerticesByPrefix: &pb.DeleteVerticesByPrefixRequest{Prefix: "limited/", Limit: 1}}})
		if err := projectMutationIdentities(mutation, func(*pb.SubscribeResponse) error { t.Fatal("sent legacy predicate"); return nil }); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("legacy predicate error = %v", err)
		}
	})
}

func TestValidateIdentityResume(t *testing.T) {
	origin := "0102030405060708090a0b0c0d0e0f10"
	entry := func(seq uint64) mutationlog.Entry {
		return mutationlog.Entry{Seq: seq, Op: testIdentityMutation(seq, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"x"}}}})}
	}
	frontier := map[string]uint64{origin: 3}
	if err := validateIdentityResume(map[string]uint64{origin: 2}, frontier, []mutationlog.Entry{entry(1), entry(2), entry(3)}); err != nil {
		t.Fatal(err)
	}
	if err := validateIdentityResume(map[string]uint64{origin: 4}, frontier, nil); err != nil {
		t.Fatalf("future cursor rejected: %v", err)
	}
	for _, retained := range [][]mutationlog.Entry{{entry(1), entry(3)}, {entry(2)}} {
		if err := validateIdentityResume(map[string]uint64{origin: 2}, frontier, retained); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("hole did not gap: %v", err)
		}
	}
	if _, err := identityCursor(map[string]uint64{"ABC": 1}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed origin accepted: %v", err)
	}
	if _, err := identityCursor(map[string]uint64{origin: 0}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("zero cursor accepted: %v", err)
	}
}

type identityTestOrigins struct{ states []OriginState }

func (o identityTestOrigins) OriginStates() []OriginState { return o.states }
func (identityTestOrigins) withReplicationSubscribeCut(capture func(<-chan struct{})) error {
	capture(nil)
	return nil
}

type blockedIdentitySender struct {
	checkpoint chan struct{}
	blocked    chan struct{}
	release    chan struct{}
	first      bool
}

func (s *blockedIdentitySender) Send(frame *pb.SubscribeResponse) error {
	if frame.GetCheckpoint() != nil {
		close(s.checkpoint)
		return nil
	}
	if !s.first {
		s.first = true
		close(s.blocked)
		<-s.release
	}
	return nil
}

func TestIdentitySubscribe_SlowSenderGaps(t *testing.T) {
	dropped := make(chan string, 1)
	log := mutationlog.New(mutationlog.Options{
		Capacity:         8,
		SubscriberBuffer: 1,
		OnDrop: func(cause string) {
			select {
			case dropped <- cause:
			default:
			}
		},
	})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewLanternReplicationService(log, nil, nil).WithOriginStates(identityTestOrigins{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sender := &blockedIdentitySender{
		checkpoint: make(chan struct{}),
		blocked:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-sender.release:
		default:
			close(sender.release)
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- svc.Subscribe(ctx, &pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}, sender)
	}()
	select {
	case <-sender.checkpoint:
	case <-ctx.Done():
		t.Fatal("identity checkpoint was not sent")
	}
	for seq := uint64(1); seq <= 5; seq++ {
		mutation := testIdentityMutation(seq, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{Key: "slow"}}})
		if _, err := log.Append(mutation, hlc.Timestamp{}); err != nil {
			t.Fatal(err)
		}
		if seq == 1 {
			select {
			case <-sender.blocked:
			case <-ctx.Done():
				t.Fatal("sender never blocked on identity frame")
			}
		}
	}
	select {
	case cause := <-dropped:
		if cause != mutationlog.DropCauseBufferFull {
			t.Fatalf("drop cause = %s", cause)
		}
	case <-ctx.Done():
		t.Fatal("slow identity subscriber was not dropped")
	}
	close(sender.release)
	select {
	case err := <-done:
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
			t.Fatalf("slow identity stream error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("slow identity stream did not end")
	}
}

type countingIdentitySender struct{ sent int }

func (s *countingIdentitySender) Send(*pb.SubscribeResponse) error {
	s.sent++
	return nil
}

func TestIdentitySubscribe_OversizeCheckpointFailsBeforeSend(t *testing.T) {
	states := make([]OriginState, 35_000)
	for i := range states {
		states[i].Origin[0] = 1
		binary.BigEndian.PutUint64(states[i].Origin[8:], uint64(i+1))
		states[i].LastSeq = 1
	}
	log := mutationlog.New(mutationlog.Options{})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewLanternReplicationService(log, nil, nil).WithOriginStates(identityTestOrigins{states: states})
	sender := &countingIdentitySender{}
	err := svc.Subscribe(context.Background(), &pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}, sender)
	if connect.CodeOf(err) != connect.CodeResourceExhausted || sender.sent != 0 {
		t.Fatalf("oversize checkpoint = (%v, %d frames), want ResourceExhausted without a frame", err, sender.sent)
	}
}

func BenchmarkProjectMutationIdentities(b *testing.B) {
	for _, count := range []int{1, 1024} {
		b.Run(strconv.Itoa(count)+"_vertices", func(b *testing.B) {
			vertices := make([]*pb.Vertex, count)
			for i := range vertices {
				vertices[i] = &pb.Vertex{Key: "bench/" + strconv.Itoa(i), Value: &pb.Vertex_String_{String_: "value-must-not-copy"}}
			}
			mutation := testIdentityMutation(1, &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: vertices}}})
			b.ReportAllocs()
			b.SetBytes(int64(proto.Size(mutation)))
			for i := 0; i < b.N; i++ {
				if err := projectMutationIdentities(mutation, func(*pb.SubscribeResponse) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
