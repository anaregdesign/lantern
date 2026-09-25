package integration_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/replication"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func receiptEdgeDeleteTailFixture(t *testing.T, origin hlc.NodeID, seq uint64, accepted []bool) (*pb.Mutation, hlc.Timestamp) {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x73}
	group := mutationreceipt.GroupID{byte(seq)}
	issued := time.Now().Add(-time.Second)
	items := make([]*pb.ReplicatedReceiptEdgeDeleteItem, len(accepted))
	for i, isAccepted := range accepted {
		head := "edge/" + strconv.Itoa(i)
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(seq), byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		canonical := []byte{byte(mutationreceipt.DeleteEdge)}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len("edge/tail")))
		canonical = append(canonical, length[:]...)
		canonical = append(canonical, "edge/tail"...)
		binary.BigEndian.PutUint64(length[:], uint64(len(head)))
		canonical = append(canonical, length[:]...)
		canonical = append(canonical, head...)
		digest := mutationreceipt.IntentDigest(canonical)
		items[i] = &pb.ReplicatedReceiptEdgeDeleteItem{
			Key: &pb.EdgeKey{Tail: "edge/tail", Head: head},
			Receipt: &pb.MutationReceipt{
				OperationId: id.Bytes(), LogicalCallId: group[:], ItemIndex: uint32(i),
				ItemCount: uint32(len(accepted)), IntentSha256: digest[:],
				DeadlineUnixMs: uint64(issued.Add(time.Hour).UnixMilli()),
				OriginalResult: &pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: isAccepted}},
			},
			CausallyAccepted: isAccepted,
		}
	}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	return &pb.Mutation{
		Seq: seq, Origin: origin[:], Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
			ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{
				DeploymentEpoch: epoch[:], PolicyFingerprint: append([]byte{0x51}, make([]byte, 31)...),
				TombstoneExpiration: timestamppb.New(time.Now().Add(time.Hour)), Items: items,
			},
		}},
	}, stamp
}

func receiptVertexPutTailDigest(vertex *pb.Vertex, ifAbsent bool) []byte {
	canonical := []byte{byte(mutationreceipt.PutVertex)}
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(vertex.GetKey())))
	canonical = append(canonical, vertex.GetKey()...)
	canonical = append(canonical, 17)
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(vertex.GetString_())))
	canonical = append(canonical, vertex.GetString_()...)
	if vertex.GetExpiration() == nil {
		canonical = append(canonical, 0)
	} else {
		canonical = append(canonical, 1)
		canonical = binary.BigEndian.AppendUint64(
			canonical, uint64(vertex.GetExpiration().GetSeconds()),
		)
		canonical = binary.BigEndian.AppendUint32(
			canonical, uint32(vertex.GetExpiration().GetNanos()),
		)
	}
	if ifAbsent {
		canonical = append(canonical, 1)
	} else {
		canonical = append(canonical, 0)
	}
	digest := mutationreceipt.IntentDigest(canonical)
	return digest[:]
}

func receiptVertexDeleteTailDigest(key string) []byte {
	canonical := []byte{byte(mutationreceipt.DeleteVertex)}
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(key)))
	canonical = append(canonical, key...)
	digest := mutationreceipt.IntentDigest(canonical)
	return digest[:]
}

func receiptVertexTailFixtures(
	t *testing.T,
	origin hlc.NodeID,
) (*pb.Mutation, hlc.Timestamp, *pb.Mutation, hlc.Timestamp) {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x73}
	issued := time.Now().Add(-time.Second)
	deadline := uint64(issued.Add(time.Hour).UnixMilli())
	policy := append([]byte{0x51}, make([]byte, 31)...)
	putGroup := mutationreceipt.GroupID{0x31}
	expiredAt := timestamppb.New(time.Now().Add(-time.Minute))
	originals := []*pb.Vertex{
		{Key: "vertex/permanent", Value: &pb.Vertex_String_{String_: "permanent"}},
		{Key: "vertex/no-op", Value: &pb.Vertex_String_{String_: "blocked"}},
		{
			Key: "vertex/expired", Value: &pb.Vertex_String_{String_: "expired"},
			Expiration: expiredAt,
		},
	}
	putResults := []pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
		pb.PutOutcome_PUT_OUTCOME_EXPIRED,
	}
	putItems := make([]*pb.ReplicatedReceiptVertexPutItem, len(originals))
	for i, original := range originals {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{0x31, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		item := &pb.ReplicatedReceiptVertexPutItem{
			Original: proto.Clone(original).(*pb.Vertex),
			Receipt: &pb.MutationReceipt{
				OperationId: id.Bytes(), LogicalCallId: putGroup[:],
				ItemIndex: uint32(i), ItemCount: uint32(len(originals)),
				IntentSha256:   receiptVertexPutTailDigest(original, true),
				DeadlineUnixMs: deadline,
				OriginalResult: &pb.ReceiptResult{
					Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: putResults[i]},
				},
			},
		}
		switch i {
		case 0:
			item.Accepted = &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_Live{
					Live: proto.Clone(original).(*pb.Vertex),
				},
			}
		case 2:
			item.Accepted = &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_CausalBarrier{
					CausalBarrier: &pb.VertexCausalBarrier{Key: original.GetKey()},
				},
			}
		}
		putItems[i] = item
	}
	putStamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	put := &pb.Mutation{
		Seq: 1, Origin: origin[:],
		Hlc: &pb.HLCTimestamp{WallNs: putStamp.WallNs, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptVertexPut{
			ReplicatedReceiptVertexPut: &pb.ReplicatedReceiptVertexPut{
				DeploymentEpoch: epoch[:], PolicyFingerprint: policy,
				IfAbsent: true, Items: putItems,
			},
		}},
	}

	deleteGroup := mutationreceipt.GroupID{0x32}
	deleteKeys := []string{"vertex/permanent", "vertex/absent"}
	deleteItems := make([]*pb.ReplicatedReceiptVertexDeleteItem, len(deleteKeys))
	for i, key := range deleteKeys {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{0x32, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		deleteItems[i] = &pb.ReplicatedReceiptVertexDeleteItem{
			Key: key,
			Receipt: &pb.MutationReceipt{
				OperationId: id.Bytes(), LogicalCallId: deleteGroup[:],
				ItemIndex: uint32(i), ItemCount: uint32(len(deleteKeys)),
				IntentSha256: receiptVertexDeleteTailDigest(key), DeadlineUnixMs: deadline,
				OriginalResult: &pb.ReceiptResult{
					Result: &pb.ReceiptResult_DeleteVertexExisted{
						DeleteVertexExisted: i == 0,
					},
				},
			},
		}
	}
	deleteStamp := putStamp
	deleteStamp.Logical++
	deleteMutation := &pb.Mutation{
		Seq: 2, Origin: origin[:],
		Hlc: &pb.HLCTimestamp{
			WallNs: deleteStamp.WallNs, Logical: deleteStamp.Logical, NodeId: origin[:],
		},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptVertexDelete{
			ReplicatedReceiptVertexDelete: &pb.ReplicatedReceiptVertexDelete{
				DeploymentEpoch: epoch[:], PolicyFingerprint: append([]byte(nil), policy...),
				TombstoneExpiration: timestamppb.New(time.Now().Add(time.Hour)),
				Items:               deleteItems,
			},
		}},
	}
	return put, putStamp, deleteMutation, deleteStamp
}

