package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/protoschema"
)

func receiptWALUnionGraphFixture(op *pb.MutationOp) *pb.Mutation {
	origin := bytes.Repeat([]byte{0x31}, len(hlc.NodeID{}))
	m := &pb.Mutation{
		Seq: 7, Origin: origin,
		Hlc: &pb.HLCTimestamp{WallNs: 1730000000000000000, Logical: 4, NodeId: append([]byte(nil), origin...)},
		Op:  op,
	}
	switch op.GetOp().(type) {
	case *pb.MutationOp_DeleteVertex, *pb.MutationOp_DeleteVertices,
		*pb.MutationOp_DeleteEdge, *pb.MutationOp_DeleteEdges:
		m.TombstoneExpiration = timestamppb.New(time.Unix(1730003600, 0))
	}
	return m
}

func receiptWALUnionGraphHLC(m *pb.Mutation) hlc.Timestamp {
	var node hlc.NodeID
	copy(node[:], m.Hlc.NodeId)
	return hlc.Timestamp{WallNs: m.Hlc.WallNs, Logical: m.Hlc.Logical, NodeID: node}
}

func receiptWALUnionRawGraphProto(protobuf []byte, repeatedCount uint32) []byte {
	body := make([]byte, receiptWALGraphHeaderSize+len(protobuf))
	binary.BigEndian.PutUint32(body[:4], uint32(len(protobuf)))
	binary.BigEndian.PutUint32(body[4:8], repeatedCount)
	copy(body[receiptWALGraphHeaderSize:], protobuf)
	raw := make([]byte, receiptWALUnionHeaderSize+len(body))
	copy(raw[:8], receiptWALUnionMagic)
	raw[8] = receiptWALUnionGraph
	binary.BigEndian.PutUint32(raw[12:16], uint32(len(body)))
	copy(raw[receiptWALUnionHeaderSize:], body)
	return raw
}

func TestReceiptWALUnionCodecRejectsSupersededVersions(t *testing.T) {
	mutation := receiptWALUnionGraphFixture(&pb.MutationOp{
		Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{}},
	})
	current, err := encodeReceiptWALUnion(mutation)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []byte{2, 3} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			old := append([]byte(nil), current...)
			old[4] = version
			if _, err := decodeReceiptWALUnion(old); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("superseded WAL union v%d decoded: %v", version, err)
			}
		})
	}
}

func TestReceiptWALUnionGraphSchemaPinRejectsFutureField(t *testing.T) {
	current := (&pb.Mutation{}).ProtoReflect().Descriptor()
	if got := protoschema.Fingerprint(current); got != receiptWALGraphSchemaFingerprintV4 {
		t.Fatalf("WAL union v4 graph schema changed to %s; review replay and migration", got)
	}
	for _, tc := range []struct {
		name  string
		field *descriptorpb.FieldDescriptorProto
	}{
		{"scalar", &descriptorpb.FieldDescriptorProto{
			Name: proto.String("future_field"), Number: proto.Int32(99),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_UINT64.Enum(),
		}},
		{"recursive", &descriptorpb.FieldDescriptorProto{
			Name: proto.String("future_cycle"), Number: proto.Int32(99),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
			TypeName: proto.String(".graph.v1.MutationOp"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := protodesc.ToFileDescriptorProto(current.ParentFile())
			for _, message := range file.MessageType {
				if message.GetName() == "MutationOp" {
					message.Field = append(message.Field, tc.field)
					break
				}
			}
			changed, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
			if err != nil {
				t.Fatal(err)
			}
			if got := protoschema.Fingerprint(changed.Messages().ByName("Mutation")); got == receiptWALGraphSchemaFingerprintV4 {
				t.Fatal("new graph mutation field did not invalidate WAL union v4 schema")
			}
		})
	}
}

func TestReceiptWALUnionCodecNonDeleteGraphArms(t *testing.T) {
	// A graph Delete now requires its accepted-effect envelope. Other graph
	// arms retain the existing exact protobuf and nil-slot codec behavior.
	arms := []struct {
		name string
		op   *pb.MutationOp
	}{
		{"put vertex", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{}}}},
		{"put vertices", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{}}}},
		{"add edge", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{}}}},
		{"add edges", &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{}}}},
		{"put edge", &pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{}}}},
		{"put edges", &pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{}}}},
		{"replicated put vertices", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{}}}},
		{"replicated put edges", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{}}}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			want := receiptWALUnionGraphFixture(arm.op)
			raw, err := encodeReceiptWALUnion(want)
			if err != nil {
				t.Fatal(err)
			}
			again, err := encodeReceiptWALUnion(want)
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatalf("nondeterministic encode: %v", err)
			}
			decoded, err := decodeReceiptWALUnion(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := decoded.(*pb.Mutation)
			if !ok || !proto.Equal(got, want) {
				t.Fatalf("graph arm roundtrip: got %T %v, want %v", decoded, decoded, want)
			}
			if err := validateReceiptWALUnionEntry(mutationlog.Entry{Seq: 99, HLC: receiptWALUnionGraphHLC(got), Op: got}); err != nil {
				t.Fatalf("relay-local seq must be independent: %v", err)
			}
		})
	}
}

