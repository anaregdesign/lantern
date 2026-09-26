package service

import (
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestReceiptEdgeAddDigestCanonical(t *testing.T) {
	var contribID graphcache.ContribID
	for i := range contribID {
		contribID[i] = byte(i + 1)
	}
	edge := &pb.Edge{
		Tail: "tail", Head: "head", Weight: -2.5,
		Expiration: &timestamppb.Timestamp{Seconds: 1700000000, Nanos: 123456789},
	}
	digest, err := receiptEdgeAddDigest(edge, contribID)
	if err != nil {
		t.Fatal(err)
	}
	const want = "f464e5c4ad38ff70f3b0ff038f04d29ac3f248f99e54ab48704081754c6ba19e"
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("canonical Add digest = %s, want %s", got, want)
	}

	for _, mutate := range []func(*pb.Edge, *graphcache.ContribID){
		func(edge *pb.Edge, _ *graphcache.ContribID) { edge.Tail = "tail-2" },
		func(edge *pb.Edge, _ *graphcache.ContribID) { edge.Head = "head-2" },
		func(edge *pb.Edge, _ *graphcache.ContribID) {
			edge.Weight = math.Float32frombits(math.Float32bits(edge.Weight) ^ 1)
		},
		func(edge *pb.Edge, _ *graphcache.ContribID) { edge.Expiration.Nanos++ },
		func(_ *pb.Edge, contribID *graphcache.ContribID) { contribID[0] ^= 1 },
	} {
		changedEdge := proto.Clone(edge).(*pb.Edge)
		changedContribID := contribID
		mutate(changedEdge, &changedContribID)
		changed, err := receiptEdgeAddDigest(changedEdge, changedContribID)
		if err != nil {
			t.Fatal(err)
		}
		if changed == digest {
			t.Fatal("semantic Add intent change preserved the digest")
		}
	}
}

func TestReceiptEdgeAddDigestRejectsInvalidIntent(t *testing.T) {
	valid := &pb.Edge{Tail: "tail", Head: "head", Weight: 1}
	unknown := proto.Clone(valid).(*pb.Edge)
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	for _, tc := range []struct {
		name      string
		edge      *pb.Edge
		contribID graphcache.ContribID
	}{
		{name: "nil Edge", contribID: graphcache.ContribID{1}},
		{name: "zero ContribID", edge: valid},
		{name: "empty tail", edge: &pb.Edge{Head: "head", Weight: 1}, contribID: graphcache.ContribID{1}},
		{name: "empty head", edge: &pb.Edge{Tail: "tail", Weight: 1}, contribID: graphcache.ContribID{1}},
		{name: "NaN", edge: &pb.Edge{Tail: "tail", Head: "head", Weight: float32(math.NaN())}, contribID: graphcache.ContribID{1}},
		{name: "infinity", edge: &pb.Edge{Tail: "tail", Head: "head", Weight: float32(math.Inf(1))}, contribID: graphcache.ContribID{1}},
		{
			name: "invalid expiration",
			edge: &pb.Edge{
				Tail: "tail", Head: "head", Weight: 1,
				Expiration: &timestamppb.Timestamp{Seconds: 253402300800},
			},
			contribID: graphcache.ContribID{1},
		},
		{name: "unknown field", edge: unknown, contribID: graphcache.ContribID{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := receiptEdgeAddDigest(tc.edge, tc.contribID); err == nil {
				t.Fatal("invalid Add intent was hashed")
			}
		})
	}
}

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

func TestReceiptEdgeAddWireCapacity(t *testing.T) {
	payload := strings.Repeat("x", receiptVertexWALMaxBytes)
	edge := &pb.Edge{Tail: "capacity", Head: "x", Weight: math.MaxFloat32}
	low, high := 1, len(payload)
	best := 0
	for low <= high {
		mid := low + (high-low)/2
		edge.Head = payload[:mid]
		if validateReceiptEdgeAddWALRequestCapacity([]*pb.Edge{edge}) == nil {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if best == 0 || best == len(payload) {
		t.Fatalf("receipt Add capacity boundary = %d, want an interior payload length", best)
	}
	edge.Head = payload[:best]
	if err := validateReceiptEdgeAddWALRequestCapacity([]*pb.Edge{edge}); err != nil {
		t.Fatalf("largest fitting receipt Add request rejected: %v", err)
	}
	edge.Head = payload[:best+1]
	if err := validateReceiptEdgeAddWALRequestCapacity([]*pb.Edge{edge}); !errors.Is(err, errReceiptEdgeAddWireCapacity) {
		t.Fatalf("first oversized receipt Add request = %v, want wire-capacity error", err)
	}

	envelope := committedReceiptEdgeAddEnvelope(t, 1)
	envelope.Original[0].Head = payload
	digest, err := receiptEdgeAddDigest(envelope.Original[0], envelope.ContribIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	envelope.Receipts[0].Digest = digest
	envelope.Mutation = receiptEdgeAddMutation(envelope)
	if _, err := envelope.ReplicationMutation(); !errors.Is(err, errReceiptEdgeAddWireCapacity) {
		t.Fatalf("oversized receipt Add envelope = %v, want wire-capacity error", err)
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