func TestIdentityCDC_SupersededEdgePutDoesNotReviveUnloggedEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := newPumpNode(t, hlc.NodeID{0xD1, 0x21})
	follower := newPumpNode(t, hlc.NodeID{0xD1, 0x22})
	remote := hlc.NodeID{0xD1, 0x23}
	expiration := timestamppb.New(time.Now().Add(time.Hour))
	// A remote future-HLC winner leaves a newer bucket floor on the origin.
	// The local clock has not observed that HLC, so its next wire Put loses.
	winner := &pb.Mutation{
		Seq: 1, Origin: remote[:],
		Hlc: &pb.HLCTimestamp{NodeId: remote[:], WallNs: time.Now().Add(time.Minute).UnixNano()},
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{
			Edges: []*pb.Edge{{Tail: "lww/tail", Head: "lww/head", Weight: 2, Expiration: expiration}},
		}}},
	}
	if err := origin.svc.ApplyMutation(ctx, winner); err != nil {
		t.Fatalf("seed remote winner: %v", err)
	}
	follower.startPump(ctx, t, []string{origin.url})
	for follower.svc.LocalSeq(remote) != 1 {
		if err := ctx.Err(); err != nil {
			t.Fatalf("follower missed winning Edge: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := origin.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "lww/tail"})); err != nil {
		t.Fatalf("remove endpoint over h2c: %v", err)
	}
	for follower.svc.LocalSeq(origin.nodeID) != 1 {
		if err := ctx.Err(); err != nil {
			t.Fatalf("follower missed endpoint Delete: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, node := range []*pumpNode{origin, follower} {
		if _, ok := node.cache.GetVertex("lww/tail"); ok {
			t.Fatalf("%x kept removed endpoint", node.nodeID)
		}
	}
	before, ok := origin.log.LastSeq()
	if !ok {
		t.Fatal("seed mutations did not enter the log")
	}
	feed, err := newReplicationRawClient(t, origin.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("identity CDC missed initial checkpoint: %v", feed.Err())
	}
	stale, err := origin.raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{{
		Tail: "lww/tail", Head: "lww/head", Weight: 9, Expiration: expiration,
	}}}))
	if err != nil || len(stale.Msg.GetOutcomes()) != 1 || stale.Msg.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_SUPERSEDED {
		t.Fatalf("stale Edge Put over h2c = %v, %v", stale, err)
	}
	if got, _ := origin.log.LastSeq(); got != before || origin.svc.LocalSeq(origin.nodeID) != 1 {
		t.Fatalf("superseded Put emitted a mutation: local/origin seq = %d/%d, want %d/1", got, origin.svc.LocalSeq(origin.nodeID), before)
	}
	for _, node := range []*pumpNode{origin, follower} {
		if _, ok := node.cache.GetVertex("lww/tail"); ok {
			t.Fatalf("%x revived endpoint without a CDC mutation", node.nodeID)
		}
	}

	accepted, err := origin.raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{{
		Tail: "lww/control", Head: "lww/new", Weight: 3, Expiration: expiration,
	}}}))
	if err != nil || len(accepted.Msg.GetOutcomes()) != 1 || accepted.Msg.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("accepted Edge Put over h2c = %v, %v", accepted, err)
	}
	if !feed.Receive() {
		t.Fatalf("identity CDC missed accepted control Put: %v", feed.Err())
	}
	chunk := feed.Msg().GetIdentityChunk()
	if chunk == nil || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_PUT_EDGE ||
		hex.EncodeToString(chunk.GetOrigin()) != hex.EncodeToString(origin.nodeID[:]) ||
		chunk.GetSeq() != 2 || len(chunk.GetEdgeKeys()) != 1 ||
		chunk.GetEdgeKeys()[0].GetTail() != "lww/control" || chunk.GetEdgeKeys()[0].GetHead() != "lww/new" {
		t.Fatalf("identity CDC emitted a stale or wrong Edge Put: %+v", feed.Msg())
	}
	for follower.svc.LocalSeq(origin.nodeID) != 2 {
		if err := ctx.Err(); err != nil {
			t.Fatalf("follower missed accepted control Put: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := follower.cache.GetVertex("lww/tail"); ok {
		t.Fatal("follower revived stale endpoint")
	}
	if weight, ok := follower.cache.GetWeight("lww/control", "lww/new"); !ok || weight != 3 {
		t.Fatalf("follower accepted control Edge = %v/%v, want 3/true", weight, ok)
	}
	// One wire batch now mixes a stale loser, a live winner, and a born-
	// expired causal barrier. The private WAL sidecar can represent these
	// receiver-local decisions, while current identity CDC still projects
	// only the two accepted identities in their request order.
	mixed, err := origin.raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "lww/tail", Head: "lww/head", Weight: 8, Expiration: expiration},
		{Tail: "mixed/live", Head: "mixed/head", Weight: 4, Expiration: expiration},
		{Tail: "mixed/expired", Head: "mixed/head", Weight: 5, Expiration: timestamppb.New(time.Now().Add(-time.Minute))},
	}}))
	if err != nil || len(mixed.Msg.GetOutcomes()) != 3 ||
		mixed.Msg.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_SUPERSEDED ||
		mixed.Msg.GetOutcomes()[1] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
		mixed.Msg.GetOutcomes()[2] != pb.PutOutcome_PUT_OUTCOME_EXPIRED {
		t.Fatalf("mixed Edge Put outcomes over h2c = %v, %v", mixed, err)
	}
	if !feed.Receive() {
		t.Fatalf("identity CDC missed mixed accepted Put: %v", feed.Err())
	}
	chunk = feed.Msg().GetIdentityChunk()
	if chunk == nil || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_PUT_EDGE ||
		chunk.GetSeq() != 3 || len(chunk.GetEdgeKeys()) != 2 ||
		chunk.GetEdgeKeys()[0].GetTail() != "mixed/live" || chunk.GetEdgeKeys()[0].GetHead() != "mixed/head" ||
		chunk.GetEdgeKeys()[1].GetTail() != "mixed/expired" || chunk.GetEdgeKeys()[1].GetHead() != "mixed/head" {
		t.Fatalf("identity CDC changed mixed Put projection: %+v", feed.Msg())
	}
	for follower.svc.LocalSeq(origin.nodeID) != 3 {
		if err := ctx.Err(); err != nil {
			t.Fatalf("follower missed mixed Put: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := follower.cache.GetVertex("lww/tail"); ok {
		t.Fatal("mixed rejected Put revived stale follower endpoint")
	}
	if weight, ok := follower.cache.GetWeight("mixed/live", "mixed/head"); !ok || weight != 4 {
		t.Fatalf("follower mixed live Edge = %v/%v, want 4/true", weight, ok)
	}
	if _, ok := follower.cache.GetVertex("mixed/expired"); ok {
		t.Fatal("accepted-expired Put created follower endpoint")
	}
}

func TestIdentityCDC_DedupedAddDoesNotReviveEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := newPumpNode(t, hlc.NodeID{0xD1, 0x31})
	follower := newPumpNode(t, hlc.NodeID{0xD1, 0x32})
	follower.startPump(ctx, t, []string{origin.url})
	waitForOrigin := func(seq uint64) {
		t.Helper()
		for follower.svc.LocalSeq(origin.nodeID) < seq {
			if err := ctx.Err(); err != nil {
				t.Fatalf("follower missed origin seq %d: %v", seq, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	expiration := timestamppb.New(time.Now().Add(time.Hour))
	firstID := make([]byte, 24)
	firstID[0] = 1
	add := func(id []byte) float32 {
		t.Helper()
		resp, err := origin.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
			Edge:      &pb.Edge{Tail: "dedup/tail", Head: "dedup/head", Weight: 1, Expiration: expiration},
			ContribId: id,
		}))
		if err != nil {
			t.Fatalf("AddEdge over h2c: %v", err)
		}
		return resp.Msg.GetEffectiveWeight()
	}
	if got := add(firstID); got != 1 {
		t.Fatalf("first Add effective weight = %v, want 1", got)
	}
	waitForOrigin(1)
	if _, err := origin.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "dedup/tail", Expiration: timestamppb.New(time.Now().Add(-time.Second)),
	}})); err != nil {
		t.Fatalf("expire endpoint over h2c: %v", err)
	}
	waitForOrigin(2)
	for _, node := range []*pumpNode{origin, follower} {
		if _, ok := node.cache.GetVertex("dedup/tail"); ok {
			t.Fatalf("%x retained expired endpoint", node.nodeID)
		}
		if _, ok := node.cache.GetWeight("dedup/tail", "dedup/head"); ok {
			t.Fatalf("%x exposed Edge without endpoint", node.nodeID)
		}
	}
	feed, err := newReplicationRawClient(t, origin.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("identity CDC missed checkpoint: %v", feed.Err())
	}
	if got := add(firstID); got != 1 {
		t.Fatalf("dedup Add effective weight = %v, want 1", got)
	}
	waitForOrigin(3)
	for _, node := range []*pumpNode{origin, follower} {
		if _, ok := node.cache.GetVertex("dedup/tail"); ok {
			t.Fatalf("%x revived endpoint on dedup Add", node.nodeID)
		}
		if _, ok := node.cache.GetWeight("dedup/tail", "dedup/head"); ok {
			t.Fatalf("%x revealed old Edge on dedup Add", node.nodeID)
		}
	}
	if !feed.Receive() {
		t.Fatalf("identity CDC missed conservative Add event: %v", feed.Err())
	}
	chunk := feed.Msg().GetIdentityChunk()
	if chunk == nil || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE ||
		len(chunk.GetVertexKeys()) != 0 || len(chunk.GetEdgeKeys()) != 1 ||
		chunk.GetEdgeKeys()[0].GetTail() != "dedup/tail" || chunk.GetEdgeKeys()[0].GetHead() != "dedup/head" {
		t.Fatalf("dedup Add identity projection = %+v", feed.Msg())
	}
	secondID := make([]byte, 24)
	secondID[0] = 2
	if got := add(secondID); got != 2 {
		t.Fatalf("new Add effective weight = %v, want 2", got)
	}
	waitForOrigin(4)
	for _, node := range []*pumpNode{origin, follower} {
		if _, ok := node.cache.GetVertex("dedup/tail"); !ok {
			t.Fatalf("%x did not create endpoint for new Add", node.nodeID)
		}
		if weight, ok := node.cache.GetWeight("dedup/tail", "dedup/head"); !ok || weight != 2 {
			t.Fatalf("%x new Add Edge = %v/%t, want 2/true", node.nodeID, weight, ok)
		}
	}
	if !feed.Receive() || feed.Msg().GetIdentityChunk() == nil || feed.Msg().GetIdentityChunk().GetSeq() != 4 {
		t.Fatalf("identity CDC missed new Add before mixed batch: %v", feed.Err())
	}
	// A single public batch mixes a receiver-local duplicate with accepted
	// contributions. The future private Add sidecar records those decisions;
	// current identity CDC still conservatively names every original key.
	thirdID := make([]byte, 24)
	thirdID[0] = 3
	mixed, err := origin.raw.AddEdges(ctx, connect.NewRequest(&pb.AddEdgesRequest{
		Edges: []*pb.Edge{
			{Tail: "dedup/tail", Head: "dedup/head", Weight: 1, Expiration: expiration},
			{Tail: "dedup/tail", Head: "dedup/head", Weight: 3, Expiration: expiration},
			{Tail: "fresh/tail", Head: "fresh/head", Weight: 1, Expiration: expiration},
		},
		ContribIds: [][]byte{firstID, thirdID, nil},
	}))
	if err != nil {
		t.Fatalf("mixed AddEdges over h2c: %v", err)
	}
	if mixed.Msg.GetWritten() != 3 || len(mixed.Msg.GetEffectiveWeights()) != 3 ||
		mixed.Msg.GetEffectiveWeights()[0] != 2 || mixed.Msg.GetEffectiveWeights()[1] != 5 || mixed.Msg.GetEffectiveWeights()[2] != 1 {
		t.Fatalf("mixed AddEdges over h2c = %v, %v", mixed, err)
	}
	if !feed.Receive() {
		t.Fatalf("identity CDC missed mixed Add: %v", feed.Err())
	}
	chunk = feed.Msg().GetIdentityChunk()
	if chunk == nil || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE ||
		chunk.GetSeq() != 5 || len(chunk.GetEdgeKeys()) != 3 ||
		chunk.GetEdgeKeys()[0].GetTail() != "dedup/tail" ||
		chunk.GetEdgeKeys()[1].GetTail() != "dedup/tail" ||
		chunk.GetEdgeKeys()[2].GetTail() != "fresh/tail" {
		t.Fatalf("mixed Add changed conservative identity projection: %+v", feed.Msg())
	}
	waitForOrigin(5)
	for _, node := range []*pumpNode{origin, follower} {
		if weight, ok := node.cache.GetWeight("dedup/tail", "dedup/head"); !ok || weight != 5 {
			t.Fatalf("%x mixed Add duplicate changed weight = %v/%t, want 5/true", node.nodeID, weight, ok)
		}
		if weight, ok := node.cache.GetWeight("fresh/tail", "fresh/head"); !ok || weight != 1 {
			t.Fatalf("%x mixed Add fresh Edge = %v/%t, want 1/true", node.nodeID, weight, ok)
		}
	}
}

