package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/protoschema"
)

// This private, unwired codec covers graph-only Mutations, accepted-effect
// sidecars, and receipt-bearing Edge Delete envelopes. The
// FileWAL frame owns its checksum, replica-local sequence and HLC. The union
// header is versioned independently of the inner LRED receipt format. No
// production provider or write path selects this codec yet.
const (
	receiptWALUnionMagic             = "LRWU\x03\x00\x00\x00"
	receiptWALUnionHeaderSize        = 16 // magic, kind, reserved[3], body length
	receiptWALUnionGraph             = byte(1)
	receiptWALUnionEdgeDelete        = byte(2)
	receiptWALUnionGraphDeleteEffect = byte(3)
	receiptWALUnionGraphPutEffect    = byte(4)
	receiptWALUnionGraphAddEffect    = byte(5)
	receiptWALUnionBaseline          = byte(6)
	receiptWALGraphHeaderSize        = 12 // protobuf length, repeated-slot count, nil count
	// FileWAL allows a 32 MiB body with a 36-byte frame metadata header.
	// The existing LRED receipt body has a stricter independent 8 MiB cap.
	receiptWALUnionMaxBytes = (32 << 20) - 36
	// A new reachable Mutation field must not silently change what the v3
	// graph kind persists or replays. Review and version the WAL schema first.
	receiptWALGraphSchemaFingerprintV3 = "7940660efd6cbc80ebf0e6804cd22e285e292d498c66c178fd175d68f4213d3d"
)

var errReceiptWALUnion = errors.New("service: invalid receipt WAL union payload")

var receiptWALGraphSchemaError = sync.OnceValue(func() error {
	digest := protoschema.Fingerprint((&pb.Mutation{}).ProtoReflect().Descriptor())
	if digest != receiptWALGraphSchemaFingerprintV3 {
		return receiptWALUnionError("WAL union v3 graph schema changed: %s", digest)
	}
	return nil
})

func receiptWALUnionError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errReceiptWALUnion, fmt.Sprintf(format, args...))
}

// encodeReceiptWALUnion matches FileWAL's payload encoder signature. It
// rejects unknown payloads rather than silently projecting a private
// envelope to graph-only.
func encodeReceiptWALUnion(op mutationlog.MutationOp) ([]byte, error) {
	var kind byte
	var body []byte
	var err error
	switch value := op.(type) {
	case *pb.Mutation:
		if isAnyGraphDelete(value) {
			return nil, receiptWALUnionError("graph Delete requires accepted-effect envelope")
		}
		kind = receiptWALUnionGraph
		body, err = encodeReceiptWALGraph(value)
	case *edgeDeleteReceiptEnvelope:
		kind = receiptWALUnionEdgeDelete
		body, err = encodeReceiptEdgeDeleteWAL(value)
	case *graphDeleteEffectEnvelope:
		kind = receiptWALUnionGraphDeleteEffect
		body, err = encodeGraphDeleteEffectWAL(value)
	case *graphPutEffectEnvelope:
		kind = receiptWALUnionGraphPutEffect
		body, err = encodeGraphPutEffectWAL(value)
	case *graphAddEffectEnvelope:
		kind = receiptWALUnionGraphAddEffect
		body, err = encodeGraphAddEffectWAL(value)
	case receiptBaselineMarker:
		kind = receiptWALUnionBaseline
		body, err = marshalReceiptBaselineMarker(value)
	case *receiptBaselineMarker:
		if value == nil {
			return nil, receiptWALUnionError("nil baseline marker")
		}
		kind = receiptWALUnionBaseline
		body, err = marshalReceiptBaselineMarker(*value)
	default:
		return nil, receiptWALUnionError("unexpected operation type %T", op)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errReceiptWALUnion, err)
	}
	if len(body) > receiptWALUnionMaxBytes-receiptWALUnionHeaderSize {
		return nil, receiptWALUnionError("body exceeds FileWAL frame limit")
	}
	raw := make([]byte, receiptWALUnionHeaderSize+len(body))
	copy(raw[:8], receiptWALUnionMagic)
	raw[8] = kind
	binary.BigEndian.PutUint32(raw[12:16], uint32(len(body)))
	copy(raw[receiptWALUnionHeaderSize:], body)
	return raw, nil
}

