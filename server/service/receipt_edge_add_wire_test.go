package service

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
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

func TestReceiptEdgeAddOriginalResultBitsSurviveWireAndWAL(t *testing.T) {
	for _, tc := range []struct {
		name string
		bits uint32
	}{
		{"NaN", 0x7fc00001},
		{"positive infinity", 0x7f800000},
		{"negative infinity", 0xff800000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := committedReceiptEdgeAddEnvelope(t, 1)
			result := make([]byte, 4)
			binary.BigEndian.PutUint32(result, tc.bits)
			envelope.Receipts[0].Result = result
			envelope.Mutation = receiptEdgeAddMutation(envelope)
			wire, err := envelope.ReplicationMutation()
			if err != nil {
				t.Fatal(err)
			}
			if err := validateMutationEdgeSourceWeights(wire.GetOp()); err != nil {
				t.Fatalf("finite original rejected for non-finite RESULT: %v", err)
			}
			decoded, err := decodeReceiptEdgeAddMutation(wire)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded.Receipts[0].Result, result) {
				t.Fatalf("original wire result = %+v, %v, want %x", decoded, err, result)
			}
			raw, err := encodeGraphAddEffectWAL(envelope)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := decodeGraphAddEffectWAL(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(replayed.Receipts[0].Result, result) {
				t.Fatalf("original WAL result = %+v, %v, want %x", replayed, err, result)
			}
		})
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

func TestReceiptEdgeAddEndpointEffectWireWALAndIntent(t *testing.T) {
	envelope := committedReceiptEdgeAddEnvelope(t, 1)
	oldDigest := envelope.Receipts[0].Digest
	digest, err := receiptEdgeAddEffectDigest(envelope.Original[0], envelope.ContribIDs[0], true, envelope.NamespaceFormat)
	if err != nil || digest == oldDigest {
		t.Fatal("endpoint effect did not bind receipt intent", err)
	}
	envelope.NoEndpointCreation = true
	envelope.Receipts[0].Digest = digest
	envelope.Mutation = receiptEdgeAddMutation(envelope)
	raw, err := encodeReceiptWALUnion(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptWALUnion(raw)
	if err != nil {
		t.Fatal(err)
	}
	recovered, ok := decoded.(*graphAddEffectEnvelope)
	if !ok || !recovered.NoEndpointCreation || !recovered.Mutation.GetOp().GetNoEndpointCreation() || !sameReceiptEdgeAddIntent(envelope, recovered) {
		t.Fatal("WAL hydration lost immutable endpoint effect")
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	graph.RetainDanglingEdgeHistory()
	if err := replayReceiptEnvelopeGraph(graph, recovered); err != nil {
		t.Fatal(err)
	}
	if len(graph.SnapshotVertices()) != 0 || len(graph.SnapshotReplication().Graph.Edges) != 1 {
		t.Fatal("receipt WAL replay fabricated endpoints or lost source")
	}
	recovered.NoEndpointCreation = false
	if _, err := validateReceiptEdgeAddEnvelope(recovered); err == nil {
		t.Fatal("effect drift accepted")
	}
}

// canonicalReceiptEdgeAddValidationEnvelope uses only immutable protocol data:
// it opens no service, Store, Log, listener or native-time runtime. The same
// fixture and full-validator benchmark run against baseline and candidate.
func canonicalReceiptEdgeAddValidationEnvelope(tb testing.TB, count int) *graphAddEffectEnvelope {
	tb.Helper()
	if count != 1 && count != 16 {
		tb.Fatal("canonical validation fixture requires 1 or 16 items")
	}
	issued := time.Unix(1700000000, 0).UTC()
	origin := hlc.NodeID{0x65}
	epoch := mutationreceipt.Epoch{0x66}
	e := &graphAddEffectEnvelope{
		NamespaceFormat:    keyspace.Version,
		NoEndpointCreation: true,
		Origin:             origin,
		OriginSeq:          1,
		HLC:                hlc.Timestamp{WallNs: issued.UnixNano(), NodeID: origin},
		Epoch:              epoch,
		PolicyFingerprint:  [32]byte{0x67},
		Original:           make([]*pb.Edge, count),
		ContribIDs:         make([]graphcache.ContribID, count),
		AcceptedIndexes:    make([]uint32, count),
		Receipts:           make([]mutationreceipt.Receipt, count),
	}
	for i := range count {
		tail, err := keyspace.DataKey("canonical:tail:" + string(rune('a'+i)))
		if err != nil {
			tb.Fatal(err)
		}
		head, err := keyspace.DataKey("canonical:head")
		if err != nil {
			tb.Fatal(err)
		}
		edge := &pb.Edge{
			Tail: tail, Head: head, Weight: 1.25,
			Expiration: timestamppb.New(issued.Add(time.Hour)),
		}
		contribID := graphcache.ContribID{0x68, byte(i + 1)}
		digest, err := receiptEdgeAddEffectDigest(edge, contribID, true, e.NamespaceFormat)
		if err != nil {
			tb.Fatal(err)
		}
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{0x69, byte(i + 1)})
		if err != nil {
			tb.Fatal(err)
		}
		resource, err := receiptResourceIdentity(e.NamespaceFormat, tail, head)
		if err != nil {
			tb.Fatal(err)
		}
		var receiptContrib mutationreceipt.ContribID
		copy(receiptContrib[:], contribID[:])
		result := make([]byte, 4)
		binary.BigEndian.PutUint32(result, math.Float32bits(1.25))
		e.Original[i] = edge
		e.ContribIDs[i] = contribID
		e.AcceptedIndexes[i] = uint32(i)
		e.Receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: mutationreceipt.GroupID{0x6a},
				Index: uint32(i), Count: uint32(count), Kind: mutationreceipt.AddEdge,
				Digest: digest, ContribID: receiptContrib, HasContrib: true, Resource: resource,
			},
			Result: result, DeadlineMillis: issued.Add(time.Hour).UnixMilli(),
		}
	}
	e.Mutation = receiptEdgeAddMutation(e)
	return e
}