// Synthetic log entries exercise the production-disabled wire projection on
// real Connect/h2c. No public receipt write or remote apply path is enabled.
func TestIdentityCDC_ReceiptEdgeDeleteTailFailsClosedAndPreservesCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x15}
	node := newPumpNode(t, origin)
	rep := newReplicationRawClient(t, node.url)
	for seq, accepted := range [][]bool{{true, false}, {false}} {
		mutation, stamp := receiptEdgeDeleteTailFixture(t, origin, uint64(seq+1), accepted)
		if _, err := node.log.Append(mutation, stamp); err != nil {
			t.Fatal(err)
		}
	}

	legacy, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err == nil {
		if legacy.Receive() {
			t.Fatalf("legacy full Subscribe received downgrade frame: %+v", legacy.Msg())
		}
		err = legacy.Err()
		_ = legacy.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("legacy full Subscribe error = %v, want InvalidArgument", err)
	}
	full, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1, AcceptReceiptEnvelopes: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = full.Close() }()
	for seq, count := range []int{2, 1} {
		if !full.Receive() {
			t.Fatalf("receipt full frame %d: %v", seq+1, full.Err())
		}
		mutation := full.Msg().GetMutation()
		call := mutation.GetOp().GetReplicatedReceiptEdgeDelete()
		if mutation.GetSeq() != uint64(seq+1) || call == nil || len(call.GetItems()) != count || mutation.GetOp().GetDeleteEdges() != nil {
			t.Fatalf("receipt full frame %d lost envelope: %+v", seq+1, full.Msg())
		}
	}
	identity, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection:       pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = identity.Close() }()
	if !identity.Receive() {
		t.Fatalf("accepted identity: %v", identity.Err())
	}
	accepted := identity.Msg().GetIdentityChunk()
	if accepted == nil || accepted.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE ||
		accepted.GetSeq() != 1 || len(accepted.GetEdgeKeys()) != 1 || accepted.GetEdgeKeys()[0].GetHead() != "edge/0" {
		t.Fatalf("accepted identity = %+v", identity.Msg())
	}
	if !identity.Receive() {
		t.Fatalf("receipt-only identity: %v", identity.Err())
	}
	marker := identity.Msg().GetIdentityChunk()
	if marker == nil || marker.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY ||
		marker.GetSeq() != 2 || marker.GetChunkIndex() != 0 || marker.GetFirstItemIndex() != 0 ||
		!marker.GetIsLast() || len(marker.GetVertexKeys())+len(marker.GetEdgeKeys()) != 0 {
		t.Fatalf("receipt-only marker = %+v", identity.Msg())
	}

	bad, stamp := receiptEdgeDeleteTailFixture(t, origin, 3, []bool{false})
	bad.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt.IntentSha256[0] ^= 1
	if _, err := node.log.Append(bad, stamp); err != nil {
		t.Fatal(err)
	}
	malformed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 3, AcceptReceiptEnvelopes: true}))
	if err == nil {
		if malformed.Receive() {
			t.Fatalf("malformed receipt full frame escaped: %+v", malformed.Msg())
		}
		err = malformed.Err()
		_ = malformed.Close()
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("malformed receipt full error = %v, want Internal", err)
	}
}