// decodeReceiptWALUnion requires an exact version, kind, reserved header,
// length, and valid inner representation. FileWAL checks CRC before this
// decoder runs; validateReceiptWALUnionEntry additionally checks frame HLC.
func decodeReceiptWALUnion(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) < receiptWALUnionHeaderSize || len(raw) > receiptWALUnionMaxBytes {
		return nil, receiptWALUnionError("invalid payload size %d", len(raw))
	}
	if string(raw[:8]) != receiptWALUnionMagic || raw[9] != 0 || raw[10] != 0 || raw[11] != 0 {
		return nil, receiptWALUnionError("unknown version or nonzero reserved header")
	}
	if uint64(binary.BigEndian.Uint32(raw[12:16])) != uint64(len(raw)-receiptWALUnionHeaderSize) {
		return nil, receiptWALUnionError("body length mismatch")
	}
	body := raw[receiptWALUnionHeaderSize:]
	switch raw[8] {
	case receiptWALUnionGraph:
		value, err := decodeReceiptWALGraph(body)
		if err != nil {
			return nil, err
		}
		if isAnyGraphDelete(value) {
			return nil, receiptWALUnionError("graph Delete lacks accepted-effect envelope")
		}
		return value, nil
	case receiptWALUnionEdgeDelete:
		value, err := decodeReceiptEdgeDeleteWAL(body)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errReceiptWALUnion, err)
		}
		return value, nil
	case receiptWALUnionGraphDeleteEffect:
		return decodeGraphDeleteEffectWAL(body)
	case receiptWALUnionGraphPutEffect:
		return decodeGraphPutEffectWAL(body)
	case receiptWALUnionGraphAddEffect:
		return decodeGraphAddEffectWAL(body)
	case receiptWALUnionBaseline:
		marker, err := unmarshalReceiptBaselineMarker(body)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errReceiptWALUnion, err)
		}
		return marker, nil
	default:
		return nil, receiptWALUnionError("unknown operation kind %d", raw[8])
	}
}

