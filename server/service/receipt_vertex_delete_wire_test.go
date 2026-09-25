package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func wireReceiptVertexDeleteFixture(t *testing.T) *vertexDeleteReceiptEnvelope {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x42}
	origin := hlc.NodeID{0x41}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	issued := time.Now().Add(-time.Second)
	group := mutationreceipt.GroupID{0x7f}
	keys := []string{"accepted", "no-op"}
	receipts := make([]mutationreceipt.Receipt, len(keys))
	for i, key := range keys {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: group, Index: uint32(i), Count: uint32(len(keys)),
				Kind: mutationreceipt.DeleteVertex, Digest: vertexDeleteDigest(key),
			},
			Result: []byte{byte(1 - i)}, DeadlineMillis: issued.Add(time.Hour).UnixMilli(),
		}
	}
	envelope := &vertexDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: 3, HLC: stamp,
		Epoch: epoch, PolicyFingerprint: [32]byte{0x51},
		TombstoneExpiration: time.Now().Add(time.Hour),
		OriginalKeys:        keys,
		Accepted: []graphcache.IndexedVertexDelete[string]{
			{Index: 0, Key: keys[0]},
		},
		Receipts: receipts,
	}
	envelope.Mutation = receiptVertexDeleteGraphMutation(envelope)
	return envelope
}

func TestReceiptVertexDeleteWireCarriesOriginalResultsAndProjection(t *testing.T) {
	envelope := wireReceiptVertexDeleteFixture(t)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	call := wire.GetOp().GetReplicatedReceiptVertexDelete()
	if call == nil || len(call.GetItems()) != 2 ||
		!call.GetItems()[0].GetReceipt().GetOriginalResult().GetDeleteVertexExisted() ||
		call.GetItems()[1].GetReceipt().GetOriginalResult().GetDeleteVertexExisted() ||
		!call.GetItems()[0].GetCausallyAccepted() ||
		call.GetItems()[1].GetCausallyAccepted() {
		t.Fatalf("receipt-bearing wire lost original result/projection: %+v", wire)
	}
	keys, err := acceptedReceiptVertexDeleteKeys(wire)
	if err != nil || len(keys) != 1 || keys[0] != "accepted" {
		t.Fatalf("accepted keys = %v, %v", keys, err)
	}
	var frames []*pb.SubscribeResponse
	if err := projectMutationIdentities(wire, func(frame *pb.SubscribeResponse) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 ||
		frames[0].GetIdentityChunk().GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_VERTEX ||
		len(frames[0].GetIdentityChunk().GetVertexKeys()) != 1 ||
		frames[0].GetIdentityChunk().GetVertexKeys()[0] != "accepted" {
		t.Fatalf("identity projection = %+v", frames)
	}
	envelope.OriginalKeys[0] = "mutated"
	envelope.Receipts[0].Result[0] = 0
	if call.GetItems()[0].GetKey() != "accepted" ||
		!call.GetItems()[0].GetReceipt().GetOriginalResult().GetDeleteVertexExisted() {
		t.Fatal("wire aliases mutable private envelope")
	}
}

func TestReceiptVertexDeleteWireRejectsMalformedEnvelope(t *testing.T) {
	wire, err := wireReceiptVertexDeleteFixture(t).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"mixed group", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[1].Receipt.LogicalCallId[0] ^= 1
		}},
		{"digest", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[0].Receipt.IntentSha256[0] ^= 1
		}},
		{"index", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[0].Receipt.ItemIndex = 1
		}},
		{"count", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[1].Receipt.ItemCount = 1
		}},
		{"duplicate ID", func(m *pb.Mutation) {
			items := m.GetOp().GetReplicatedReceiptVertexDelete().Items
			items[1].Receipt.OperationId = append([]byte(nil), items[0].Receipt.OperationId...)
		}},
		{"empty key", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[0].Key = ""
		}},
		{"wrong result kind", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[0].Receipt.OriginalResult =
				&pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: true}}
		}},
		{"unequal retention", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[1].Receipt.DeadlineUnixMs++
		}},
		{"zero expiration", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().TombstoneExpiration =
				timestamppb.New(time.Unix(0, 0))
		}},
		{"nested unknown", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[0].Receipt.ProtoReflect().
				SetUnknown([]byte{0xf8, 0x07, 0x01})
		}},
		{"outer tombstone expiration", func(m *pb.Mutation) {
			m.TombstoneExpiration = timestamppb.New(time.Now().Add(time.Hour))
		}},
		{"typed nil arm", func(m *pb.Mutation) {
			m.Op.Op = &pb.MutationOp_ReplicatedReceiptVertexDelete{}
		}},
		{"over 8 MiB", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexDelete().Items[0].Key =
				string(bytes.Repeat([]byte{'x'}, receiptVertexWALMaxBytes))
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			bad := proto.Clone(wire).(*pb.Mutation)
			tc.mutate(bad)
			if _, err := decodeReceiptVertexDeleteMutation(bad); err == nil {
				t.Fatal("malformed Vertex Delete receipt wire decoded")
			}
		})
	}
}

