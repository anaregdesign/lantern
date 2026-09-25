package service

import (
	"bytes"
	"context"
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

func wireReceiptVertexPutFixture(t *testing.T) *vertexPutReceiptEnvelope {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x42}
	origin := hlc.NodeID{0x41}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	issued := time.Now().Add(-time.Second)
	group := mutationreceipt.GroupID{0x7f}
	original := []*pb.Vertex{
		{
			Key: "accepted", Value: &pb.Vertex_String_{String_: "value"},
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		},
		{Key: "no-op", Value: &pb.Vertex_Int64{Int64: 42}},
	}
	results := []pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
	}
	receipts := make([]mutationreceipt.Receipt, len(original))
	for i, vertex := range original {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		digest, err := vertexPutDigest(vertex, true)
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: group, Index: uint32(i), Count: uint32(len(original)),
				Kind: mutationreceipt.PutVertex, Digest: digest,
			},
			Result: []byte{byte(results[i])}, DeadlineMillis: issued.Add(time.Hour).UnixMilli(),
		}
	}
	envelope := &vertexPutReceiptEnvelope{
		Origin: origin, OriginSeq: 3, HLC: stamp,
		Epoch: epoch, PolicyFingerprint: [32]byte{0x51}, IfAbsent: true,
		Original: original,
		Accepted: []graphcache.IndexedVertexPut[string, *pb.Vertex]{{
			Index: 0, Outcome: graphcache.PutOutcomeAppliedAndLive,
			Item: graphcache.VertexItem[string, *pb.Vertex]{
				Key: "accepted", Value: proto.Clone(original[0]).(*pb.Vertex),
				Expiration: original[0].GetExpiration().AsTime(),
			},
		}},
		Receipts: receipts,
	}
	envelope.Mutation = receiptVertexPutGraphMutation(envelope)
	return envelope
}

func TestReceiptVertexPutWireCarriesOriginalResultsAndProjection(t *testing.T) {
	envelope := wireReceiptVertexPutFixture(t)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	call := wire.GetOp().GetReplicatedReceiptVertexPut()
	if call == nil || !call.GetIfAbsent() || len(call.GetItems()) != 2 ||
		call.GetItems()[0].GetReceipt().GetOriginalResult().GetPutVertexOutcome() !=
			pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
		call.GetItems()[1].GetReceipt().GetOriginalResult().GetPutVertexOutcome() !=
			pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET ||
		call.GetItems()[0].GetAccepted().GetLive().GetString_() != "value" ||
		call.GetItems()[1].GetAccepted() != nil {
		t.Fatalf("receipt-bearing wire lost original intent/result/projection: %+v", wire)
	}
	keys, err := acceptedReceiptVertexPutKeys(wire)
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
		frames[0].GetIdentityChunk().GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX ||
		len(frames[0].GetIdentityChunk().GetVertexKeys()) != 1 ||
		frames[0].GetIdentityChunk().GetVertexKeys()[0] != "accepted" {
		t.Fatalf("identity projection = %+v", frames)
	}
	envelope.Original[0].GetValue().(*pb.Vertex_String_).String_ = "mutated"
	envelope.Receipts[0].Result[0] = byte(pb.PutOutcome_PUT_OUTCOME_EXPIRED)
	if call.GetItems()[0].GetOriginal().GetString_() != "value" ||
		call.GetItems()[0].GetReceipt().GetOriginalResult().GetPutVertexOutcome() !=
			pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatal("wire aliases mutable private envelope")
	}
}

func TestReceiptVertexPutWireRejectsMalformedEnvelope(t *testing.T) {
	wire, err := wireReceiptVertexPutFixture(t).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"mixed group", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[1].Receipt.LogicalCallId[0] ^= 1
		}},
		{"digest", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Receipt.IntentSha256[0] ^= 1
		}},
		{"index", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Receipt.ItemIndex = 1
		}},
		{"count", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[1].Receipt.ItemCount = 1
		}},
		{"duplicate ID", func(m *pb.Mutation) {
			items := m.GetOp().GetReplicatedReceiptVertexPut().Items
			items[1].Receipt.OperationId = append([]byte(nil), items[0].Receipt.OperationId...)
		}},
		{"invalid original", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Original.Key = ""
		}},
		{"result and effect drift", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Receipt.OriginalResult =
				&pb.ReceiptResult{Result: &pb.ReceiptResult_PutVertexOutcome{
					PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
				}}
		}},
		{"effect key drift", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Accepted.GetLive().Key = "different"
		}},
		{"typed nil effect", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Accepted.Outcome =
				(*pb.ReplicatedPutVertex_Live)(nil)
		}},
		{"nested unknown", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Receipt.ProtoReflect().
				SetUnknown([]byte{0xf8, 0x07, 0x01})
		}},
		{"outer tombstone expiration", func(m *pb.Mutation) {
			m.TombstoneExpiration = timestamppb.New(time.Now().Add(time.Hour))
		}},
		{"typed nil arm", func(m *pb.Mutation) {
			m.Op.Op = &pb.MutationOp_ReplicatedReceiptVertexPut{}
		}},
		{"over 8 MiB", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptVertexPut().Items[0].Original.Key =
				string(bytes.Repeat([]byte{'x'}, receiptVertexWALMaxBytes))
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			bad := proto.Clone(wire).(*pb.Mutation)
			tc.mutate(bad)
			if _, err := decodeReceiptVertexPutMutation(bad); err == nil {
				t.Fatal("malformed Vertex Put receipt wire decoded")
			}
		})
	}
}