// The decoder API receives only payload bytes. A replay/restore visitor must
// call this before applying an Entry; otherwise FileWAL's frame HLC has not
// been compared with the payload HLC. The frame seq is replica-local and is
// deliberately independent of the payload's origin-local seq.
func validateReceiptWALUnionEntry(entry mutationlog.Entry) error {
	if entry.Seq == 0 {
		return receiptWALUnionError("zero FileWAL local seq")
	}
	switch value := entry.Op.(type) {
	case *pb.Mutation:
		if isAnyGraphDelete(value) {
			return receiptWALUnionError("graph Delete lacks accepted-effect envelope")
		}
		if err := validateReceiptWALGraph(value); err != nil {
			return err
		}
		var node hlc.NodeID
		copy(node[:], value.GetHlc().GetNodeId())
		payloadHLC := hlc.Timestamp{WallNs: value.GetHlc().GetWallNs(), Logical: value.GetHlc().GetLogical(), NodeID: node}
		if !entry.HLC.Equal(payloadHLC) {
			return receiptWALUnionError("FileWAL HLC differs from graph mutation HLC")
		}
		return nil
	case *graphDeleteEffectEnvelope:
		if err := validateGraphDeleteEffectEnvelope(value); err != nil {
			return err
		}
		m := value.Mutation
		var node hlc.NodeID
		copy(node[:], m.GetHlc().GetNodeId())
		payloadHLC := hlc.Timestamp{WallNs: m.GetHlc().GetWallNs(), Logical: m.GetHlc().GetLogical(), NodeID: node}
		if !entry.HLC.Equal(payloadHLC) {
			return receiptWALUnionError("FileWAL HLC differs from graph Delete effect HLC")
		}
		return nil
	case *graphPutEffectEnvelope:
		if err := validateGraphPutEffectEnvelope(value); err != nil {
			return err
		}
		m := value.Mutation
		var node hlc.NodeID
		copy(node[:], m.GetHlc().GetNodeId())
		payloadHLC := hlc.Timestamp{WallNs: m.GetHlc().GetWallNs(), Logical: m.GetHlc().GetLogical(), NodeID: node}
		if !entry.HLC.Equal(payloadHLC) {
			return receiptWALUnionError("FileWAL HLC differs from graph Put effect HLC")
		}
		return nil
	case *graphAddEffectEnvelope:
		if err := validateGraphAddEffectEnvelope(value); err != nil {
			return err
		}
		m := value.Mutation
		var node hlc.NodeID
		copy(node[:], m.GetHlc().GetNodeId())
		payloadHLC := hlc.Timestamp{WallNs: m.GetHlc().GetWallNs(), Logical: m.GetHlc().GetLogical(), NodeID: node}
		if !entry.HLC.Equal(payloadHLC) {
			return receiptWALUnionError("FileWAL HLC differs from graph Add effect HLC")
		}
		return nil
	case *edgeDeleteReceiptEnvelope:
		if err := validateReceiptEdgeDeleteWALEntry(entry); err != nil {
			return fmt.Errorf("%w: %w", errReceiptWALUnion, err)
		}
		return nil
	case receiptBaselineMarker:
		if err := validateReceiptBaselineEntry(entry.Seq, entry.HLC, value); err != nil {
			return fmt.Errorf("%w: %w", errReceiptWALUnion, err)
		}
		return nil
	default:
		return receiptWALUnionError("unexpected operation type %T", entry.Op)
	}
}

func encodeReceiptWALGraph(m *pb.Mutation) ([]byte, error) {
	if err := validateReceiptWALGraph(m); err != nil {
		return nil, err
	}
	arm, err := receiptWALGraphArm(m)
	if err != nil {
		return nil, err
	}
	var nilIndexes []uint32
	for i := 0; i < arm.count; i++ {
		if nilProtoMessage(arm.at(i)) {
			nilIndexes = append(nilIndexes, uint32(i))
		}
	}
	maxBody := receiptWALUnionMaxBytes - receiptWALUnionHeaderSize
	if len(nilIndexes) > (maxBody-receiptWALGraphHeaderSize)/4 || proto.Size(m) > maxBody-receiptWALGraphHeaderSize-len(nilIndexes)*4 {
		return nil, receiptWALUnionError("graph mutation exceeds FileWAL frame limit")
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal graph mutation: %w", errReceiptWALUnion, err)
	}
	if len(encoded) > maxBody-receiptWALGraphHeaderSize-len(nilIndexes)*4 {
		return nil, receiptWALUnionError("encoded graph mutation exceeds FileWAL frame limit")
	}
	body := make([]byte, receiptWALGraphHeaderSize+len(nilIndexes)*4+len(encoded))
	binary.BigEndian.PutUint32(body[:4], uint32(len(encoded)))
	binary.BigEndian.PutUint32(body[4:8], uint32(arm.count))
	binary.BigEndian.PutUint32(body[8:12], uint32(len(nilIndexes)))
	for i, index := range nilIndexes {
		binary.BigEndian.PutUint32(body[receiptWALGraphHeaderSize+i*4:], index)
	}
	copy(body[receiptWALGraphHeaderSize+len(nilIndexes)*4:], encoded)
	return body, nil
}

