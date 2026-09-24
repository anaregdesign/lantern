package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func archiveWireHeaderFrame(header []byte) []byte {
	frame := protowire.AppendTag(nil, 1, protowire.BytesType)
	return protowire.AppendBytes(frame, header)
}

func TestArchiveGraphWireSchemaPinRejectsFutureField(t *testing.T) {
	current := (&pb.SnapshotResponse{}).ProtoReflect().Descriptor()
	if got := archiveMessageSchemaFingerprint(current); got != archiveGraphSchemaFingerprintV1 {
		t.Fatalf("archive v1 graph schema changed to %s; review the archive contract", got)
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
			TypeName: proto.String(".graph.v1.SnapshotHeader"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := protodesc.ToFileDescriptorProto(current.ParentFile())
			for _, message := range file.MessageType {
				if message.GetName() == "SnapshotHeader" {
					message.Field = append(message.Field, tc.field)
					break
				}
			}
			changed, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
			if err != nil {
				t.Fatal(err)
			}
			if got := archiveMessageSchemaFingerprint(changed.Messages().ByName("SnapshotResponse")); got == archiveGraphSchemaFingerprintV1 {
				t.Fatal("new nested graph field did not invalidate archive v1 schema")
			}
		})
	}
}

func archiveWireReplaceFirstFrame(t *testing.T, baseline, frame []byte) []byte {
	t.Helper()
	frameStart := wholeStateArchiveHeaderSize + 5
	oldSize := int(binary.BigEndian.Uint32(baseline[wholeStateArchiveHeaderSize+1:]))
	footerStart := len(baseline) - 5 - wholeStateArchiveFooterSize
	out := append([]byte(nil), baseline[:frameStart]...)
	binary.BigEndian.PutUint32(out[wholeStateArchiveHeaderSize+1:], uint32(len(frame)))
	out = append(out, frame...)
	out = append(out, baseline[frameStart+oldSize:footerStart]...)
	digest := sha256.Sum256(out)
	out = append(out, baseline[footerStart:footerStart+5+24]...)
	out = append(out, digest[:]...)
	return out
}

func TestArchiveGraphWireAcceptsReorderedFieldsAcrossArchiveDecode(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	secondOrigin := a.Origins[0]
	secondOrigin.Origin[0]++
	secondOrigin.LastHLC.NodeID = secondOrigin.Origin
	secondOrigin.LastSeq = 1
	a.Origins = append(a.Origins, secondOrigin)
	a.Graph[0].GetHeader().CutoffSeqPerOrigin[hex.EncodeToString(secondOrigin.Origin[:])] = secondOrigin.LastSeq
	header, err := proto.Marshal(a.Graph[0].GetHeader())
	if err != nil {
		t.Fatal(err)
	}
	var fields [][]byte
	for len(header) != 0 {
		number, wireType, tagSize := protowire.ConsumeTag(header)
		valueSize := protowire.ConsumeFieldValue(number, wireType, header[tagSize:])
		fields = append(fields, append([]byte(nil), header[:tagSize+valueSize]...))
		header = header[tagSize+valueSize:]
	}
	var reordered []byte
	for i := len(fields) - 1; i >= 0; i-- {
		reordered = append(reordered, fields[i]...)
	}
	frame := archiveWireHeaderFrame(reordered)
	if err := validateArchiveGraphFrameWire(frame); err != nil {
		t.Fatalf("valid reordered graph frame rejected: %v", err)
	}
	archive := archiveWireReplaceFirstFrame(t, encodedWholeStateArchive(t, a), frame)
	decoded, err := decodeWholeStateArchive(bytes.NewReader(archive))
	if err != nil || !proto.Equal(decoded.Graph[0], a.Graph[0]) {
		t.Fatalf("reordered archive graph = (%v, %v), want equivalent header", decoded.Graph, err)
	}
}

