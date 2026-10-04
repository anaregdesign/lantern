package replication

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

type namespaceTestStream struct {
	frames  []*pb.SnapshotResponse
	current *pb.SnapshotResponse
}

func (s *namespaceTestStream) Receive() bool {
	if len(s.frames) == 0 {
		return false
	}
	s.current = s.frames[0]
	s.frames = s.frames[1:]
	return true
}
func (s *namespaceTestStream) Msg() *pb.SnapshotResponse { return s.current }
func (*namespaceTestStream) Err() error                  { return nil }

func TestNamespaceSnapshotRejectsBeforeExposure(t *testing.T) {
	for _, format := range []string{"", "unknown", keyspace.Version} {
		raw := &namespaceTestStream{frames: []*pb.SnapshotResponse{{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{NamespaceFormat: format}}}}}
		stream := &namespaceSnapshotStream{stream: raw, format: keyspace.Version}
		if got := stream.Receive(); got != (format == keyspace.Version) {
			t.Fatalf("format %q: receive=%v", format, got)
		}
		if format != keyspace.Version && stream.Err() == nil {
			t.Fatal("mismatch did not fail stream")
		}
	}
	for _, key := range []string{"data:client", "sys:security:roles", "client", "data:"} {
		raw := &namespaceTestStream{frames: []*pb.SnapshotResponse{{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: &pb.Vertex{Key: key}}}}}}
		stream := &namespaceSnapshotStream{stream: raw, format: keyspace.Version}
		if got := stream.Receive(); got != (key == "data:client") {
			t.Fatalf("key %q: receive=%v", key, got)
		}
	}
}

func TestNamespaceMutationValidatesBeforeSelfEcho(t *testing.T) {
	for _, key := range []string{"data:client", "sys:security:roles", "client"} {
		mutation := &pb.Mutation{NamespaceFormat: keyspace.Version, Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}}}}}
		if err := validateDataMutation(mutation, keyspace.Version); (err == nil) != (key == "data:client") {
			t.Fatalf("key %q: %v", key, err)
		}
		if err := validateDataMutation(mutation, ""); err == nil {
			t.Fatal("mutation format downgrade accepted")
		}
	}
}