func TestIdentityCDC_VertexReceiptTailsUseRealWireAndFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x16}
	node := newPumpNode(t, origin)
	rep := newReplicationRawClient(t, node.url)
	put, putStamp, deleteMutation, deleteStamp := receiptVertexTailFixtures(t, origin)
	if _, err := node.log.Append(put, putStamp); err != nil {
		t.Fatal(err)
	}
	if _, err := node.log.Append(deleteMutation, deleteStamp); err != nil {
		t.Fatal(err)
	}

	legacy, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err == nil {
		if legacy.Receive() {
			t.Fatalf("legacy full Subscribe received Vertex receipt frame: %+v", legacy.Msg())
		}
		err = legacy.Err()
		_ = legacy.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("legacy Vertex receipt Subscribe = %v, want InvalidArgument", err)
	}

	full, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		FromLocalSeq: 1, AcceptReceiptEnvelopes: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = full.Close() }()
	if !full.Receive() {
		t.Fatalf("Vertex Put full frame: %v", full.Err())
	}
	putFrame := full.Msg().GetMutation()
	putCall := putFrame.GetOp().GetReplicatedReceiptVertexPut()
	if putFrame.GetSeq() != 1 || putCall == nil || len(putCall.GetItems()) != 3 ||
		putCall.GetItems()[0].GetOriginal().GetExpiration() != nil ||
		putCall.GetItems()[0].GetAccepted().GetLive().GetExpiration() != nil ||
		putCall.GetItems()[1].GetAccepted() != nil ||
		putCall.GetItems()[2].GetAccepted().GetCausalBarrier().GetKey() != "vertex/expired" {
		t.Fatalf("Vertex Put receipt envelope changed over h2c: %+v", full.Msg())
	}
	if !full.Receive() {
		t.Fatalf("Vertex Delete full frame: %v", full.Err())
	}
	deleteFrame := full.Msg().GetMutation()
	deleteCall := deleteFrame.GetOp().GetReplicatedReceiptVertexDelete()
	if deleteFrame.GetSeq() != 2 || deleteCall == nil || len(deleteCall.GetItems()) != 2 ||
		deleteCall.GetItems()[0].GetReceipt().GetOriginalResult().GetDeleteVertexExisted() != true ||
		deleteCall.GetItems()[1].GetReceipt().GetOriginalResult().GetDeleteVertexExisted() ||
		deleteCall.GetItems()[0].GetCausallyAccepted() ||
		deleteCall.GetItems()[1].GetCausallyAccepted() {
		t.Fatalf("Vertex Delete receipt envelope changed over h2c: %+v", full.Msg())
	}

	identity, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection:       pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = identity.Close() }()
	if !identity.Receive() {
		t.Fatalf("Vertex Put identity frame: %v", identity.Err())
	}
	putIdentity := identity.Msg().GetIdentityChunk()
	if putIdentity == nil ||
		putIdentity.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX ||
		putIdentity.GetSeq() != 1 ||
		!reflect.DeepEqual(putIdentity.GetVertexKeys(),
			[]string{"vertex/permanent", "vertex/expired"}) {
		t.Fatalf("Vertex Put identity projection = %+v", identity.Msg())
	}
	if !identity.Receive() {
		t.Fatalf("Vertex Delete receipt-only identity frame: %v", identity.Err())
	}
	deleteIdentity := identity.Msg().GetIdentityChunk()
	if deleteIdentity == nil ||
		deleteIdentity.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY ||
		deleteIdentity.GetSeq() != 2 || !deleteIdentity.GetIsLast() ||
		len(deleteIdentity.GetVertexKeys())+len(deleteIdentity.GetEdgeKeys()) != 0 {
		t.Fatalf("Vertex Delete receipt-only projection = %+v", identity.Msg())
	}

	bad := proto.Clone(deleteMutation).(*pb.Mutation)
	bad.Seq = 3
	bad.Hlc.Logical++
	bad.GetOp().GetReplicatedReceiptVertexDelete().Items[1].Receipt.LogicalCallId[0] ^= 1
	if _, err := node.log.Append(bad, hlc.Timestamp{
		WallNs: bad.GetHlc().GetWallNs(), Logical: bad.GetHlc().GetLogical(), NodeID: origin,
	}); err != nil {
		t.Fatal(err)
	}
	malformed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		FromLocalSeq: 3, AcceptReceiptEnvelopes: true,
	}))
	if err == nil {
		if malformed.Receive() {
			t.Fatalf("mixed-group Vertex receipt frame escaped: %+v", malformed.Msg())
		}
		err = malformed.Err()
		_ = malformed.Close()
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("mixed-group Vertex receipt Subscribe = %v, want Internal", err)
	}
}