func BenchmarkReceiptEdgeAddCanonicalValidation(b *testing.B) {
	for _, tc := range []struct {
		name  string
		count int
	}{
		{"single", 1},
		{"batch16", 16},
	} {
		b.Run(tc.name, func(b *testing.B) {
			envelope := canonicalReceiptEdgeAddValidationEnvelope(b, tc.count)
			wantRetention := int64(time.Hour / time.Millisecond)
			if retention, err := validateReceiptEdgeAddEnvelope(envelope); err != nil || retention != wantRetention {
				b.Fatalf("fixture validation = %d, %v", retention, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if retention, err := validateReceiptEdgeAddEnvelope(envelope); err != nil || retention != wantRetention {
					b.Fatalf("canonical validation = %d, %v", retention, err)
				}
			}
		})
	}
}

func TestReceiptEdgeAddCanonicalValidationExactBits(t *testing.T) {
	for _, tc := range []struct {
		name string
		bits uint32
	}{
		{"positive_zero", 0x00000000},
		{"negative_zero", 0x80000000},
		{"nan_payload_one", 0x7fc00001},
		{"nan_payload_two", 0x7fc00002},
		{"positive_infinity", 0x7f800000},
		{"negative_infinity", 0xff800000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := canonicalReceiptEdgeAddValidationEnvelope(t, 1)
			binary.BigEndian.PutUint32(envelope.Receipts[0].Result, tc.bits)
			envelope.Mutation = receiptEdgeAddMutation(envelope)
			before, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope.Mutation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateReceiptEdgeAddEnvelope(envelope); err != nil {
				t.Fatalf("exact original result rejected: %v", err)
			}
			after, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope.Mutation)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("validation changed the owned wire mutation", err)
			}
			item := envelope.Mutation.GetOp().GetReplicatedReceiptEdgeAdd().Items[0]
			gotBits := math.Float32bits(item.Receipt.OriginalResult.GetAddEdgeEffectiveWeight())
			if gotBits != tc.bits {
				t.Fatalf("owned wire result bits = %08x, want %08x", gotBits, tc.bits)
			}
			item.Receipt.OriginalResult.Result = &pb.ReceiptResult_AddEdgeEffectiveWeight{
				AddEdgeEffectiveWeight: math.Float32frombits(tc.bits ^ 1),
			}
			if _, err := validateReceiptEdgeAddEnvelope(envelope); err == nil {
				t.Fatal("different original-result bits accepted")
			}
		})
	}
}

