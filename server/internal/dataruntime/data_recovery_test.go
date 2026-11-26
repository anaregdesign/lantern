package dataruntime

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func TestDataSnapshotNamespaceRequiresExplicitBinding(t *testing.T) {
	frames := []*pb.SnapshotResponse{{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{NamespaceFormat: keyspace.Version}}}}
	if err := ValidateDataSnapshotNamespace(frames, keyspace.Version); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"", "unknown"} {
		if err := ValidateDataSnapshotNamespace(frames, format); err == nil {
			t.Fatalf("accepted unexpected namespace %q", format)
		}
	}
}

func TestDataRecoverySnapshotRejectsMixedDomains(t *testing.T) {
	for _, key := range []string{"data:users:1", "users:1", "sys:security:roles", "data:"} {
		frames := []*pb.SnapshotResponse{
			{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{NamespaceFormat: keyspace.Version}}},
			{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: &pb.Vertex{Key: key}}}},
		}
		if err := ValidateDataSnapshotNamespace(frames, keyspace.Version); (err == nil) != (key == "data:users:1") {
			t.Fatalf("key %q: %v", key, err)
		}
		if err := ValidateDataSnapshotNamespace(frames, ""); err == nil {
			t.Fatal("snapshot downgraded to unclassified keys")
		}
	}
}