func TestReceiptVertexPutWireProducerAndCanonicalCodecFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*vertexPutReceiptEnvelope)
	}{
		{"mixed group", func(e *vertexPutReceiptEnvelope) { e.Receipts[1].Group[0] ^= 1 }},
		{"contribution", func(e *vertexPutReceiptEnvelope) {
			e.Receipts[0].HasContrib = true
			e.Receipts[0].ContribID[0] = 1
		}},
		{"duplicate ID", func(e *vertexPutReceiptEnvelope) { e.Receipts[1].ID = e.Receipts[0].ID }},
		{"graph projection drift", func(e *vertexPutReceiptEnvelope) {
			e.Mutation.GetOp().GetReplicatedPutVertices().Entries[0].GetLive().Key = "different"
		}},
		{"accepted order", func(e *vertexPutReceiptEnvelope) {
			e.Accepted = append(e.Accepted, e.Accepted[0])
		}},
		{"permanent expired result", func(e *vertexPutReceiptEnvelope) {
			e.Original[0].Expiration = nil
			e.Receipts[0].Result[0] = byte(pb.PutOutcome_PUT_OUTCOME_EXPIRED)
			e.Receipts[0].Digest, _ = vertexPutDigest(e.Original[0], e.IfAbsent)
			e.Accepted[0] = graphcache.IndexedVertexPut[string, *pb.Vertex]{
				Index: 0, Outcome: graphcache.PutOutcomeExpired,
				Item: graphcache.VertexItem[string, *pb.Vertex]{
					Key: "accepted", CausalBarrier: true,
				},
			}
			e.Mutation = receiptVertexPutGraphMutation(e)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := wireReceiptVertexPutFixture(t)
			tc.mutate(envelope)
			if _, err := envelope.ReplicationMutation(); err == nil {
				t.Fatal("invalid private envelope produced wire")
			}
		})
	}

	envelope := wireReceiptVertexPutFixture(t)
	raw, err := encodeReceiptVertexPutWAL(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptVertexPutWAL(raw)
	if err != nil {
		t.Fatal(err)
	}
	decodedWire, err := decoded.(*vertexPutReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	originalWire, _ := envelope.ReplicationMutation()
	if !proto.Equal(decodedWire, originalWire) {
		t.Fatal("canonical WAL round trip changed envelope")
	}
	noncanonical := append(append([]byte(nil), raw...),
		protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), envelope.OriginSeq)...)
	if _, err := decodeReceiptVertexPutWAL(noncanonical); err == nil {
		t.Fatal("noncanonical duplicate field decoded")
	}
	if _, err := decodeReceiptVertexPutWAL(make([]byte, receiptVertexWALMaxBytes+1)); err == nil {
		t.Fatal("oversized WAL payload decoded")
	}
}

func TestDecodeReceiptVertexPutMutationOwnsWire(t *testing.T) {
	wire, err := wireReceiptVertexPutFixture(t).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeReceiptVertexPutMutation(wire)
	if err != nil {
		t.Fatal(err)
	}
	wire.Origin[0] ^= 1
	item := wire.GetOp().GetReplicatedReceiptVertexPut().Items[0]
	item.Original.GetValue().(*pb.Vertex_String_).String_ = "caller-mutated"
	item.Accepted.GetLive().Key = "caller-mutated"
	item.Receipt.OperationId[0] ^= 1
	if envelope.Origin[0] != 0x41 || envelope.Original[0].GetString_() != "value" ||
		envelope.Accepted[0].Item.Key != "accepted" || envelope.Receipts[0].ID[0] != 1 {
		t.Fatalf("decoded envelope aliases caller wire: %+v", envelope)
	}
}

func TestReceiptVertexPutApplyRejectsMixedGroupsWithoutMutation(t *testing.T) {
	origin := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0xa1}, 8, nil)
	remote := newReceiptVertexPutFixture(t, nil, hlc.NodeID{0xa2}, 8, nil)
	call := receiptVertexPutTestCall(t, origin.epoch, 0x71, false,
		&pb.Vertex{Key: "first"}, &pb.Vertex{Key: "second"})
	if _, err := origin.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	wire, err := origin.log.RetainedEntries()[0].Op.(*vertexPutReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	wire.GetOp().GetReplicatedReceiptVertexPut().Items[1].Receipt.LogicalCallId[0] ^= 1
	if err := remote.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("mixed-group ApplyMutation = %v, want InvalidArgument", err)
	}
	if remote.log.Len() != 0 || remote.service.LocalSeq(origin.service.clock.NodeID()) != 0 ||
		remote.store.Stats().Entries != 0 {
		t.Fatal("mixed-group ApplyMutation changed log, origin, or Store")
	}
}
