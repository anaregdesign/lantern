package service

import (
	"fmt"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestDataEnvelopeOwnsIdentitiesAndSharesImmutablePayload(t *testing.T) {
	payload := make([]byte, 1<<20)
	original := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "logical", Value: &pb.Vertex_Bytes{Bytes: payload}}}}
	cloned := cloneDataEnvelope(original.ProtoReflect()).Interface().(*pb.PutVerticesRequest)
	if !proto.Equal(original, cloned) {
		t.Fatal("clone changed request")
	}
	if &cloned.Vertices[0].GetBytes()[0] != &payload[0] {
		t.Fatal("boundary copied the data payload")
	}
	cloned.Vertices[0].Key = "data:logical"
	cloned.Vertices = append(cloned.Vertices, &pb.Vertex{Key: "data:other"})
	if original.Vertices[0].Key != "logical" || len(original.Vertices) != 1 {
		t.Fatal("identity mutation escaped envelope")
	}
}

func BenchmarkDataEnvelopePut(b *testing.B) {
	for _, size := range []int{1024, 4 << 20} {
		request := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "logical", Value: &pb.Vertex_Bytes{Bytes: make([]byte, size)}}}}
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				cloned := cloneDataEnvelope(request.ProtoReflect())
				if err := mapDataIdentities(cloned, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
