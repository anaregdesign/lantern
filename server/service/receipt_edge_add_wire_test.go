package service

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func committedReceiptEdgeAddEnvelope(
	t *testing.T,
	count int,
) *graphAddEffectEnvelope {
	t.Helper()
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x65}, 32, 1<<20)
	coordinator, err := newEdgeAddReceiptCoordinator(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	edges := make([]*pb.Edge, count)
	for i := range edges {
		edges[i] = &pb.Edge{
			Tail: "wire-tail", Head: string(rune('a' + i)), Weight: float32(i + 1),
		}
	}
	if _, err := coordinator.Commit(
		t.Context(),
		receiptEdgeAddTestCall(t, f.epoch, 0x65, edges...),
	); err != nil {
		t.Fatal(err)
	}
	envelope, ok := f.log.RetainedEntries()[0].Op.(*graphAddEffectEnvelope)
	if !ok {
		t.Fatalf("receipt Add WAL payload = %T", f.log.RetainedEntries()[0].Op)
	}
	return envelope
}

func TestReceiptEdgeAddWireAndWALRoundTrip(t *testing.T) {
	envelope := committedReceiptEdgeAddEnvelope(t, 2)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptEdgeAddMutation(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !sameReceiptEdgeAddIntent(envelope, decoded) ||
		!proto.Equal(envelope.Mutation, decoded.Mutation) ||
		len(decoded.AcceptedIndexes) != 2 {
		t.Fatalf("wire round trip drift: original=%+v decoded=%+v", envelope, decoded)
	}

	raw, err := encodeGraphAddEffectWAL(envelope)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := decodeGraphAddEffectWAL(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !sameReceiptEdgeAddIntent(envelope, replayed) ||
		len(replayed.AcceptedIndexes) != 2 {
		t.Fatalf("WAL round trip drift: original=%+v decoded=%+v", envelope, replayed)
	}
	info, ok := receiptWALEnvelopeInfo(replayed)
	if !ok || info.origin != envelope.Origin ||
		len(info.receipts) != len(envelope.Receipts) {
		t.Fatalf("receipt WAL classification = %+v, %v", info, ok)
	}
}

func TestReceiptEdgeAddWireRejectsMalformedEvidence(t *testing.T) {
	envelope := committedReceiptEdgeAddEnvelope(t, 2)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"short ContribID", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].ContribId = []byte{1}
		}},
		{"zero ContribID", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].ContribId = make([]byte, 24)
		}},
		{"missing receipt", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt = nil
		}},
		{"wrong result arm", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.OriginalResult =
				&pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: true}}
		}},
		{"intent digest drift", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.IntentSha256[0] ^= 1
		}},
		{"duplicate operation ID", func(m *pb.Mutation) {
			items := m.GetOp().GetReplicatedReceiptEdgeAdd().Items
			items[1].Receipt.OperationId = append([]byte(nil), items[0].Receipt.OperationId...)
		}},
		{"duplicate ContribID", func(m *pb.Mutation) {
			items := m.GetOp().GetReplicatedReceiptEdgeAdd().Items
			items[1].ContribId = append([]byte(nil), items[0].ContribId...)
			copy(items[1].Receipt.IntentSha256, items[0].Receipt.IntentSha256)
		}},
		{"unknown field", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].ProtoReflect().
				SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := proto.Clone(wire).(*pb.Mutation)
			test.mutate(malformed)
			if decoded, err := decodeReceiptEdgeAddMutation(malformed); err == nil || decoded != nil {
				t.Fatalf("decode malformed wire = %+v, %v", decoded, err)
			}
		})
	}
}

func TestReceiptEdgeAddIntentIgnoresRelayAcceptedProjection(t *testing.T) {
	envelope := committedReceiptEdgeAddEnvelope(t, 1)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	wire.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].CausallyAccepted = false
	relay, err := decodeReceiptEdgeAddMutation(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !sameReceiptEdgeAddIntent(envelope, relay) ||
		len(envelope.AcceptedIndexes) != 1 || len(relay.AcceptedIndexes) != 0 {
		t.Fatalf("accepted projection changed origin intent: origin=%+v relay=%+v", envelope, relay)
	}
}

func TestReceiptEdgeAddStatusRejectsInvalidStoredResult(t *testing.T) {
	id := mutationreceipt.ID{1}
	_, err := receiptStatusProto(id, mutationreceipt.Observation{
		Status: mutationreceipt.Confirmed,
		Receipt: mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{ID: id, Kind: mutationreceipt.AddEdge},
			Result: []byte{1, 2, 3},
		},
	})
	if err == nil {
		t.Fatal("invalid Add result was exposed as a confirmed receipt")
	}
}