func TestReceiptWALUnionCodecRejectsReceiptContextInGraphArm(t *testing.T) {
	context := &pb.MutationReceiptContext{}
	for _, tc := range []struct {
		name string
		op   *pb.MutationOp
	}{
		{
			"put vertex",
			&pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{ReceiptContext: context},
			}},
		},
		{
			"put vertices",
			&pb.MutationOp{Op: &pb.MutationOp_PutVertices{
				PutVertices: &pb.PutVerticesRequest{ReceiptContext: context},
			}},
		},
		{
			"delete vertex",
			&pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
				DeleteVertex: &pb.DeleteVertexRequest{ReceiptContext: context},
			}},
		},
		{
			"delete vertices",
			&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
				DeleteVertices: &pb.DeleteVerticesRequest{ReceiptContext: context},
			}},
		},
		{
			"delete edge",
			&pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{
				DeleteEdge: &pb.DeleteEdgeRequest{ReceiptContext: context},
			}},
		},
		{
			"delete edges",
			&pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
				DeleteEdges: &pb.DeleteEdgesRequest{ReceiptContext: context},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := receiptWALUnionGraphFixture(tc.op)
			if _, err := encodeReceiptWALUnion(mutation); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("receipt context encoded as a graph mutation: %v", err)
			}
			protobuf, err := proto.Marshal(mutation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeReceiptWALUnion(
				receiptWALUnionRawGraphProto(protobuf, 0),
			); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("receipt context decoded as a graph mutation: %v", err)
			}
		})
	}
}

func TestReceiptWALUnionCodecPreservesGraphNilSlots(t *testing.T) {
	// Ordinary relay batches keep nil elements distinct from empty messages,
	// including their original request indexes.
	arms := []struct {
		name string
		op   *pb.MutationOp
	}{
		{"put vertices", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{nil, {}, {Key: "v"}, nil}}}}},
		{"add edges", &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{nil, {}, {Tail: "t", Head: "h"}, nil}}}}},
		{"put edges", &pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{Edges: []*pb.Edge{nil, {}, {Tail: "t", Head: "h"}, nil}}}}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			want := receiptWALUnionGraphFixture(arm.op)
			raw, err := encodeReceiptWALUnion(want)
			if err != nil {
				t.Fatal(err)
			}
			gotOp, err := decodeReceiptWALUnion(raw)
			if err != nil {
				t.Fatal(err)
			}
			got := gotOp.(*pb.Mutation)
			gotArm, err := receiptWALGraphArm(got)
			if err != nil {
				t.Fatal(err)
			}
			if gotArm.count != 4 || !nilProtoMessage(gotArm.at(0)) || nilProtoMessage(gotArm.at(1)) ||
				proto.Size(gotArm.at(1)) != 0 || nilProtoMessage(gotArm.at(2)) || !nilProtoMessage(gotArm.at(3)) {
				t.Fatalf("nil slots were lost: %+v", got.Op)
			}
			if !proto.Equal(got, want) {
				t.Fatalf("graph with nil slots changed: got %+v, want %+v", got, want)
			}
			reencoded, err := encodeReceiptWALUnion(got)
			if err != nil || !bytes.Equal(reencoded, raw) {
				t.Fatalf("noncanonical nil-slot roundtrip: %v", err)
			}
		})
	}
}