func TestArchiveGraphWireRejectsAmbiguousOrMalformedFields(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	header, err := proto.Marshal(a.Graph[0].GetHeader())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := proto.Marshal(a.Graph[0])
	if err != nil {
		t.Fatal(err)
	}
	duplicateFormat := protowire.AppendTag(append([]byte(nil), header...), 4, protowire.VarintType)
	duplicateFormat = protowire.AppendVarint(duplicateFormat, 2)
	unknown := protowire.AppendTag(append([]byte(nil), header...), 99, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	duplicateOneof := protowire.AppendTag(append([]byte(nil), frame...), 4, protowire.BytesType)
	duplicateOneof = protowire.AppendBytes(duplicateOneof, nil)
	var mapEntry []byte
	for key, seq := range a.Graph[0].GetHeader().GetCutoffSeqPerOrigin() {
		mapEntry = protowire.AppendTag(mapEntry, 1, protowire.BytesType)
		mapEntry = protowire.AppendString(mapEntry, key)
		mapEntry = protowire.AppendTag(mapEntry, 2, protowire.VarintType)
		mapEntry = protowire.AppendVarint(mapEntry, seq)
		break
	}
	duplicateMap := protowire.AppendTag(append([]byte(nil), header...), 1, protowire.BytesType)
	duplicateMap = protowire.AppendBytes(duplicateMap, mapEntry)
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"duplicate singular", archiveWireHeaderFrame(duplicateFormat)},
		{"duplicate map key", archiveWireHeaderFrame(duplicateMap)},
		{"duplicate oneof", duplicateOneof},
		{"unknown nested field", archiveWireHeaderFrame(unknown)},
		{"wrong wire type", archiveWireHeaderFrame([]byte{0x22, 0x01, 0x02})},
		{"nonminimal varint", archiveWireHeaderFrame([]byte{0x20, 0x82, 0x00})},
		{"nonminimal tag", []byte{0x8a, 0x00, 0x00}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateArchiveGraphFrameWire(tc.frame); !errors.Is(err, errWholeStateArchive) {
				t.Fatalf("malformed frame accepted: %v", err)
			}
			archive := archiveWireReplaceFirstFrame(t, encodedWholeStateArchive(t, a), tc.frame)
			if decoded, err := decodeWholeStateArchive(bytes.NewReader(archive)); !errors.Is(err, errWholeStateArchive) || len(decoded.Graph) != 0 {
				t.Fatalf("malformed archive returned (%+v, %v)", decoded, err)
			}
		})
	}
}

func TestArchiveGraphWireRejectsMalformedNestedScalars(t *testing.T) {
	uint32Overflow := protowire.AppendTag(nil, 2, protowire.VarintType)
	uint32Overflow = protowire.AppendVarint(uint32Overflow, 1<<32)
	shortNegativeInt32 := protowire.AppendTag(nil, 12, protowire.VarintType)
	shortNegativeInt32 = protowire.AppendVarint(shortNegativeInt32, (1<<32)-1)
	for _, tc := range []struct {
		name       string
		raw        []byte
		descriptor protoreflect.MessageDescriptor
	}{
		{"short fixed32", []byte{0x0d, 0x01, 0x02}, (&pb.SnapshotEdgeContribution{}).ProtoReflect().Descriptor()},
		{"invalid UTF-8", []byte{0x0a, 0x01, 0xff}, (&pb.SnapshotVertexCausalBarrier{}).ProtoReflect().Descriptor()},
		{"noncanonical bool", []byte{0xf0, 0x01, 0x02}, (&pb.Vertex{}).ProtoReflect().Descriptor()},
		{"uint32 overflow", uint32Overflow, (&pb.HLCTimestamp{}).ProtoReflect().Descriptor()},
		{"short negative int32", shortNegativeInt32, (&pb.Vertex{}).ProtoReflect().Descriptor()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateArchiveMessageWire(tc.raw, tc.descriptor, 0); !errors.Is(err, errWholeStateArchive) {
				t.Fatalf("malformed nested scalar accepted: %v", err)
			}
		})
	}
}