func TestIdentityCDC_BootstrapLiveExactVictimsAndFullCompatibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x01}
	node := newPumpNode(t, origin)
	rep := newReplicationRawClient(t, node.url)
	if _, err := node.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "cap/one", Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}},
		{Key: "cap/two", Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}},
	}})); err != nil {
		t.Fatal(err)
	}
	full, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if !full.Receive() || full.Msg().GetMutation() == nil || full.Msg().GetIdentityChunk() != nil {
		t.Fatalf("default Subscribe no longer returns full Mutation: (%v,%v)", full.Msg(), full.Err())
	}
	_ = full.Close()

	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() {
		t.Fatalf("bootstrap checkpoint: %v", feed.Err())
	}
	checkpoint := feed.Msg().GetCheckpoint()
	if checkpoint == nil || checkpoint.GetLastSeqPerOrigin()[hex.EncodeToString(origin[:])] != 1 || feed.Msg().GetMutation() != nil {
		t.Fatalf("bad identity checkpoint: %+v", feed.Msg())
	}
	if _, err := node.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "cap/", Limit: 1})); err != nil {
		t.Fatal(err)
	}
	if _, err := node.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "edge/a", Head: "edge/b", Weight: 72.5}, ContribId: []byte("PRIVATE-CONTRIBUTION-ID!")})); err != nil {
		t.Fatal(err)
	}
	if _, err := node.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "edge/a", Head: "edge/c", Weight: 91.25}})); err != nil {
		t.Fatal(err)
	}
	if _, err := node.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "edge/a", Head: "edge/b"})); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		seq  uint64
		op   pb.IdentityOperation
		tail string
		head string
	}{
		{seq: 2, op: pb.IdentityOperation_IDENTITY_OPERATION_DELETE_VERTEX},
		{seq: 3, op: pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE, tail: "edge/a", head: "edge/b"},
		{seq: 4, op: pb.IdentityOperation_IDENTITY_OPERATION_PUT_EDGE, tail: "edge/a", head: "edge/c"},
		{seq: 5, op: pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE, tail: "edge/a", head: "edge/b"},
	} {
		if !feed.Receive() {
			t.Fatalf("identity event seq %d: %v", expected.seq, feed.Err())
		}
		chunk := feed.Msg().GetIdentityChunk()
		if chunk == nil || chunk.GetSeq() != expected.seq || chunk.GetOperation() != expected.op || !chunk.GetIsLast() || chunk.GetChunkIndex() != 0 || len(chunk.GetOrigin()) != 16 || feed.Msg().GetMutation() != nil {
			t.Fatalf("bad identity event seq %d: %+v", expected.seq, feed.Msg())
		}
		if expected.seq == 2 {
			if len(chunk.GetVertexKeys()) != 1 || !strings.HasPrefix(chunk.GetVertexKeys()[0], "cap/") {
				t.Fatalf("prefix did not project exact victim: %+v", chunk)
			}
			survivor := "cap/one"
			if chunk.GetVertexKeys()[0] == survivor {
				survivor = "cap/two"
			}
			if _, err := node.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: survivor})); err != nil {
				t.Fatalf("capped prefix removed survivor: %v", err)
			}
		} else if len(chunk.GetEdgeKeys()) != 1 || chunk.GetEdgeKeys()[0].GetTail() != expected.tail || chunk.GetEdgeKeys()[0].GetHead() != expected.head {
			t.Fatalf("edge operation projected wrong pair: %+v", chunk)
		}
		encoded, err := protojson.Marshal(feed.Msg())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "PRIVATE-VALUE") || strings.Contains(string(encoded), "PRIVATE-CONTRIBUTION-ID!") || strings.Contains(string(encoded), "72.5") || strings.Contains(string(encoded), "91.25") {
			t.Fatalf("identity frame leaked payload: %s", encoded)
		}
	}
}

