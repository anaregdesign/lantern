package rpcwire

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// StrictJSONHandlerOption preserves unknown-field rejection on any protected
// protobuf service without changing that service's own message-size limits.
func StrictJSONHandlerOption() connect.HandlerOption { return connect.WithCodec(strictJSONCodec{}) }

type strictJSONCodec struct{}

func (strictJSONCodec) Name() string { return "json" }
func (strictJSONCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("JSON requires protobuf")
	}
	return protojson.Marshal(message)
}
func (strictJSONCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return errors.New("JSON requires protobuf")
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, message)
}