func TestReceiptWALUnionCodecRejectsUnservableReplicatedPutSlots(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   *pb.MutationOp
	}{
		{"vertex nil", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{nil}}}}},
		{"vertex missing outcome", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{{}}}}}},
		{"edge nil", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{Entries: []*pb.ReplicatedPutEdge{nil}}}}},
		{"edge missing outcome", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{Entries: []*pb.ReplicatedPutEdge{{}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := receiptWALUnionGraphFixture(tc.op)
			if _, err := encodeReceiptWALUnion(mutation); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("unservable replicated Put encoded: %v", err)
			}
			protobuf, err := proto.Marshal(mutation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeReceiptWALUnion(receiptWALUnionRawGraphProto(protobuf, 1)); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("unservable replicated Put decoded: %v", err)
			}
			path := filepath.Join(t.TempDir(), "replicated-put.wal")
			wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
			if err != nil {
				t.Fatal(err)
			}
			if err := wal.Write(mutationlog.Entry{Seq: 1, HLC: receiptWALUnionGraphHLC(mutation), Op: mutation}); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("unservable replicated Put appended: %v", err)
			}
			if err := wal.Close(); err != nil {
				t.Fatal(err)
			}
			if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(mutationlog.Entry) error {
				t.Fatal("replay visited an unservable replicated Put")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReceiptWALUnionCodecRejectsAliasedSyntheticAddIDs(t *testing.T) {
	edges := make([]*pb.Edge, maxSyntheticContribIndex+2)
	for i := range edges {
		edges[i] = &pb.Edge{Tail: "tail", Head: "head", Weight: 1}
	}
	for _, tc := range []struct {
		name  string
		seq   uint64
		count uint32
		op    *pb.MutationOp
	}{
		{"wire index", 7, uint32(len(edges)), &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: edges}}}},
		{"origin sequence", maxSyntheticContribSequence + 1, 0, &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "tail", Head: "head"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := receiptWALUnionGraphFixture(tc.op)
			mutation.Seq = tc.seq
			if _, err := encodeReceiptWALUnion(mutation); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("aliased Add encoded: %v", err)
			}
			protobuf, err := proto.Marshal(mutation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeReceiptWALUnion(receiptWALUnionRawGraphProto(protobuf, tc.count)); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("aliased Add decoded: %v", err)
			}
		})
	}
}

