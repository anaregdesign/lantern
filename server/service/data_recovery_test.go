package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func TestDataRecoveryRestartAndFormatRefusal(t *testing.T) {
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "data.wal"))
	config.NamespaceFormat = keyspace.Version
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	peer, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, peer); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.PutVertices(context.Background(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:sys:client"}}}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	before := make(map[string][]byte)
	for _, suffix := range []string{"", ".tip", ".clock", ".generation"} {
		before[suffix], err = os.ReadFile(config.Path + suffix)
		if err != nil {
			t.Fatal(err)
		}
	}
	wrong := config
	wrong.NamespaceFormat = ""
	wrong.Now = time.Now().Add(time.Minute)
	wrong.Receipt.ClockHighWater = wrong.Now
	if reopened, err := OpenDurableReceiptWALServingRuntime(wrong); err == nil {
		_ = reopened.Close()
		t.Fatal("unclassified restart accepted namespaced WAL")
	}
	for suffix, data := range before {
		after, err := os.ReadFile(config.Path + suffix)
		if err != nil || !bytes.Equal(data, after) {
			t.Fatalf("failed restart changed %q: %v", suffix, err)
		}
	}
	reopened, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if value, ok := reopened.graph.GetVertex("data:sys:client"); !ok || value.GetKey() != "data:sys:client" {
		t.Fatal("physical identity drifted across restart")
	}
	if reopened.NewLanternService(nil).DataNamespaceFormat() != keyspace.Version {
		t.Fatal("restarted public mapper was not installed")
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
