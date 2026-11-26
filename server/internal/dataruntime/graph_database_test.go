package dataruntime

import (
	"path/filepath"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func TestOpenReceiptDatabasePreservesNamespacedCutOnRefusal(t *testing.T) {
	cfg := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "data"))
	cfg.NamespaceFormat = keyspace.Version
	rt, err := OpenReceiptDatabase(t.Context(), cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	executor := rt.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	peer, err := rt.NewReplication(executor)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CertifyInstallation(executor, peer); err != nil {
		t.Fatal(err)
	}
	const key = "data:sys:client"
	if _, err := executor.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}}); err != nil {
		t.Fatal(err)
	}
	cut := func(runtime *ServingRuntime) (uint64, TarpBackupWitness) {
		t.Helper()
		view, err := runtime.GraphStore().Read()
		if err != nil {
			t.Fatal(err)
		}
		defer view.Close()
		witness, err := runtime.TarpBackupWitness(view.Snapshot, view.Generation)
		if err != nil {
			t.Fatal(err)
		}
		return view.Revision, witness
	}
	revision, witness := cut(rt)
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := cfg
	wrong.NamespaceFormat = ""
	wrong.Now = cfg.Now.Add(time.Minute)
	wrong.Receipt.ClockHighWater = wrong.Now
	if reopened, err := OpenReceiptDatabase(t.Context(), wrong, false); err == nil {
		_ = reopened.Close()
		t.Fatal("unclassified restart accepted the namespaced database")
	}
	reopened, err := OpenReceiptDatabase(t.Context(), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if gotRev, gotWitness := cut(reopened); gotRev != revision || gotWitness != witness {
		t.Fatalf("rejected restart changed the durable cut: revision %d -> %d, witness %+v -> %+v", revision, gotRev, witness, gotWitness)
	}
	if value, ok := reopened.graph.GetVertex(key); !ok || value.GetKey() != key || reopened.DataNamespaceFormat() != keyspace.Version {
		t.Fatal("classified graph identity did not survive restart")
	}
}
