package service

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestCurrentDataRequestDigestBindsTypeOrderAndShape(t *testing.T) {
	a, err := currentDataRequestDigest(&pb.GetVerticesRequest{Keys: []string{"orders:a", "orders:b"}})
	if err != nil || a == [32]byte{} {
		t.Fatal(err)
	}
	for _, request := range []proto.Message{&pb.GetVerticesRequest{Keys: []string{"orders:b", "orders:a"}}, &pb.DeleteVerticesRequest{Keys: []string{"orders:a", "orders:b"}}, &pb.GetVerticesRequest{Keys: []string{"orders:a"}}} {
		other, err := currentDataRequestDigest(request)
		if err != nil || other == a {
			t.Fatal("request shape or type not bound", request, err)
		}
	}
	for _, request := range []proto.Message{nil, (*pb.GetVerticesRequest)(nil)} {
		if _, err := currentDataRequestDigest(request); err == nil {
			t.Fatal("missing request accepted")
		}
	}
}
