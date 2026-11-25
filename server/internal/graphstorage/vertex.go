// Package graphstorage binds Lantern's public vertex representation to the Core disk schema.
package graphstorage

import (
	"errors"

	"github.com/anaregdesign/lantern/core/graphstore"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/proto"
)

// Rows uses bytewise UTF-8 key order. Presence of an implicit endpoint with a
// nil value is distinct from an explicitly stored empty protobuf message.
func Rows() (*graphstore.Rows[string, *pb.Vertex], error) {
	return graphstore.NewRows[string, *pb.Vertex](tarp.StringCodec{}, vertexCodec{})
}

type vertexCodec struct{}

func (vertexCodec) Encode(vertex *pb.Vertex) ([]byte, error) {
	if vertex == nil {
		return []byte{0}, nil
	}
	return proto.MarshalOptions{Deterministic: true}.MarshalAppend([]byte{1}, vertex)
}

func (vertexCodec) Decode(data []byte) (*pb.Vertex, error) {
	if len(data) == 1 && data[0] == 0 {
		return nil, nil
	}
	if len(data) == 0 || data[0] != 1 {
		return nil, errors.New("graphstorage: invalid vertex presence tag")
	}
	var value pb.Vertex
	if err := proto.Unmarshal(data[1:], &value); err != nil {
		return nil, err
	}
	return &value, nil
}