func TestIdentityCDC_ChunkedPluralAndVectorGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x02}
	node := newPumpNodeWithSearch(t, origin, 3, true)
	rep := newReplicationRawClient(t, node.url)
	vertices := make([]*pb.Vertex, 1025)
	for i := range vertices {
		vertices[i] = &pb.Vertex{Key: "bulk/" + strconv.Itoa(i), Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}}
	}
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing bootstrap checkpoint: %v", feed.Err())
	}
	if _, err := node.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
		t.Fatal(err)
	}
	for i, count := range []int{1024, 1} {
		if !feed.Receive() {
			t.Fatalf("plural chunk %d: %v", i, feed.Err())
		}
		chunk := feed.Msg().GetIdentityChunk()
		if chunk == nil || chunk.GetSeq() != 1 || len(chunk.GetVertexKeys()) != count || chunk.GetChunkIndex() != uint32(i) || chunk.GetIsLast() != (i == 1) || proto.Size(feed.Msg()) > 1<<20 {
			t.Fatalf("plural chunk %d invalid: %+v", i, feed.Msg())
		}
	}
	for i := 2; i <= 4; i++ {
		if _, err := node.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "gap/key"})); err != nil {
			t.Fatal(err)
		}
	}
	resume, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 3}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resume.Close() }()
	for _, seq := range []uint64{3, 4} {
		if !resume.Receive() || resume.Msg().GetIdentityChunk().GetSeq() != seq {
			t.Fatalf("resume seq %d: (%v,%v)", seq, resume.Msg(), resume.Err())
		}
	}
	gap, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 1}}))
	if err == nil {
		if gap.Receive() {
			t.Fatalf("evicted seq was silently skipped: %+v", gap.Msg())
		}
		err = gap.Err()
		_ = gap.Close()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
		t.Fatalf("evicted vector error = %v", err)
	}
	bad, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, FromSeqPerOrigin: map[string]uint64{"BAD": 1}}))
	if err == nil {
		if bad.Receive() {
			t.Fatalf("invalid cursor produced frame: %+v", bad.Msg())
		}
		err = bad.Err()
		_ = bad.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid cursor error = %v", err)
	}
}