func TestReceiptWALUnionCodecMixedFileWALRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{nil, {Key: "v"}}}}})
	graphEntry := mutationlog.Entry{Seq: 1, HLC: receiptWALUnionGraphHLC(graph), Op: graph}
	if err := wal.Write(graphEntry); err != nil {
		t.Fatal(err)
	}
	_, envelope := receiptEdgeDeleteCodecFixture(t, nil)
	receiptEntry := mutationlog.Entry{Seq: 2, HLC: envelope.HLC, Op: envelope}
	if err := wal.Write(receiptEntry); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	visits := 0
	check := func(entry mutationlog.Entry) error {
		visits++
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return err
		}
		switch entry.Seq {
		case 1:
			got, ok := entry.Op.(*pb.Mutation)
			if !ok || got.GetOp().GetPutVertices().Vertices[0] != nil || !proto.Equal(got, graph) {
				return errors.New("graph mutation changed")
			}
		case 2:
			got, ok := entry.Op.(*edgeDeleteReceiptEnvelope)
			if !ok || !reflect.DeepEqual(got.Receipts, envelope.Receipts) || got.OriginSeq != envelope.OriginSeq {
				return errors.New("receipt envelope changed")
			}
		default:
			return errors.New("unexpected local sequence")
		}
		return nil
	}
	if err := mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, check); err != nil || visits != 2 {
		t.Fatalf("mixed FileWAL replay = %v, visits=%d", err, visits)
	}
	visits = 0
	resumed, err := mutationlog.ResumeFileWAL(path, encodeReceiptWALUnion, decodeReceiptWALUnion, check)
	if err != nil || visits != 2 {
		t.Fatalf("mixed FileWAL resume = %v, visits=%d", err, visits)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptWALUnionCodecDeleteDeadlineAndPhysicalDecisionFailClosed(t *testing.T) {
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
		DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "t", Head: "h"}}},
	}})
	envelope, err := newGraphDeleteEffectEnvelope(graph, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeReceiptWALUnion(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReceiptWALUnion(raw)
	if err != nil || !proto.Equal(decoded.(*graphDeleteEffectEnvelope).Mutation.GetTombstoneExpiration(), graph.GetTombstoneExpiration()) {
		t.Fatalf("Delete deadline WAL round-trip = %v, %v", decoded, err)
	}
	envelope.Mutation.TombstoneExpiration = nil
	if _, err := encodeReceiptWALUnion(envelope); err != nil {
		t.Fatalf("exact graph-only Delete encoded: %v", err)
	}
	envelope.AcceptedIndexes = nil
	if _, err := encodeReceiptWALUnion(envelope); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("incomplete graph-only Delete decision encoded: %v", err)
	}
	envelope.AcceptedIndexes = []uint32{0}
	envelope.Mutation.TombstoneExpiration = &timestamppb.Timestamp{Seconds: 253402300800}
	if _, err := encodeReceiptWALUnion(envelope); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("invalid Delete deadline encoded: %v", err)
	}
}

func TestReceiptWALUnionCodecRejectsMalformedHeaderAndGraphBody(t *testing.T) {
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{nil, {Key: "v"}, nil}}}})
	valid, err := encodeReceiptWALUnion(graph)
	if err != nil {
		t.Fatal(err)
	}
	const base = receiptWALUnionHeaderSize
	for _, tc := range []struct {
		name string
		bad  func([]byte) []byte
	}{
		{"short header", func(b []byte) []byte { return b[:receiptWALUnionHeaderSize-1] }},
		{"unknown version", func(b []byte) []byte { b[4]++; return b }},
		{"reserved", func(b []byte) []byte { b[9] = 1; return b }},
		{"unknown kind", func(b []byte) []byte { b[8] = 255; return b }},
		{"length drift", func(b []byte) []byte { b[15]++; return b }},
		{"trailing bytes", func(b []byte) []byte { return append(b, 0) }},
		{"short body", func(b []byte) []byte {
			b = b[:len(b)-1]
			binary.BigEndian.PutUint32(b[12:16], uint32(len(b)-base))
			return b
		}},
		{"proto length drift", func(b []byte) []byte { b[base+3]++; return b }},
		{"repeated count drift", func(b []byte) []byte { b[base+7]++; return b }},
		{"nil count drift", func(b []byte) []byte { b[base+11]++; return b }},
		{"unordered nil indexes", func(b []byte) []byte { binary.BigEndian.PutUint32(b[base+16:base+20], 0); return b }},
		{"out of range nil index", func(b []byte) []byte { binary.BigEndian.PutUint32(b[base+16:base+20], 3); return b }},
		{"nil index points at nonempty vertex", func(b []byte) []byte { binary.BigEndian.PutUint32(b[base+16:base+20], 1); return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := tc.bad(append([]byte(nil), valid...))
			if _, err := decodeReceiptWALUnion(bad); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("decode malformed payload = %v", err)
			}
		})
	}
	// A receipt body cannot masquerade as a graph payload, or vice versa.
	_, envelope := receiptEdgeDeleteCodecFixture(t, nil)
	receiptRaw, err := encodeReceiptWALUnion(envelope)
	if err != nil {
		t.Fatal(err)
	}
	receiptRaw[8] = receiptWALUnionGraph
	if _, err := decodeReceiptWALUnion(receiptRaw); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("receipt body as graph = %v", err)
	}
	valid[8] = receiptWALUnionEdgeDelete
	if _, err := decodeReceiptWALUnion(valid); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("graph body as receipt = %v", err)
	}
}