func TestReceiptEdgeAddCanonicalValidationRejectsWireDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"namespace", func(m *pb.Mutation) { m.NamespaceFormat = "" }},
		{"origin", func(m *pb.Mutation) { m.Origin[0] ^= 1 }},
		{"sequence", func(m *pb.Mutation) { m.Seq++ }},
		{"hlc", func(m *pb.Mutation) { m.Hlc.WallNs++ }},
		{"endpoint_effect", func(m *pb.Mutation) { m.Op.NoEndpointCreation = false }},
		{"epoch", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().DeploymentEpoch[0] ^= 1 }},
		{"policy", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().PolicyFingerprint[0] ^= 1 }},
		{"original", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Original.Tail += "changed" }},
		{"expiration_presence", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Original.Expiration = nil }},
		{"contribution", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].ContribId[0] ^= 1 }},
		{"accepted_projection", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].CausallyAccepted = false }},
		{"operation_id", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.OperationId[25] ^= 1 }},
		{"logical_call", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.LogicalCallId[0] ^= 1 }},
		{"item_index", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.ItemIndex++ }},
		{"item_count", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.ItemCount++ }},
		{"digest", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.IntentSha256[0] ^= 1 }},
		{"deadline", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.DeadlineUnixMs++ }},
		{"result_presence", func(m *pb.Mutation) { m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.OriginalResult = nil }},
		{"result_arm", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.OriginalResult.Result =
				&pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: true}
		}},
		{"unknown_outer", func(m *pb.Mutation) { m.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{"unknown_edge", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Original.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"unknown_result", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.OriginalResult.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := canonicalReceiptEdgeAddValidationEnvelope(t, 16)
			tc.mutate(envelope.Mutation)
			if _, err := validateReceiptEdgeAddEnvelope(envelope); err == nil {
				t.Fatal("wire evidence drift accepted")
			}
		})
	}
}

func TestReceiptEdgeAddPublishingMutationOwnsPayload(t *testing.T) {
	envelope := canonicalReceiptEdgeAddValidationEnvelope(t, 16)
	owned := envelope.Mutation
	before, err := (proto.MarshalOptions{Deterministic: true}).Marshal(owned)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate every slice-backed evidence family after construction. Publishing
	// must own its payload even if validation later borrows an expected view.
	envelope.Origin[0] ^= 1
	envelope.HLC.NodeID[0] ^= 1
	envelope.Epoch[0] ^= 1
	envelope.PolicyFingerprint[0] ^= 1
	envelope.AcceptedIndexes[0] = 1
	for i := range envelope.Original {
		envelope.Original[i].Tail += "changed"
		envelope.Original[i].Weight = -7
		envelope.Original[i].Expiration.Nanos++
		envelope.ContribIDs[i][0] ^= 1
		envelope.Receipts[i].ID[25] ^= 1
		envelope.Receipts[i].Group[0] ^= 1
		envelope.Receipts[i].Digest[0] ^= 1
		envelope.Receipts[i].Result[0] ^= 1
	}
	after, err := (proto.MarshalOptions{Deterministic: true}).Marshal(owned)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("publishing mutation aliases source evidence", err)
	}
}

// refreshCanonicalReceiptEdgeAddValidationEnvelope updates origin evidence for
// a test's semantic changes, then builds the independently owned wire payload.
func refreshCanonicalReceiptEdgeAddValidationEnvelope(tb testing.TB, e *graphAddEffectEnvelope) {
	tb.Helper()
	for i, edge := range e.Original {
		digest, err := receiptEdgeAddEffectDigest(edge, e.ContribIDs[i], e.NoEndpointCreation, e.NamespaceFormat)
		if err != nil {
			tb.Fatal(err)
		}
		resource, err := receiptResourceIdentity(e.NamespaceFormat, edge.GetTail(), edge.GetHead())
		if err != nil {
			tb.Fatal(err)
		}
		e.Receipts[i].Digest = digest
		e.Receipts[i].Resource = resource
	}
	e.Mutation = receiptEdgeAddMutation(e)
}