func decodeReceiptWALGraph(body []byte) (*pb.Mutation, error) {
	if len(body) < receiptWALGraphHeaderSize {
		return nil, receiptWALUnionError("graph body is truncated")
	}
	protoLen := binary.BigEndian.Uint32(body[:4])
	repeatedCount := binary.BigEndian.Uint32(body[4:8])
	nilCount := binary.BigEndian.Uint32(body[8:12])
	if nilCount > uint32((len(body)-receiptWALGraphHeaderSize)/4) {
		return nil, receiptWALUnionError("nil-slot index table is truncated")
	}
	indexBytes := int(nilCount) * 4
	if uint64(protoLen) != uint64(len(body)-receiptWALGraphHeaderSize-indexBytes) {
		return nil, receiptWALUnionError("graph protobuf length mismatch")
	}
	indexes := body[receiptWALGraphHeaderSize : receiptWALGraphHeaderSize+indexBytes]
	encoded := body[receiptWALGraphHeaderSize+indexBytes:]
	if err := scanReceiptWALGraphWire(encoded, (&pb.Mutation{}).ProtoReflect().Descriptor(), 0); err != nil {
		return nil, err
	}
	var m pb.Mutation
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, &m); err != nil {
		return nil, fmt.Errorf("%w: unmarshal graph mutation: %w", errReceiptWALUnion, err)
	}
	if err := validateReceiptWALGraph(&m); err != nil {
		return nil, err
	}
	arm, err := receiptWALGraphArm(&m)
	if err != nil {
		return nil, err
	}
	if uint64(repeatedCount) != uint64(arm.count) || nilCount > repeatedCount {
		return nil, receiptWALUnionError("repeated-slot count differs from graph arm")
	}
	// Deterministic marshaling is useful for this binary's writes, but the
	// protobuf library does not promise identical bytes across versions.
	// Replay accepts all valid known-field encodings (including duplicate
	// scalar fields with protobuf's last-value-wins semantics).
	var previous uint32
	for i := uint32(0); i < nilCount; i++ {
		index := binary.BigEndian.Uint32(indexes[i*4:])
		if index >= repeatedCount || (i > 0 && index <= previous) {
			return nil, receiptWALUnionError("nil-slot index is out of range or unordered")
		}
		if item := arm.at(int(index)); nilProtoMessage(item) || proto.Size(item) != 0 {
			return nil, receiptWALUnionError("nil-slot index points to nonempty message")
		}
		arm.clear(int(index))
		previous = index
	}
	return &m, nil
}

// Protobuf Unmarshal takes the last oneof arm and merges repeated singular
// messages. Without inspecting raw field occurrences, an inner receipt arm
// followed by a graph arm could silently downgrade a receipt to graph-only.
// Scalar duplicates retain protobuf's last-value-wins semantics; oneofs and
// the outer Mutation.op field have the stricter WAL admission contract.
func scanReceiptWALGraphWire(raw []byte, descriptor protoreflect.MessageDescriptor, depth int) error {
	if depth > 32 {
		return receiptWALUnionError("protobuf message nesting exceeds limit")
	}
	var oneofs map[protoreflect.FullName]struct{}
	var opCount int
	var armCount int
	for len(raw) != 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(raw)
		if tagBytes < 0 {
			return receiptWALUnionError("malformed protobuf tag")
		}
		raw = raw[tagBytes:]
		field := descriptor.Fields().ByNumber(number)
		if field == nil {
			return receiptWALUnionError("unknown protobuf field %d in %s", number, descriptor.FullName())
		}
		if descriptor.FullName() == "graph.v1.Mutation" && number == 4 {
			opCount++
			if opCount != 1 {
				return receiptWALUnionError("duplicate outer Mutation.op field")
			}
		}
		if descriptor.FullName() == "graph.v1.MutationOp" {
			if number < 1 || number > 14 {
				return receiptWALUnionError("graph kind cannot contain receipt or unknown operation arm %d", number)
			}
			armCount++
		}
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs == nil {
				oneofs = make(map[protoreflect.FullName]struct{})
			}
			if _, seen := oneofs[oneof.FullName()]; seen {
				return receiptWALUnionError("duplicate protobuf oneof arm in %s", descriptor.FullName())
			}
			oneofs[oneof.FullName()] = struct{}{}
		}
		if field.Kind() == protoreflect.MessageKind {
			if wireType != protowire.BytesType {
				return receiptWALUnionError("message field %s has wrong wire type", field.FullName())
			}
			value, valueBytes := protowire.ConsumeBytes(raw)
			if valueBytes < 0 {
				return receiptWALUnionError("malformed protobuf message field")
			}
			if err := scanReceiptWALGraphWire(value, field.Message(), depth+1); err != nil {
				return err
			}
			raw = raw[valueBytes:]
			continue
		}
		if field.Kind() == protoreflect.GroupKind {
			return receiptWALUnionError("protobuf group fields are unsupported")
		}
		valueBytes := protowire.ConsumeFieldValue(number, wireType, raw)
		if valueBytes < 0 {
			return receiptWALUnionError("malformed protobuf field %s", field.FullName())
		}
		raw = raw[valueBytes:]
	}
	if descriptor.FullName() == "graph.v1.Mutation" && opCount != 1 {
		return receiptWALUnionError("Mutation.op must occur exactly once")
	}
	if descriptor.FullName() == "graph.v1.MutationOp" && armCount != 1 {
		return receiptWALUnionError("MutationOp must contain exactly one graph arm")
	}
	return nil
}