func TestReceiptWALUnionCodecRejectsUnknownAndAcceptsAlternativeGraphWire(t *testing.T) {
	for _, op := range []mutationlog.MutationOp{nil, 42, (*pb.Mutation)(nil), (*edgeDeleteReceiptEnvelope)(nil)} {
		if _, err := encodeReceiptWALUnion(op); !errors.Is(err, errReceiptWALUnion) {
			t.Fatalf("unsupported operation %T encoded: %v", op, err)
		}
	}
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "v"}}}}})
	graph.Op.GetPutVertices().Vertices[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, err := encodeReceiptWALUnion(graph); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("nested unknown field encoded: %v", err)
	}
	graph.Op.GetPutVertices().Vertices[0].ProtoReflect().SetUnknown(nil)
	raw, err := encodeReceiptWALUnion(graph)
	if err != nil {
		t.Fatal(err)
	}
	// Append a valid unknown field to the protobuf, then adjust both lengths.
	withUnknown := append(append([]byte(nil), raw...), 0xa0, 0x06, 0x01)
	binary.BigEndian.PutUint32(withUnknown[12:16], uint32(len(withUnknown)-receiptWALUnionHeaderSize))
	binary.BigEndian.PutUint32(withUnknown[receiptWALUnionHeaderSize:receiptWALUnionHeaderSize+4],
		binary.BigEndian.Uint32(raw[receiptWALUnionHeaderSize:receiptWALUnionHeaderSize+4])+3)
	if _, err := decodeReceiptWALUnion(withUnknown); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("unknown field decoded: %v", err)
	}
	// A duplicate scalar field is valid protobuf. Replay follows protobuf's
	// last-value-wins semantics rather than pinning one library's marshaling.
	duplicate := append(append([]byte(nil), raw...), 0x08, 0x08)
	binary.BigEndian.PutUint32(duplicate[12:16], uint32(len(duplicate)-receiptWALUnionHeaderSize))
	binary.BigEndian.PutUint32(duplicate[receiptWALUnionHeaderSize:receiptWALUnionHeaderSize+4],
		binary.BigEndian.Uint32(raw[receiptWALUnionHeaderSize:receiptWALUnionHeaderSize+4])+2)
	decoded, err := decodeReceiptWALUnion(duplicate)
	if err != nil || decoded.(*pb.Mutation).GetSeq() != 8 {
		t.Fatalf("duplicate scalar replay = %v, %v", decoded, err)
	}
}

