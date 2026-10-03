package keyspace

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"testing"
)

func TestValidatePhysicalGraphMessage(t *testing.T) {
	for _, key := range []string{"data:user:1", "data:sys:client", "data:data:client", "data:利用者"} {
		request := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: key, Value: &pb.Vertex_String_{String_: "sys:ordinary-value"}}}}
		if err := ValidatePhysicalGraphMessage(request.ProtoReflect()); err != nil {
			t.Fatal(key, err)
		}
	}
	for _, key := range []string{"", "data:", "sys:security:roles", "users:1", "data:\xff"} {
		request := &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "data:good", Head: key}}}
		if err := ValidatePhysicalGraphMessage(request.ProtoReflect()); err == nil {
			t.Fatalf("accepted physical head %q", key)
		}
	}
	if err := ValidatePhysicalGraphMessage((&pb.SnapshotHeader{CutoffSeqPerOrigin: map[string]uint64{"origin": 1}}).ProtoReflect()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFormat(Version); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFormat("unknown"); err == nil {
		t.Fatal("unknown format accepted")
	}
}