func validateReceiptWALGraph(m *pb.Mutation) error {
	if err := receiptWALGraphSchemaError(); err != nil {
		return err
	}
	if m == nil || m.GetSeq() == 0 || m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 ||
		len(m.GetOrigin()) != len(hlc.NodeID{}) || len(m.GetHlc().GetNodeId()) != len(hlc.NodeID{}) ||
		zeroNodeID(m.GetOrigin()) || !bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) {
		return receiptWALUnionError("invalid graph origin, sequence, or HLC")
	}
	if _, err := receiptWALGraphArm(m); err != nil {
		return err
	}
	if err := validateSyntheticAddMutationBounds(m); err != nil {
		return receiptWALUnionError("graph Add identity: %v", err)
	}
	if _, err := mutationTombstoneExpiration(m, m.GetTombstoneExpiration() != nil); err != nil {
		return receiptWALUnionError("graph Delete deadline: %v", err)
	}
	return rejectReceiptWALUnknownFields(m.ProtoReflect())
}

type receiptWALRepeatedArm struct {
	count int
	at    func(int) proto.Message
	clear func(int)
}

// Every current MutationOp arm is explicit here. Ordinary Put/Add batches can
// contain nil slots, which protobuf normalizes to empty messages on unmarshal.
// The sidecar restores those exact slots for graph replay. Replicated Put
// batches are stricter: serving apply rejects missing entries and outcomes.
func receiptWALGraphArm(m *pb.Mutation) (receiptWALRepeatedArm, error) {
	if m == nil || m.Op == nil {
		return receiptWALRepeatedArm{}, receiptWALUnionError("missing graph mutation operation")
	}
	switch value := m.Op.GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		if value != nil && value.PutVertex != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_PutVertices:
		if value == nil || value.PutVertices == nil {
			break
		}
		items := value.PutVertices.Vertices
		return receiptWALRepeatedArm{len(items), func(i int) proto.Message { return items[i] }, func(i int) { items[i] = nil }}, nil
	case *pb.MutationOp_DeleteVertex:
		if value != nil && value.DeleteVertex != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_DeleteVertices:
		if value != nil && value.DeleteVertices != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_DeleteVerticesByPrefix:
		if value != nil && value.DeleteVerticesByPrefix != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_AddEdge:
		if value != nil && value.AddEdge != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_AddEdges:
		if value == nil || value.AddEdges == nil {
			break
		}
		items := value.AddEdges.Edges
		return receiptWALRepeatedArm{len(items), func(i int) proto.Message { return items[i] }, func(i int) { items[i] = nil }}, nil
	case *pb.MutationOp_PutEdge:
		if value != nil && value.PutEdge != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_PutEdges:
		if value == nil || value.PutEdges == nil {
			break
		}
		items := value.PutEdges.Edges
		return receiptWALRepeatedArm{len(items), func(i int) proto.Message { return items[i] }, func(i int) { items[i] = nil }}, nil
	case *pb.MutationOp_DeleteEdge:
		if value != nil && value.DeleteEdge != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_DeleteEdges:
		if value == nil || value.DeleteEdges == nil {
			break
		}
		items := value.DeleteEdges.Edges
		return receiptWALRepeatedArm{len(items), func(i int) proto.Message { return items[i] }, func(i int) { items[i] = nil }}, nil
	case *pb.MutationOp_DeleteEdgesByPrefix:
		if value != nil && value.DeleteEdgesByPrefix != nil {
			return receiptWALRepeatedArm{}, nil
		}
	case *pb.MutationOp_ReplicatedPutVertices:
		if value == nil || value.ReplicatedPutVertices == nil {
			break
		}
		items := value.ReplicatedPutVertices.Entries
		for i, item := range items {
			if item == nil || item.GetOutcome() == nil {
				return receiptWALRepeatedArm{}, receiptWALUnionError("replicated Vertex Put entry %d has no outcome", i)
			}
		}
		return receiptWALRepeatedArm{len(items), func(i int) proto.Message { return items[i] }, func(i int) { items[i] = nil }}, nil
	case *pb.MutationOp_ReplicatedPutEdges:
		if value == nil || value.ReplicatedPutEdges == nil {
			break
		}
		items := value.ReplicatedPutEdges.Entries
		for i, item := range items {
			if item == nil || item.GetOutcome() == nil {
				return receiptWALRepeatedArm{}, receiptWALUnionError("replicated Edge Put entry %d has no outcome", i)
			}
		}
		return receiptWALRepeatedArm{len(items), func(i int) proto.Message { return items[i] }, func(i int) { items[i] = nil }}, nil
	default:
		return receiptWALRepeatedArm{}, receiptWALUnionError("unknown graph mutation operation %T", m.Op.GetOp())
	}
	return receiptWALRepeatedArm{}, receiptWALUnionError("nil graph mutation operation %T", m.Op.GetOp())
}

