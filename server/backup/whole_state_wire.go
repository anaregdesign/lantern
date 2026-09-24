package backup

import (
	"sync"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/protoschema"
)

// validateArchiveGraphFrameWire validates the protobuf wire shape without
// comparing bytes produced by a particular protobuf runtime. Field order and
// map-entry order are not significant. Unknown fields, ambiguous duplicate
// singular/oneof fields, duplicate map keys, nonminimal varints, wrong wire
// types, and malformed nested messages are rejected before proto.Unmarshal.
// The caller separately validates the decoded graph's domain semantics.
func validateArchiveGraphFrameWire(raw []byte) error {
	if err := archiveGraphSchemaError(); err != nil {
		return err
	}
	return validateArchiveMessageWire(raw, (&pb.SnapshotResponse{}).ProtoReflect().Descriptor(), 0)
}

const archiveWireMaxDepth = 32

// Archive v1 must not start accepting a newly generated protobuf field merely
// because the application was rebuilt with a newer proto. Review any reachable
// SnapshotResponse schema change against archive semantics before updating this
// fingerprint or assigning a new archive version.
const archiveGraphSchemaFingerprintV1 = "fed3f32f77295f18c8689ba997923564516028326422669751eae3acfe524d83"

var archiveGraphSchemaError = sync.OnceValue(func() error {
	digest := protoschema.Fingerprint((&pb.SnapshotResponse{}).ProtoReflect().Descriptor())
	if digest != archiveGraphSchemaFingerprintV1 {
		return wholeStateArchiveError("archive v1 graph schema changed: %s", digest)
	}
	return nil
})

func validateArchiveMessageWire(raw []byte, descriptor protoreflect.MessageDescriptor, depth int) error {
	if depth > archiveWireMaxDepth {
		return wholeStateArchiveError("graph frame exceeds protobuf nesting limit")
	}
	seenFields := make(map[protoreflect.FieldNumber]struct{})
	seenOneofs := make(map[protoreflect.FullName]struct{})
	mapKeys := make(map[protoreflect.FieldNumber]map[string]struct{})
	for len(raw) != 0 {
		number, wireType, tagSize := protowire.ConsumeTag(raw)
		if tagSize < 0 || tagSize != protowire.SizeTag(number) {
			return wholeStateArchiveError("invalid or nonminimal graph field tag")
		}
		field := descriptor.Fields().ByNumber(protoreflect.FieldNumber(number))
		if field == nil {
			return wholeStateArchiveError("unknown graph field %d in %s", number, descriptor.FullName())
		}
		wantType, ok := archiveFieldWireType(field.Kind())
		if !ok || wireType != wantType {
			return wholeStateArchiveError("wrong graph wire type for %s", field.FullName())
		}
		if !field.IsList() && !field.IsMap() {
			if _, duplicate := seenFields[field.Number()]; duplicate {
				return wholeStateArchiveError("duplicate graph field %s", field.FullName())
			}
			seenFields[field.Number()] = struct{}{}
		}
		if oneof := field.ContainingOneof(); oneof != nil {
			if _, duplicate := seenOneofs[oneof.FullName()]; duplicate {
				return wholeStateArchiveError("duplicate graph oneof %s", oneof.FullName())
			}
			seenOneofs[oneof.FullName()] = struct{}{}
		}
		value := raw[tagSize:]
		var valueSize int
		switch wireType {
		case protowire.VarintType:
			integer, size := protowire.ConsumeVarint(value)
			if size < 0 || size != protowire.SizeVarint(integer) || !validArchiveVarint(field.Kind(), integer) {
				return wholeStateArchiveError("invalid or nonminimal graph varint for %s", field.FullName())
			}
			valueSize = size
		case protowire.Fixed32Type:
			_, valueSize = protowire.ConsumeFixed32(value)
		case protowire.Fixed64Type:
			_, valueSize = protowire.ConsumeFixed64(value)
		case protowire.BytesType:
			payload, size := protowire.ConsumeBytes(value)
			if size < 0 || size-len(payload) != protowire.SizeVarint(uint64(len(payload))) {
				return wholeStateArchiveError("invalid or nonminimal graph length for %s", field.FullName())
			}
			valueSize = size
			switch field.Kind() {
			case protoreflect.StringKind:
				if !utf8.Valid(payload) {
					return wholeStateArchiveError("invalid UTF-8 in graph field %s", field.FullName())
				}
			case protoreflect.MessageKind:
				if err := validateArchiveMessageWire(payload, field.Message(), depth+1); err != nil {
					return err
				}
				if field.IsMap() {
					key, err := archiveStringMapKey(payload, field)
					if err != nil {
						return err
					}
					keys := mapKeys[field.Number()]
					if keys == nil {
						keys = make(map[string]struct{})
						mapKeys[field.Number()] = keys
					}
					if _, duplicate := keys[key]; duplicate {
						return wholeStateArchiveError("duplicate graph map key in %s", field.FullName())
					}
					keys[key] = struct{}{}
				}
			}
		default:
			return wholeStateArchiveError("unsupported graph wire type for %s", field.FullName())
		}
		if valueSize < 0 {
			return wholeStateArchiveError("truncated graph field %s", field.FullName())
		}
		raw = value[valueSize:]
	}
	return nil
}

func validArchiveVarint(kind protoreflect.Kind, value uint64) bool {
	switch kind {
	case protoreflect.BoolKind:
		return value <= 1
	case protoreflect.Uint32Kind, protoreflect.Sint32Kind:
		return value <= uint64(^uint32(0))
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return uint64(int64(int32(value))) == value
	default:
		return true
	}
}

func archiveFieldWireType(kind protoreflect.Kind) (protowire.Type, bool) {
	switch kind {
	case protoreflect.BoolKind, protoreflect.EnumKind,
		protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		return protowire.VarintType, true
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return protowire.Fixed32Type, true
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return protowire.Fixed64Type, true
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
		return protowire.BytesType, true
	default:
		return 0, false
	}
}

// Snapshot graph maps currently have string keys. If a future schema adds
// another map-key kind, reject it until the archive contract is reviewed.
func archiveStringMapKey(raw []byte, field protoreflect.FieldDescriptor) (string, error) {
	if field.MapKey().Kind() != protoreflect.StringKind {
		return "", wholeStateArchiveError("unsupported graph map key in %s", field.FullName())
	}
	for len(raw) != 0 {
		number, wireType, tagSize := protowire.ConsumeTag(raw)
		if tagSize < 0 {
			return "", wholeStateArchiveError("invalid graph map tag in %s", field.FullName())
		}
		value := raw[tagSize:]
		if number == 1 {
			if wireType != protowire.BytesType {
				return "", wholeStateArchiveError("invalid graph map key in %s", field.FullName())
			}
			key, size := protowire.ConsumeBytes(value)
			if size < 0 {
				return "", wholeStateArchiveError("truncated graph map key in %s", field.FullName())
			}
			return string(key), nil
		}
		valueSize := protowire.ConsumeFieldValue(number, wireType, value)
		if valueSize < 0 {
			return "", wholeStateArchiveError("invalid graph map value in %s", field.FullName())
		}
		raw = value[valueSize:]
	}
	return "", nil
}