func TestIdentityCDC_CappedEdgePrefixProjectsOnlyCommittedVictim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	node := newPumpNode(t, hlc.NodeID{0xD1, 0x06})
	rep := newReplicationRawClient(t, node.url)
	for _, head := range []string{"cap/one", "cap/two"} {
		if _, err := node.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "cap/tail", Head: head, Weight: 9}})); err != nil {
			t.Fatal(err)
		}
	}
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing checkpoint: %v", feed.Err())
	}
	deleted, err := node.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "cap/", HeadPrefix: "cap/", Limit: 1}))
	if err != nil || deleted.Msg.GetDeleted() != 1 {
		t.Fatalf("capped edge delete = (%v,%v)", deleted, err)
	}
	if !feed.Receive() {
		t.Fatalf("missing capped edge identity: %v", feed.Err())
	}
	chunk := feed.Msg().GetIdentityChunk()
	if chunk == nil || chunk.GetSeq() != 3 || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE || len(chunk.GetEdgeKeys()) != 1 || !chunk.GetIsLast() {
		t.Fatalf("prefix projected wrong victim set: %+v", feed.Msg())
	}
	victim := chunk.GetEdgeKeys()[0]
	if victim.GetTail() != "cap/tail" || (victim.GetHead() != "cap/one" && victim.GetHead() != "cap/two") {
		t.Fatalf("wrong edge victim: %+v", victim)
	}
	survivor := "cap/one"
	if victim.GetHead() == survivor {
		survivor = "cap/two"
	}
	if _, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "cap/tail", Head: survivor})); err != nil {
		t.Fatalf("capped edge prefix deleted survivor: %v", err)
	}
}

type identityStreamMetrics struct {
	ended chan struct{}
}

func (*identityStreamMetrics) OnSubscribeStarted()       {}
func (m *identityStreamMetrics) OnSubscribeEnded()       { close(m.ended) }
func (*identityStreamMetrics) OnSubscribeDropped(string) {}

func TestIdentityCDC_CancelReleasesSubscriber(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	metrics := &identityStreamMetrics{ended: make(chan struct{})}
	node := newPumpNodeWithWALAndMetrics(t, hlc.NodeID{0xD1, 0x05}, 16, true, nil, metrics)
	rep := newReplicationRawClient(t, node.url)
	streamCtx, stop := context.WithCancel(ctx)
	feed, err := rep.Subscribe(streamCtx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		stop()
		t.Fatalf("missing checkpoint before cancel: (%v,%v)", feed.Msg(), feed.Err())
	}
	stop()
	if feed.Receive() {
		t.Fatalf("identity feed continued after cancellation: %+v", feed.Msg())
	}
	select {
	case <-metrics.ended:
	case <-ctx.Done():
		t.Fatal("canceled identity feed retained its subscriber")
	}
}