func TestReceiptEdgeAddValidationProjectionMatchesOwnedBytes(t *testing.T) {
	for _, profile := range []struct {
		name       string
		count      int
		namespaced bool
		protected  bool
		expiration string
		accepted   string
	}{
		{"protected_single", 1, true, true, "ttl", "all"},
		{"protected_subset", 16, true, true, "nil", "subset"},
		{"protected_no_effect", 16, true, true, "zero", "none"},
		{"normal_namespaced", 16, true, false, "ttl", "all"},
		{"normal_legacy", 16, false, false, "nil", "subset"},
	} {
		t.Run(profile.name, func(t *testing.T) {
			for _, result := range []struct {
				name string
				bits uint32
			}{
				{"finite", 0x3fa00000},
				{"positive_zero", 0x00000000},
				{"negative_zero", 0x80000000},
				{"positive_infinity", 0x7f800000},
				{"negative_infinity", 0xff800000},
				{"quiet_nan_one", 0x7fc00001},
				{"quiet_nan_two", 0x7fc00002},
				{"negative_nan", 0xffc00003},
				{"signalling_nan", 0x7f800001},
			} {
				t.Run(result.name, func(t *testing.T) {
					e := canonicalReceiptEdgeAddValidationEnvelope(t, profile.count)
					e.NoEndpointCreation = profile.protected
					e.HLC.Logical = 0x180
					e.OriginSeq = 129
					if !profile.namespaced {
						e.NamespaceFormat = ""
					}
					e.AcceptedIndexes = nil
					for i, edge := range e.Original {
						if !profile.namespaced {
							var err error
							edge.Tail, err = keyspace.LogicalKey(edge.Tail)
							if err != nil {
								t.Fatal(err)
							}
							edge.Head, err = keyspace.LogicalKey(edge.Head)
							if err != nil {
								t.Fatal(err)
							}
						}
						switch profile.expiration {
						case "nil":
							edge.Expiration = nil
						case "zero":
							edge.Expiration = &timestamppb.Timestamp{}
						}
						if profile.accepted == "all" || profile.accepted == "subset" && i%2 == 0 {
							e.AcceptedIndexes = append(e.AcceptedIndexes, uint32(i))
						}
						binary.BigEndian.PutUint32(e.Receipts[i].Result, result.bits)
					}
					refreshCanonicalReceiptEdgeAddValidationEnvelope(t, e)
					ownedRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(e.Mutation)
					if err != nil {
						t.Fatal(err)
					}
					projection := receiptEdgeAddValidationProjection(e)
					borrowedRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(projection)
					if err != nil || !bytes.Equal(ownedRaw, borrowedRaw) || proto.Size(e.Mutation) != proto.Size(projection) {
						t.Fatal("borrowed canonical bytes or size differ from owned construction", err)
					}
					retention, err := validateReceiptEdgeAddEnvelope(e)
					if err != nil || retention != int64(time.Hour/time.Millisecond) {
						t.Fatalf("canonical validation = %d, %v", retention, err)
					}
					decoded, err := decodeReceiptEdgeAddMutation(projection)
					if err != nil {
						t.Fatal(err)
					}
					for i := range decoded.Receipts {
						if !bytes.Equal(decoded.Receipts[i].Result, e.Receipts[i].Result) {
							t.Fatalf("decoded original-result bytes differ at item %d", i)
						}
					}
					// Compare semantic wire bytes, not protobuf MessageState or size
					// caches, which Marshal/Size may update during a read.
					after, err := (proto.MarshalOptions{Deterministic: true}).Marshal(receiptEdgeAddMutation(e))
					if err != nil || !bytes.Equal(ownedRaw, after) {
						t.Fatal("validation changed source evidence", err)
					}
				})
			}
		})
	}
}