func TestReceiptWALUnionCodecRejectsHiddenReceiptArmAndDuplicateOneofs(t *testing.T) {
	receiptWire := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
		ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{},
	}})
	if _, err := encodeReceiptWALUnion(receiptWire); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("receipt wire arm encoded as graph-only kind: %v", err)
	}
	receiptProto, err := proto.Marshal(receiptWire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReceiptWALUnion(receiptWALUnionRawGraphProto(receiptProto, 0)); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("receipt wire arm decoded as graph-only kind: %v", err)
	}
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v"}}}})
	graphArm, err := proto.Marshal(graph.Op)
	if err != nil {
		t.Fatal(err)
	}
	withoutOp := proto.Clone(graph).(*pb.Mutation)
	withoutOp.Op = nil
	prefix, err := proto.Marshal(withoutOp)
	if err != nil {
		t.Fatal(err)
	}
	appendOuterOp := func(raw, inner []byte) []byte {
		raw = protowire.AppendTag(raw, 4, protowire.BytesType)
		return protowire.AppendBytes(raw, inner)
	}
	receiptArm := protowire.AppendBytes(protowire.AppendTag(nil, 15, protowire.BytesType), nil)
	// When the receipt arm comes first, protobuf Unmarshal would keep the
	// later graph arm and discard receipt metadata. Raw WAL admission must
	// reject this regardless of the decoded last-wins value.
	inner := append(append([]byte(nil), receiptArm...), graphArm...)
	unsafe := receiptWALUnionRawGraphProto(appendOuterOp(append([]byte(nil), prefix...), inner), 0)
	if _, err := decodeReceiptWALUnion(unsafe); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("receipt-then-graph oneof downgrade = %v", err)
	}
	// The guard also rejects two otherwise supported graph arms.
	otherGraphArm := protowire.AppendBytes(protowire.AppendTag(nil, 13, protowire.BytesType), nil)
	inner = append(append([]byte(nil), otherGraphArm...), graphArm...)
	if _, err := decodeReceiptWALUnion(receiptWALUnionRawGraphProto(appendOuterOp(append([]byte(nil), prefix...), inner), 0)); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("two graph oneof arms = %v", err)
	}
	// Two outer Mutation.op fields can similarly hide an earlier arm.
	duplicateOuter := appendOuterOp(appendOuterOp(append([]byte(nil), prefix...), graphArm), graphArm)
	if _, err := decodeReceiptWALUnion(receiptWALUnionRawGraphProto(duplicateOuter, 0)); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("duplicate outer Mutation.op = %v", err)
	}
	// The same oneof rule applies to nested replay outcomes and Vertex values.
	for _, tc := range []struct {
		name       string
		descriptor protoreflect.MessageDescriptor
		raw        []byte
	}{
		{"vertex value", (&pb.Vertex{}).ProtoReflect().Descriptor(), protowire.AppendBytes(protowire.AppendTag(
			protowire.AppendBytes(protowire.AppendTag(nil, 19, protowire.BytesType), nil), 20, protowire.BytesType), nil)},
		{"replicated outcome", (&pb.ReplicatedPutVertex{}).ProtoReflect().Descriptor(), protowire.AppendBytes(protowire.AppendTag(
			protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), nil), 2, protowire.BytesType), nil)},
		{"replicated outcome reverse", (&pb.ReplicatedPutVertex{}).ProtoReflect().Descriptor(), protowire.AppendBytes(protowire.AppendTag(
			protowire.AppendBytes(protowire.AppendTag(nil, 2, protowire.BytesType), nil), 1, protowire.BytesType), nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := scanReceiptWALGraphWire(tc.raw, tc.descriptor, 0)
			if !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("duplicate nested oneof = %v", err)
			}
		})
	}
	// Field order is not a security boundary. The decoder accepts an
	// alternate valid protobuf ordering with identical graph semantics.
	hlcWire, err := proto.Marshal(graph.Hlc)
	if err != nil {
		t.Fatal(err)
	}
	reordered := appendOuterOp(nil, graphArm)
	reordered = protowire.AppendBytes(protowire.AppendTag(reordered, 3, protowire.BytesType), graph.Origin)
	reordered = protowire.AppendBytes(protowire.AppendTag(reordered, 2, protowire.BytesType), hlcWire)
	reordered = protowire.AppendVarint(protowire.AppendTag(reordered, 1, protowire.VarintType), graph.Seq)
	decoded, err := decodeReceiptWALUnion(receiptWALUnionRawGraphProto(reordered, 0))
	if err != nil || !proto.Equal(decoded.(*pb.Mutation), graph) {
		t.Fatalf("reordered protobuf fields = %v, %v", decoded, err)
	}
}

