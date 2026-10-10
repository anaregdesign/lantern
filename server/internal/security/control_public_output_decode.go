package security

import (
	"errors"
	"reflect"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	currentDecodeDepth      = 100
	currentDecodeNodeBytes  = 512
	currentDecodeMaxNodes   = 128 << 10
	currentDecodeMaxStruct  = 256
	currentDecodeFixedBytes = 1 << 20
)

// Structural credit is independent of encoded bytes. Each counted node covers
// a <=256-byte generated message or a field/list element: <=24-byte element,
// slice growth (old and new backing arrays), scalar/oneof/reflection boxes and
// allocator rounding. Message fields count both the field and child message.
// JSON counts containers and ALL tokens, including field names. There are no
// maps, extensions, custom WKT decoders or dynamic messages in this certificate.
// See the derivation and the separately charged byte buffers in the runbook.
func currentDecodeNodes(read int) int {
	return min(currentDecodeMaxNodes, max(256, read/32))
}

func currentDecodeCharge(read int) uint64 {
	// 8*read covers decoded string/bytes/unknown backing storage, escape/base64
	// scratch and simultaneous growth/copy storage, in addition to Connect's
	// existing 4*read compressed/uncompressed buffers. Nodes are a separate cap.
	return uint64(8*read + currentDecodeNodeBytes*currentDecodeNodes(read) + currentDecodeFixedBytes)
}

// Built from linked generated descriptors, never from request data. Only the
// certified generated representation may reach the allocating decoder. Schema
// drift outside the envelope refuses; the all-public-method test requires an
// explicit proof update before such a schema can be added to a public service.
var currentDecodeSchemas = sync.OnceValue(func() map[protoreflect.MessageDescriptor]reflect.Type {
	result := make(map[protoreflect.MessageDescriptor]reflect.Type)
	protoregistry.GlobalTypes.RangeMessages(func(mt protoreflect.MessageType) bool {
		md := mt.Descriptor()
		if currentDecodeSchema(md, make(map[protoreflect.MessageDescriptor]bool)) {
			result[md] = reflect.TypeOf(mt.Zero().Interface())
		}
		return true
	})
	return result
})

func currentDecodeSchema(md protoreflect.MessageDescriptor, seen map[protoreflect.MessageDescriptor]bool) bool {
	if seen[md] {
		return true
	}
	seen[md] = true
	if md.IsMapEntry() || md.ExtensionRanges().Len() != 0 {
		return false
	}
	name := string(md.FullName())
	if !strings.HasPrefix(name, "graph.v1.") && !strings.HasPrefix(name, "connectext.grpc.health.v1.") && !strings.HasPrefix(name, "connectext.grpc.reflection.v1.") && name != "google.protobuf.Timestamp" && name != "google.protobuf.Duration" && name != "google.protobuf.Empty" {
		return false
	}
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName())
	if err != nil || mt.Descriptor() != md {
		return false
	}
	typ := reflect.TypeOf(mt.Zero().Interface())
	if typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct || typ.Elem().Size() > currentDecodeMaxStruct {
		return false
	}
	// Exclude custom or dynamic implementations, even with a copied descriptor.
	pkg := typ.Elem().PkgPath()
	if pkg != "github.com/anaregdesign/lantern/pb/graph/v1" && pkg != "connectrpc.com/grpchealth/internal/gen/go/connectext/grpc/health/v1" && pkg != "connectrpc.com/grpcreflect/internal/gen/go/connectext/grpc/reflection/v1" && pkg != "google.golang.org/protobuf/types/known/timestamppb" && pkg != "google.golang.org/protobuf/types/known/durationpb" && pkg != "google.golang.org/protobuf/types/known/emptypb" {
		return false
	}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.IsMap() || f.Kind() == protoreflect.GroupKind {
			return false
		}
		if f.Kind() == protoreflect.MessageKind && !currentDecodeSchema(f.Message(), seen) {
			return false
		}
	}
	return true
}

// No message-sized allocation or destination mutation precedes this complete
// preflight. In particular, repeated empty messages and packed scalars cannot
// exploit an encoded-byte-to-heap multiplier. Unknown binary fields are counted
// and their groups are depth checked; their payload remains opaque byte storage.
func currentCheckDecode(raw []byte, m proto.Message, binary bool, nodes int) error {
	reflected := m.ProtoReflect()
	if !reflected.IsValid() || currentDecodeSchemas()[reflected.Descriptor()] != reflect.TypeOf(m) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("uncertified current request schema"))
	}
	b := currentDecodeBudget{remaining: nodes}
	var err error
	if binary {
		_, err = b.wire(raw, reflected.Descriptor(), 0, 0)
	} else {
		err = b.json(raw)
	}
	return err
}

