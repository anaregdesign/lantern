package graphstorage

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestVertexCodec(t *testing.T) {
	codec := vertexCodec{}
	for _, vertex := range []*pb.Vertex{nil, {}, {Key: "data:a\x00b"}} {
		raw, err := codec.Encode(vertex)
		if err != nil {
			t.Fatal(err)
		}
		got, err := codec.Decode(raw)
		if err != nil || !proto.Equal(got, vertex) || (vertex == nil) != (got == nil) {
			t.Fatalf("round trip of %v returned %v, %v", vertex, got, err)
		}
	}
	for _, invalid := range [][]byte{nil, {0, 1}, {2}, {1, 0xff}} {
		if _, err := codec.Decode(invalid); err == nil {
			t.Errorf("accepted invalid bytes %x", invalid)
		}
	}
}
