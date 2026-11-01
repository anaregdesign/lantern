package dataruntime

import (
	"encoding/binary"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestGraphMutationWireRoundTrip(t *testing.T) {
	e := graphMutationEnvelopeFixture(t)
	if _, err := encodeGraphMutationWAL((*graphMutationEnvelope)(nil)); err == nil {
		t.Fatal("nil envelope accepted")
	}
	typedNil := proto.Clone(e.Mutation).(*pb.Mutation)
	typedNil.Op.Op = (*pb.MutationOp_GraphMutation)(nil)
	if _, err := decodeGraphMutation(typedNil); err == nil {
		t.Fatal("typed nil arm accepted")
	}
	raw, err := encodeReceiptWALUnion(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptWALUnion(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !sameGraphMutationIntent(e, decoded.(*graphMutationEnvelope)) || !proto.Equal(e.Mutation, decoded.(*graphMutationEnvelope).Mutation) {
		t.Fatal("round trip drift")
	}
	if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 1, HLC: hlcFromProto(e.Mutation.GetHlc()), Op: decoded}); err != nil {
		t.Fatal(err)
	}
	size, err := e.replicationFrameSize()
	frame, frameErr := subscribeMutationFrame(e)
	if err != nil || frameErr != nil || size != proto.Size(frame) {
		t.Fatal(size, err, frameErr)
	}
	legacy := append([]byte(nil), raw...)
	copy(legacy[:8], receiptWALUnionMagicV9)
	if _, err := decodeReceiptWALUnion(legacy); err == nil {
		t.Fatal("V9 accepted mixed kind")
	}
	body, err := encodeGraphMutationWAL(e)
	if err != nil {
		t.Fatal(err)
	}
	// A new arm disguised as the frozen legacy graph kind must also fail.
	graphBody := make([]byte, receiptWALGraphHeaderSize+len(body))
	binary.BigEndian.PutUint32(graphBody, uint32(len(body)))
	copy(graphBody[receiptWALGraphHeaderSize:], body)
	if _, err := decodeReceiptWALGraph(graphBody); err == nil {
		t.Fatal("legacy graph tag accepted mixed arm")
	}
	for name, malformed := range map[string][]byte{
		"unknown":          protowire.AppendVarint(protowire.AppendTag(append([]byte(nil), body...), 99, protowire.VarintType), 1),
		"duplicate scalar": protowire.AppendVarint(protowire.AppendTag(append([]byte(nil), body...), 2, protowire.VarintType), e.Mutation.GetSeq()),
		"duplicate op":     protowire.AppendBytes(protowire.AppendTag(append([]byte(nil), body...), 4, protowire.BytesType), mustMarshalGraphTest(t, e.Mutation.GetOp())),
		"truncated":        body[:len(body)-1],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeGraphMutationWAL(malformed); err == nil {
				t.Fatal("malformed raw wire accepted")
			}
		})
	}
}

func mustMarshalGraphTest(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGraphMutationWireRejectsOriginProjectionDrift(t *testing.T) {
	e := graphMutationEnvelopeFixture(t)
	for _, tc := range []struct {
		name string
		edit func(*pb.ReplicatedGraphMutation)
	}{
		{"digest", func(c *pb.ReplicatedGraphMutation) { c.Items[0].Receipt.IntentSha256[0] ^= 1 }},
		{"global count", func(c *pb.ReplicatedGraphMutation) { c.Items[2].Receipt.ItemCount = 1 }},
		{"missing manifest", func(c *pb.ReplicatedGraphMutation) { c.Items[0] = nil }},
		{"duplicate projection", func(c *pb.ReplicatedGraphMutation) { c.Projection = append(c.Projection, c.Projection[0]) }},
		{"unaccepted edge", func(c *pb.ReplicatedGraphMutation) { c.Items[2].EdgeAccepted = false }},
		{"permanent barrier", func(c *pb.ReplicatedGraphMutation) {
			c.Projection[0].GetPutVertex().Outcome = &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: "a"}}
		}},
		{"typed nil", func(c *pb.ReplicatedGraphMutation) {
			c.Projection[0].GetPutVertex().Outcome = (*pb.ReplicatedPutVertex_Live)(nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := proto.Clone(e.Mutation).(*pb.Mutation)
			tc.edit(m.GetOp().GetGraphMutation())
			if _, err := decodeGraphMutation(m); err == nil {
				t.Fatal("drift accepted")
			}
		})
	}
}