type currentDecodeBudget struct{ remaining int }

func (b *currentDecodeBudget) take() error {
	if b.remaining <= 0 {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("decoded request structure exceeds current output limit"))
	}
	b.remaining--
	return nil
}
func currentDecodeInvalid() error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid current request framing"))
}

func (b *currentDecodeBudget) wire(raw []byte, md protoreflect.MessageDescriptor, depth int, group protowire.Number) (int, error) {
	if depth >= currentDecodeDepth {
		return 0, currentDecodeInvalid()
	}
	if err := b.take(); err != nil {
		return 0, err
	}
	start := len(raw)
	for len(raw) != 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 || !num.IsValid() {
			return 0, currentDecodeInvalid()
		}
		raw = raw[n:]
		if typ == protowire.EndGroupType {
			if group == 0 || num != group {
				return 0, currentDecodeInvalid()
			}
			return start - len(raw), nil
		}
		if err := b.take(); err != nil {
			return 0, err
		}
		var field protoreflect.FieldDescriptor
		if md != nil {
			field = md.Fields().ByNumber(num)
		}
		switch typ {
		case protowire.BytesType:
			payload, size := protowire.ConsumeBytes(raw)
			if size < 0 {
				return 0, currentDecodeInvalid()
			}
			n = size
			if field != nil && field.Kind() == protoreflect.MessageKind {
				if _, err := b.wire(payload, field.Message(), depth+1, 0); err != nil {
					return 0, err
				}
			} else if field != nil && field.IsList() && field.Kind() != protoreflect.StringKind && field.Kind() != protoreflect.BytesKind {
				for len(payload) != 0 {
					if err := b.take(); err != nil {
						return 0, err
					}
					size := currentPackedScalar(payload, field.Kind())
					if size < 0 {
						return 0, currentDecodeInvalid()
					}
					payload = payload[size:]
				}
			}
		case protowire.StartGroupType:
			// No supported schema has groups. Unknown groups are retained as bytes,
			// with all nested tags charged and depth bounded before protobuf sees them.
			var err error
			n, err = b.wire(raw, nil, depth+1, num)
			if err != nil {
				return 0, err
			}
		default:
			n = protowire.ConsumeFieldValue(num, typ, raw)
			if n < 0 {
				return 0, currentDecodeInvalid()
			}
		}
		raw = raw[n:]
	}
	if group != 0 {
		return 0, currentDecodeInvalid()
	}
	return start, nil
}

func currentPackedScalar(raw []byte, kind protoreflect.Kind) int {
	switch kind {
	case protoreflect.FloatKind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		_, n := protowire.ConsumeFixed32(raw)
		return n
	case protoreflect.DoubleKind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		_, n := protowire.ConsumeFixed64(raw)
		return n
	default:
		_, n := protowire.ConsumeVarint(raw)
		return n
	}
}

// This lexical pass deliberately does not replace protojson's grammar or field
// validation. It counts every token, including unknown names/values, without
// unquoting, building a DOM or allocating a decoder stack. Even malformed input
// must fit the structural envelope before the real decoder can allocate.
func (b *currentDecodeBudget) json(raw []byte) error {
	var stack [currentDecodeDepth]byte
	depth := 0
	if err := b.take(); err != nil {
		return err
	} // root generated message
	for i := 0; i < len(raw); {
		c := raw[i]
		switch c {
		case ' ', '\n', '\r', '\t', ',', ':':
			i++
			continue
		case '}', ']':
			if depth == 0 || (c == '}' && stack[depth-1] != '{') || (c == ']' && stack[depth-1] != '[') {
				return currentDecodeInvalid()
			}
			depth--
			i++
			continue
		}
		if err := b.take(); err != nil {
			return err
		}
		switch c {
		case '{', '[':
			if depth == len(stack) {
				return currentDecodeInvalid()
			}
			stack[depth] = c
			depth++
			i++
		case '"':
			i++
			closed := false
			for i < len(raw) {
				c = raw[i]
				i++
				if c == '"' {
					closed = true
					break
				}
				if c == '\\' {
					i++
				}
			}
			if !closed {
				return currentDecodeInvalid()
			}
		default:
			// Strings and punctuation terminate invalid tokens too. This cannot hide
			// later containers from the cost check; protojson owns semantic rejection.
			for i < len(raw) && !strings.ContainsRune(" \n\r\t,:{}[]\"", rune(raw[i])) {
				i++
			}
		}
	}
	if depth != 0 {
		return currentDecodeInvalid()
	}
	return nil
}
