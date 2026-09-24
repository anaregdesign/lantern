package service

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func wireReceiptEdgeDeleteFixture(t *testing.T) *edgeDeleteReceiptEnvelope {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x42}
	origin := hlc.NodeID{0x41}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	issued := time.Now().Add(-time.Second)
	group := mutationreceipt.GroupID{0x7f}
	keys := []graphcache.EdgeKey[string]{
		{Tail: "tail", Head: "accepted"},
		{Tail: "tail", Head: "no-op"},
	}
	receipts := make([]mutationreceipt.Receipt, len(keys))
	for i, key := range keys {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{ID: id, Group: group, Index: uint32(i), Count: uint32(len(keys)),
				Kind: mutationreceipt.DeleteEdge, Digest: edgeDeleteDigest(key.Tail, key.Head)},
			Result: []byte{byte(1 - i)}, DeadlineMillis: issued.Add(time.Hour).UnixMilli(),
		}
	}
	return &edgeDeleteReceiptEnvelope{
		Mutation: &pb.Mutation{Seq: 3, Origin: origin[:], Hlc: hlcToProto(stamp),
			Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{
				Edges: []*pb.EdgeKey{{Tail: keys[0].Tail, Head: keys[0].Head}},
			}}}},
		Origin: origin, OriginSeq: 3, HLC: stamp,
		Epoch: epoch, PolicyFingerprint: [32]byte{0x51},
		TombstoneExpiration: time.Now().Add(time.Hour),
		OriginalKeys:        keys,
		Accepted:            []graphcache.IndexedEdgeDelete[string]{{Index: 0, Key: keys[0]}},
		Receipts:            receipts,
	}
}

func TestReceiptEdgeDeleteWireCarriesOriginalAndAcceptedPositions(t *testing.T) {
	envelope := wireReceiptEdgeDeleteFixture(t)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	call := wire.GetOp().GetReplicatedReceiptEdgeDelete()
	if wire.GetOp().GetDeleteEdges() != nil || call == nil || len(call.GetItems()) != 2 ||
		!call.GetItems()[0].GetCausallyAccepted() || call.GetItems()[1].GetCausallyAccepted() ||
		!call.GetItems()[0].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() ||
		call.GetItems()[1].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() {
		t.Fatalf("receipt-bearing wire lost original results or accepted indexes: %+v", wire)
	}
	accepted, err := acceptedReceiptEdgeDeleteKeys(wire)
	if err != nil || len(accepted) != 1 || accepted[0].GetHead() != "accepted" {
		t.Fatalf("accepted keys = %+v, %v", accepted, err)
	}
	var frames []*pb.SubscribeResponse
	if err := projectMutationIdentities(wire, func(frame *pb.SubscribeResponse) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].GetIdentityChunk().GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE ||
		len(frames[0].GetIdentityChunk().GetEdgeKeys()) != 1 ||
		frames[0].GetIdentityChunk().GetEdgeKeys()[0].GetHead() != "accepted" {
		t.Fatalf("identity projection invalidated no-op item: %+v", frames)
	}
	envelope.OriginalKeys[0].Head = "changed"
	envelope.Receipts[0].Result[0] = 0
	if call.GetItems()[0].GetKey().GetHead() != "accepted" || !call.GetItems()[0].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() {
		t.Fatal("wire aliases mutable private WAL envelope")
	}
}

func TestReceiptEdgeDeleteWireReceiptOnlyAndTampering(t *testing.T) {
	envelope := wireReceiptEdgeDeleteFixture(t)
	envelope.Accepted = nil
	envelope.Mutation.GetOp().GetDeleteEdges().Edges = nil
	envelope.Receipts[0].Result[0] = 0
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	var frames []*pb.SubscribeResponse
	if err := projectMutationIdentities(wire, func(frame *pb.SubscribeResponse) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("receipt-only frame count = %d, want 1", len(frames))
	}
	chunk := frames[0].GetIdentityChunk()
	if chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY ||
		!chunk.GetIsLast() || chunk.GetChunkIndex() != 0 || chunk.GetFirstItemIndex() != 0 ||
		len(chunk.GetVertexKeys())+len(chunk.GetEdgeKeys()) != 0 {
		t.Fatalf("receipt-only final marker = %+v", frames)
	}
	mutations := []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"missing epoch", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().DeploymentEpoch = nil }},
		{"zero epoch", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().DeploymentEpoch = make([]byte, 16) }},
		{"zero policy", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().PolicyFingerprint = make([]byte, 32) }},
		{"digest", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt.IntentSha256[0] ^= 1 }},
		{"result", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt.OriginalResult = nil }},
		{"index", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt.ItemIndex = 1 }},
		{"key", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Key = nil }},
		{"invalid UTF-8", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Key.Head = "\xff" }},
		{"duplicate ID", func(m *pb.Mutation) {
			items := m.GetOp().GetReplicatedReceiptEdgeDelete().Items
			items[1].Receipt.OperationId = append([]byte(nil), items[0].Receipt.OperationId...)
		}},
		{"short retention", func(m *pb.Mutation) {
			receipt := m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt
			receipt.DeadlineUnixMs = binary.BigEndian.Uint64(receipt.OperationId[17:25]) + 1
		}},
		{"unequal retention", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeDelete().Items[1].Receipt.DeadlineUnixMs++ }},
		{"zero expiration", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeDelete().TombstoneExpiration = timestamppb.New(time.Unix(0, 0))
		}},
		{"origin mismatch", func(m *pb.Mutation) { m.Origin[0] ^= 1 }},
		{"HLC node mismatch", func(m *pb.Mutation) { m.Hlc.NodeId[0] ^= 1 }},
		{"zero HLC wall", func(m *pb.Mutation) { m.Hlc.WallNs = 0 }},
		{"typed nil arm", func(m *pb.Mutation) { m.Op.Op = &pb.MutationOp_ReplicatedReceiptEdgeDelete{} }},
		{"over 8 MiB", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Key.Head = string(bytes.Repeat([]byte{'x'}, receiptEdgeDeleteWALMaxBytes))
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			bad := proto.Clone(wire).(*pb.Mutation)
			tc.mutate(bad)
			if err := projectMutationIdentities(bad, func(*pb.SubscribeResponse) error {
				t.Fatal("malformed receipt envelope advanced identity cursor")
				return nil
			}); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("tampered envelope = %v, want Internal", err)
			}
		})
	}
	if bytes.Equal(wire.GetOp().GetReplicatedReceiptEdgeDelete().GetDeploymentEpoch(), make([]byte, 16)) {
		t.Fatal("wire lost deployment epoch")
	}
}

func TestReceiptEdgeDeleteWireProducerUsesWALValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*edgeDeleteReceiptEnvelope)
	}{
		{"duplicate ID", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[1].ID = e.Receipts[0].ID }},
		{"invalid UTF-8", func(e *edgeDeleteReceiptEnvelope) { e.OriginalKeys[1].Head = "\xff" }},
		{"unequal retention", func(e *edgeDeleteReceiptEnvelope) { e.Receipts[1].DeadlineMillis++ }},
		{"HLC origin mismatch", func(e *edgeDeleteReceiptEnvelope) { e.HLC.NodeID[0] ^= 1 }},
		{"graph projection drift", func(e *edgeDeleteReceiptEnvelope) { e.Mutation.GetOp().GetDeleteEdges().Edges[0].Head = "different" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := wireReceiptEdgeDeleteFixture(t)
			tc.mutate(e)
			if _, err := e.ReplicationMutation(); err == nil {
				t.Fatal("invalid private envelope produced receipt wire")
			}
		})
	}
}
