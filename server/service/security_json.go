package service

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// SecurityHandlerOptions preserves unknown-field rejection on JSON as well as
// binary protobuf. Connect's general-purpose JSON codec discards unknown fields.
// Installing this codec is mandatory on the typed control service.
func SecurityHandlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{StrictJSONHandlerOption(), connect.WithReadMaxBytes(4 << 20), connect.WithSendMaxBytes(4 << 20)}
}

// StrictJSONHandlerOption preserves unknown-field rejection on any protected
// protobuf service without changing that service's own message-size limits.
func StrictJSONHandlerOption() connect.HandlerOption { return connect.WithCodec(securityJSONCodec{}) }

type securityJSONCodec struct{}

func (securityJSONCodec) Name() string { return "json" }
func (securityJSONCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, errors.New("JSON requires protobuf")
	}
	return protojson.Marshal(message)
}
func (securityJSONCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return errors.New("JSON requires protobuf")
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, message)
}