func nilProtoMessage(m proto.Message) bool {
	if m == nil {
		return true
	}
	v := reflect.ValueOf(m)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func rejectReceiptWALUnknownFields(m protoreflect.Message) error {
	if err := rejectProtoUnknownFields(m); err != nil {
		return receiptWALUnionError("graph mutation %v", err)
	}
	return nil
}

func rejectProtoUnknownFields(m protoreflect.Message) error {
	if !m.IsValid() {
		return nil
	}
	if len(m.GetUnknown()) != 0 {
		return errors.New("contains unknown protobuf fields")
	}
	// Protobuf encodes a message-valued oneof wrapper with a nil payload in
	// exactly the same bytes as a present empty message. Unmarshal changes
	// nil to non-nil, bypassing replay's nil guards or changing a Vertex value.
	// Repeated-message nil slots are handled separately by the sidecar.
	for i := 0; i < m.Descriptor().Oneofs().Len(); i++ {
		field := m.WhichOneof(m.Descriptor().Oneofs().Get(i))
		if field != nil && (field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind) &&
			!m.Get(field).Message().IsValid() {
			return errors.New("contains nil message-valued oneof payload")
		}
	}
	var nestedErr error
	m.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
			return true
		}
		if field.IsList() {
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				if nestedErr = rejectProtoUnknownFields(list.Get(i).Message()); nestedErr != nil {
					return false
				}
			}
			return true
		}
		if field.IsMap() {
			if field.MapValue().Kind() != protoreflect.MessageKind && field.MapValue().Kind() != protoreflect.GroupKind {
				return true
			}
			value.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
				nestedErr = rejectProtoUnknownFields(item.Message())
				return nestedErr == nil
			})
			return nestedErr == nil
		}
		nestedErr = rejectProtoUnknownFields(value.Message())
		return nestedErr == nil
	})
	return nestedErr
}