func TestIdentityCDC_SnapshotInstallGapsExistingStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	source := newPumpNodeWithSearch(t, hlc.NodeID{0xD1, 0x07}, 2, true)
	follower := newPumpNode(t, hlc.NodeID{0xD1, 0x08})
	for i := 0; i < 6; i++ {
		if _, err := source.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "snapshot/" + strconv.Itoa(i)}})); err != nil {
			t.Fatal(err)
		}
	}
	rep := newReplicationRawClient(t, follower.url)
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing pre-Snapshot checkpoint: %v", feed.Err())
	}
	metrics := &tombstoneSnapshotMetrics{failed: make(chan struct{}, 1), snapshots: make(chan struct{}, 1)}
	follower.startPumpWithMetrics(ctx, t, []string{source.url}, metrics)
	select {
	case <-metrics.snapshots:
	case <-metrics.failed:
		t.Fatal("peer Snapshot recovery failed")
	case <-ctx.Done():
		t.Fatal("peer Snapshot recovery did not complete")
	}
	if _, ok := follower.cache.GetVertex("snapshot/5"); !ok {
		t.Fatal("peer Snapshot did not change the graph")
	}
	if feed.Receive() || connect.CodeOf(feed.Err()) != connect.CodeFailedPrecondition {
		t.Fatalf("identity stream survived an unlogged Snapshot graph change: (%v,%v)", feed.Msg(), feed.Err())
	}
	fresh, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if !fresh.Receive() || fresh.Msg().GetCheckpoint().GetLastSeqPerOrigin()[hex.EncodeToString(source.nodeID[:])] != 6 {
		t.Fatalf("fresh checkpoint missed verified Snapshot cutoff: (%v,%v)", fresh.Msg(), fresh.Err())
	}
}

func TestIdentityCDC_AntiEntropySnapshotAlsoGapsExistingStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	source := newPumpNodeWithSearch(t, hlc.NodeID{0xD1, 0x09}, 2, true)
	follower := newPumpNode(t, hlc.NodeID{0xD1, 0x0A})
	for i := 0; i < 6; i++ {
		if _, err := source.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "anti-snapshot/" + strconv.Itoa(i)}})); err != nil {
			t.Fatal(err)
		}
	}
	rep := newReplicationRawClient(t, follower.url)
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing pre-Snapshot checkpoint: %v", feed.Err())
	}
	anti := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID: follower.nodeID, Peers: []string{source.url}, Interval: 20 * time.Millisecond,
		SubscribeTimeout: time.Second, HTTPClient: h2cClient(),
	}, follower.svc, follower.svc, follower.cache)
	antiCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- anti.Run(antiCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("anti-entropy did not stop within 2s")
		}
	})
	for follower.svc.LocalSeq(source.nodeID) < 6 {
		select {
		case <-ctx.Done():
			t.Fatal("anti-entropy Snapshot did not advance the source cutoff")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, ok := follower.cache.GetVertex("anti-snapshot/5"); !ok {
		t.Fatal("anti-entropy Snapshot did not change the graph")
	}
	if feed.Receive() || connect.CodeOf(feed.Err()) != connect.CodeFailedPrecondition {
		t.Fatalf("identity stream survived anti-entropy Snapshot: (%v,%v)", feed.Msg(), feed.Err())
	}
}

type checkpointHoldWAL struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (w *checkpointHoldWAL) Write(mutationlog.Entry) error {
	if w.calls.Add(1) == 1 {
		close(w.entered)
		<-w.release
	}
	return nil
}

func TestIdentityCDC_CheckpointAndTailSharePublicationCut(t *testing.T) {
	for _, mode := range []string{"local", "relay"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			wal := &checkpointHoldWAL{entered: make(chan struct{}), release: make(chan struct{})}
			node := newPumpNodeWithWAL(t, hlc.NodeID{0xD1, 0x03}, 16, true, wal)
			rep := newReplicationRawClient(t, node.url)
			writeDone := make(chan error, 1)
			origin := node.nodeID
			if mode == "local" {
				go func() {
					_, err := node.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "cut/first"}}))
					writeDone <- err
				}()
			} else {
				origin = hlc.NodeID{0xD1, 0x04}
				go func() {
					writeDone <- node.svc.ApplyMutation(ctx, &pb.Mutation{Seq: 1, Origin: origin[:], Hlc: &pb.HLCTimestamp{NodeId: origin[:], WallNs: time.Now().UnixNano()}, Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "cut/first"}}}}}})
				}()
			}
			select {
			case <-wal.entered:
			case <-ctx.Done():
				t.Fatal("write never entered WAL")
			}
			type opened struct {
				feed *connect.ServerStreamForClient[pb.SubscribeResponse]
				err  error
			}
			first := make(chan opened, 1)
			go func() {
				feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
				if err == nil {
					if !feed.Receive() {
						err = feed.Err()
					}
				}
				first <- opened{feed: feed, err: err}
			}()
			select {
			case got := <-first:
				t.Fatalf("checkpoint crossed graph-before-WAL cut: %+v", got)
			case <-time.After(150 * time.Millisecond):
			}
			close(wal.release)
			if err := <-writeDone; err != nil {
				t.Fatalf("committing first write: %v", err)
			}
			got := <-first
			if got.err != nil {
				t.Fatal(got.err)
			}
			defer func() { _ = got.feed.Close() }()
			if got.feed.Msg().GetCheckpoint().GetLastSeqPerOrigin()[hex.EncodeToString(origin[:])] != 1 {
				t.Fatalf("checkpoint skipped committed first write: %+v", got.feed.Msg())
			}
			if _, err := node.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "cut/second"}})); err != nil {
				t.Fatal(err)
			}
			if !got.feed.Receive() || got.feed.Msg().GetIdentityChunk().GetSeq() == 0 {
				t.Fatalf("live tail missed post-checkpoint write: (%v,%v)", got.feed.Msg(), got.feed.Err())
			}
		})
	}
}
