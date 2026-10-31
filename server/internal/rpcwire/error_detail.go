package rpcwire

import (
	"connectrpc.com/connect"
	"errors"
	"google.golang.org/protobuf/proto"
)

// Every public detail producer bounds its first Any encoding, before Connect
// allocates the serialized detail. Current output then freezes and bounds the
// complete error/metadata envelope within its already reserved request credit.
func BoundedErrorDetail(message proto.Message) (*connect.ErrorDetail, error) {
	if message == nil || proto.Size(message) > 4096 {
		return nil, errors.New("error detail exceeds public output limit")
	}
	return connect.NewErrorDetail(message)
}