func TestReceiptVertexDeleteWireProducerAndCanonicalCodecFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*vertexDeleteReceiptEnvelope)
	}{
		{"mixed group", func(e *vertexDeleteReceiptEnvelope) { e.Receipts[1].Group[0] ^= 1 }},
		{"contribution", func(e *vertexDeleteReceiptEnvelope) {
			e.Receipts[0].HasContrib = true
			e.Receipts[0].ContribID[0] = 1
		}},
		{"duplicate ID", func(e *vertexDeleteReceiptEnvelope) { e.Receipts[1].ID = e.Receipts[0].ID }},
		{"graph projection drift", func(e *vertexDeleteReceiptEnvelope) {
			e.Mutation.GetOp().GetDeleteVertices().Keys[0] = "different"
		}},
		{"accepted order", func(e *vertexDeleteReceiptEnvelope) {
			e.Accepted = append(e.Accepted, e.Accepted[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := wireReceiptVertexDeleteFixture(t)
			tc.mutate(envelope)
			if _, err := envelope.ReplicationMutation(); err == nil {
				t.Fatal("invalid private envelope produced wire")
			}
		})
	}

	envelope := wireReceiptVertexDeleteFixture(t)
	raw, err := encodeReceiptVertexDeleteWAL(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptVertexDeleteWAL(raw)
	if err != nil {
		t.Fatal(err)
	}
	decodedWire, err := decoded.(*vertexDeleteReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	originalWire, _ := envelope.ReplicationMutation()
	if !proto.Equal(decodedWire, originalWire) {
		t.Fatal("canonical WAL round trip changed envelope")
	}
	noncanonical := append(append([]byte(nil), raw...),
		protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), envelope.OriginSeq)...)
	if _, err := decodeReceiptVertexDeleteWAL(noncanonical); err == nil {
		t.Fatal("noncanonical duplicate field decoded")
	}
	if _, err := decodeReceiptVertexDeleteWAL(make([]byte, receiptVertexWALMaxBytes+1)); err == nil {
		t.Fatal("oversized WAL payload decoded")
	}
}

func TestReceiptVertexDeleteWALPreflightBoundsItems(t *testing.T) {
	tooMany := receiptVertexWALItemsWire(
		protowire.Number(receiptVertexDeleteMutationArm),
		receiptVertexWALMaxItems+1,
		nil,
	)
	if _, err := decodeReceiptVertexDeleteWAL(tooMany); err == nil ||
		!strings.Contains(err.Error(), "item count exceeds") {
		t.Fatalf("excessive item preflight = %v, want item-count rejection", err)
	}
	malformed := receiptVertexWALMalformedItemWire(
		protowire.Number(receiptVertexDeleteMutationArm),
	)
	if _, err := decodeReceiptVertexDeleteWAL(malformed); err == nil ||
		!strings.Contains(err.Error(), "preflight") {
		t.Fatalf("malformed item preflight = %v, want framing rejection", err)
	}
	truncated := append(
		protowire.AppendTag(nil, 4, protowire.BytesType),
		0x80,
	)
	if _, err := decodeReceiptVertexDeleteWAL(truncated); err == nil ||
		!strings.Contains(err.Error(), "preflight") {
		t.Fatalf("truncated outer preflight = %v, want framing rejection", err)
	}

	wire, err := wireReceiptVertexDeleteFixture(t).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	wire.GetOp().GetReplicatedReceiptVertexDelete().Items =
		make([]*pb.ReplicatedReceiptVertexDeleteItem, receiptVertexWALMaxItems+1)
	if _, err := decodeReceiptVertexDeleteMutation(wire); err == nil {
		t.Fatal("decoded-message item cap was not enforced")
	}
}

func TestDecodeReceiptVertexDeleteMutationOwnsWire(t *testing.T) {
	wire, err := wireReceiptVertexDeleteFixture(t).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeReceiptVertexDeleteMutation(wire)
	if err != nil {
		t.Fatal(err)
	}
	wire.Origin[0] ^= 1
	item := wire.GetOp().GetReplicatedReceiptVertexDelete().Items[0]
	item.Key = "caller-mutated"
	item.Receipt.OperationId[0] ^= 1
	item.Receipt.OriginalResult = &pb.ReceiptResult{
		Result: &pb.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: false},
	}
	if envelope.Origin[0] != 0x41 || envelope.OriginalKeys[0] != "accepted" ||
		envelope.Accepted[0].Key != "accepted" || envelope.Receipts[0].ID[0] != 1 ||
		envelope.Receipts[0].Result[0] != 1 {
		t.Fatalf("decoded envelope aliases caller wire: %+v", envelope)
	}
}

func TestReceiptVertexDeleteApplyRejectsMixedGroupsWithoutMutation(t *testing.T) {
	origin := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0xb1}, 8, nil)
	remote := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0xb2}, 8, nil)
	call := receiptVertexDeleteTestCall(t, origin.epoch, 0x72, "first", "second")
	if _, err := origin.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	wire, err := origin.log.RetainedEntries()[0].Op.(*vertexDeleteReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	wire.GetOp().GetReplicatedReceiptVertexDelete().Items[1].Receipt.LogicalCallId[0] ^= 1
	if err := remote.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("mixed-group ApplyMutation = %v, want InvalidArgument", err)
	}
	if remote.log.Len() != 0 || remote.service.LocalSeq(origin.service.clock.NodeID()) != 0 ||
		remote.store.Stats().Entries != 0 {
		t.Fatal("mixed-group ApplyMutation changed log, origin, or Store")
	}
}