func TestReceiptEdgeAddValidationProjectionRefusesEnvelopeDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*graphAddEffectEnvelope)
		want   string
	}{
		{"first_header_failure", func(e *graphAddEffectEnvelope) {
			e.OriginSeq = 0
			e.Receipts[0].Result = nil
		}, "invalid receipt Add origin"},
		{"provenance", func(e *graphAddEffectEnvelope) { e.Receipts[0].Resource.Key += "changed" }, "original resource provenance drift"},
		{"invalid_ttl", func(e *graphAddEffectEnvelope) { e.Original[0].Expiration.Nanos = 1000000000 }, "receipt Add edge 0"},
		{"digest", func(e *graphAddEffectEnvelope) { e.Receipts[0].Digest[0] ^= 1 }, "receipt Add intent or index drift"},
		{"duplicate_id", func(e *graphAddEffectEnvelope) { e.Receipts[1].ID = e.Receipts[0].ID }, "duplicate receipt Add operation ID"},
		{"retention", func(e *graphAddEffectEnvelope) { e.Receipts[1].DeadlineMillis++ }, "inconsistent receipt Add retention"},
		{"result_length", func(e *graphAddEffectEnvelope) { e.Receipts[0].Result = []byte{1} }, "invalid receipt Add result"},
		{"accepted_order", func(e *graphAddEffectEnvelope) { e.AcceptedIndexes = []uint32{1, 0} }, "invalid receipt Add accepted index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := canonicalReceiptEdgeAddValidationEnvelope(t, 16)
			tc.mutate(e)
			if _, err := validateReceiptEdgeAddEnvelope(e); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("envelope drift = %v, want %q", err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pb.Mutation)
	}{
		{"unknown_hlc", func(m *pb.Mutation) { m.Hlc.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{"unknown_op", func(m *pb.Mutation) { m.Op.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{"unknown_call", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"unknown_item", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"unknown_receipt", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Receipt.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"unknown_expiration", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeAdd().Items[0].Original.Expiration.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := canonicalReceiptEdgeAddValidationEnvelope(t, 1)
			tc.mutate(e.Mutation)
			if _, err := validateReceiptEdgeAddEnvelope(e); err == nil {
				t.Fatal("unknown wire field accepted")
			}
		})
	}
}

func TestReceiptEdgeAddValidationProjectionCapacityBoundary(t *testing.T) {
	e := canonicalReceiptEdgeAddValidationEnvelope(t, 1)
	payload := keyspace.DataPrefix + strings.Repeat("x", receiptVertexWALMaxBytes+1)
	wire := receiptEdgeAddMutation(e)
	item := wire.GetOp().GetReplicatedReceiptEdgeAdd().Items[0]
	low, high, best := len(keyspace.DataPrefix)+1, len(payload), -1
	for low <= high {
		mid := low + (high-low)/2
		item.Original.Head = payload[:mid]
		if proto.Size(wire) <= receiptVertexWALMaxBytes {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if best < len(keyspace.DataPrefix)+1 || best+1 > len(payload) {
		t.Fatalf("canonical capacity boundary = %d", best)
	}
	for _, count := range []int{best, best + 1} {
		e.Original[0].Head = payload[:count]
		refreshCanonicalReceiptEdgeAddValidationEnvelope(t, e)
		ownedSize := proto.Size(e.Mutation)
		if size := proto.Size(receiptEdgeAddValidationProjection(e)); size != ownedSize {
			t.Fatalf("borrowed size = %d, want owned %d", size, ownedSize)
		}
		_, err := validateReceiptEdgeAddEnvelope(e)
		if count == best {
			if ownedSize > receiptVertexWALMaxBytes || ownedSize < receiptVertexWALMaxBytes-1 || err != nil {
				t.Fatalf("largest fitting canonical mutation = %d, %v", ownedSize, err)
			}
		} else if ownedSize <= receiptVertexWALMaxBytes || !errors.Is(err, errReceiptEdgeAddWireCapacity) {
			t.Fatalf("first oversized canonical mutation = %d, %v", ownedSize, err)
		}
	}
}

func TestReceiptEdgeAddValidationProjectionConcurrentReads(t *testing.T) {
	// A separate but identical fixture supplies expected bytes without warming
	// the shared fixture's newly constructed generated messages.
	wantRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonicalReceiptEdgeAddValidationEnvelope(t, 16).Mutation)
	if err != nil {
		t.Fatal(err)
	}
	e := canonicalReceiptEdgeAddValidationEnvelope(t, 16)
	for i, edge := range e.Original {
		// Construct fields explicitly; never copy generated MessageState.
		e.Original[i] = &pb.Edge{
			Tail: edge.GetTail(), Head: edge.GetHead(), Weight: edge.GetWeight(),
			Expiration: &timestamppb.Timestamp{
				Seconds: edge.GetExpiration().GetSeconds(), Nanos: edge.GetExpiration().GetNanos(),
			},
		}
	}
	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			for j := 0; j < 16; j++ {
				retention, err := validateReceiptEdgeAddEnvelope(e)
				if err != nil {
					results <- err
					return
				}
				if retention != int64(time.Hour/time.Millisecond) {
					results <- errors.New("concurrent validation retention drift")
					return
				}
				raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(e.Mutation)
				if err != nil || !bytes.Equal(wantRaw, raw) {
					results <- errors.New("concurrent serialization changed wire bytes")
					return
				}
			}
			results <- nil
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	after, err := (proto.MarshalOptions{Deterministic: true}).Marshal(receiptEdgeAddMutation(e))
	if err != nil || !bytes.Equal(wantRaw, after) {
		t.Fatal("concurrent reads changed source evidence", err)
	}
}