func TestReceiptWALUnionCodecRejectsNestedTypedNilOneofPayloads(t *testing.T) {
	// These oneof wrappers survive protobuf serialization but their nil
	// message payloads become non-nil empty messages on decode. The difference
	// is observable to replicated Put replay and typed Vertex values.
	for _, tc := range []struct {
		name string
		op   *pb.MutationOp
	}{
		{"vertex timestamp", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v", Value: &pb.Vertex_Timestamp{Timestamp: nil}}}}}},
		{"vertex duration in batch", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Duration{Duration: nil}}}}}}},
		{"replicated vertex live", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{{Outcome: &pb.ReplicatedPutVertex_Live{Live: nil}}}}}}},
		{"replicated vertex barrier", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{{Outcome: &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: nil}}}}}}},
		{"replicated live vertex value", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{
			ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{
				{Outcome: &pb.ReplicatedPutVertex_Live{Live: &pb.Vertex{Key: "v", Value: &pb.Vertex_Timestamp{Timestamp: nil}}}},
			}},
		}}},
		{"replicated edge live", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{Entries: []*pb.ReplicatedPutEdge{{Outcome: &pb.ReplicatedPutEdge_Live{Live: nil}}}}}}},
		{"replicated edge barrier", &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: &pb.ReplicatedPutEdges{Entries: []*pb.ReplicatedPutEdge{{Outcome: &pb.ReplicatedPutEdge_CausalBarrier{CausalBarrier: nil}}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := encodeReceiptWALUnion(receiptWALUnionGraphFixture(tc.op)); !errors.Is(err, errReceiptWALUnion) {
				t.Fatalf("typed-nil oneof payload encoded: %v", err)
			}
		})
	}
	// A real, present empty Timestamp is a valid epoch value and remains
	// distinct from a nil oneof payload.
	valid := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "v", Value: &pb.Vertex_Timestamp{Timestamp: &timestamppb.Timestamp{}}},
	}}})
	raw, err := encodeReceiptWALUnion(valid)
	if err != nil {
		t.Fatalf("present empty Timestamp rejected: %v", err)
	}
	decoded, err := decodeReceiptWALUnion(raw)
	if err != nil || decoded.(*pb.Mutation).GetOp().GetPutVertex().GetVertex().GetTimestamp() == nil {
		t.Fatalf("present empty Timestamp roundtrip = %v, %v", decoded, err)
	}
}

func TestReceiptWALUnionCodecFrameHLCMismatch(t *testing.T) {
	graph := receiptWALUnionGraphFixture(&pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "k"}}}})
	graphEntry := mutationlog.Entry{Seq: 1, HLC: receiptWALUnionGraphHLC(graph), Op: graph}
	if err := validateReceiptWALUnionEntry(graphEntry); err != nil {
		t.Fatal(err)
	}
	graphEntry.HLC.Logical++
	if err := validateReceiptWALUnionEntry(graphEntry); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("graph frame HLC mismatch = %v", err)
	}
	_, envelope := receiptEdgeDeleteCodecFixture(t, nil)
	receiptEntry := mutationlog.Entry{Seq: 2, HLC: envelope.HLC, Op: envelope}
	if err := validateReceiptWALUnionEntry(receiptEntry); err != nil {
		t.Fatal(err)
	}
	receiptEntry.HLC.Logical++
	if err := validateReceiptWALUnionEntry(receiptEntry); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("receipt frame HLC mismatch = %v", err)
	}
	// FileWAL itself accepts the frame bytes; the replay visitor must fail
	// closed before restoring graph, receipt or origin state.
	path := filepath.Join(t.TempDir(), "bad-hlc.wal")
	wal, err := mutationlog.CreateFileWAL(path, encodeReceiptWALUnion)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Write(graphEntry); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	visits := 0
	err = mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		visits++
		return validateReceiptWALUnionEntry(entry)
	})
	if !errors.Is(err, errReceiptWALUnion) || visits != 1 {
		t.Fatalf("mismatched frame replay = %v, visits=%d", err, visits)
	}
}
