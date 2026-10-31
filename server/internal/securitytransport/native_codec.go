package securitytransport

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/rpcwire"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// NativeHandlerOptions keeps ambiguous credential representations out of the
// workflow. Ordinary protobuf merging loses duplicates before typed validation.
func NativeHandlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{rpcwire.StrictJSONHandlerOption(), connect.WithCodec(nativeProtoCodec{}), connect.WithReadMaxBytes(NativeAuthMaxBytes), connect.WithSendMaxBytes(NativeAuthMaxBytes)}
}

type nativeProtoCodec struct{}

func (nativeProtoCodec) Name() string { return "proto" }
func (nativeProtoCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("native message requires protobuf")
	}
	return proto.Marshal(message)
}
func (nativeProtoCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return errors.New("invalid native protobuf representation")
	}
	if err := ValidateNativeRequest(data, message, true); err != nil {
		return err
	}
	return proto.Unmarshal(data, message)
}

// ValidateNativeRequest is composed inside the current bounded decoder by the
// provider. Service rules add rejection; they never replace current byte/node
// accounting. The shared current JSON codec already rejects duplicate fields.
func ValidateNativeRequest(data []byte, message proto.Message, binary bool) error {
	if message == nil || !message.ProtoReflect().IsValid() || len(data) > NativeAuthMaxBytes || binary && !nativeProtoFields(data, message.ProtoReflect().Descriptor(), 0) {
		return errors.New("invalid native request representation")
	}
	return nil
}

func nativeProtoFields(data []byte, descriptor protoreflect.MessageDescriptor, depth int) bool {
	if depth > 32 {
		return false
	}
	seen := make(map[protowire.Number]bool)
	oneofs := make(map[protoreflect.Name]bool)
	for len(data) != 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return false
		}
		data = data[n:]
		field := descriptor.Fields().ByNumber(number)
		if field == nil || seen[number] && !field.IsList() && !field.IsMap() {
			return false
		}
		seen[number] = true
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs[oneof.Name()] {
				return false
			}
			oneofs[oneof.Name()] = true
		}
		n = protowire.ConsumeFieldValue(number, kind, data)
		if n < 0 {
			return false
		}
		if field.Kind() == protoreflect.MessageKind {
			if kind != protowire.BytesType {
				return false
			}
			nested, size := protowire.ConsumeBytes(data[:n])
			if size < 0 || !nativeProtoFields(nested, field.Message(), depth+1) {
				return false
			}
		}
		data = data[n:]
	}
	return true
}
