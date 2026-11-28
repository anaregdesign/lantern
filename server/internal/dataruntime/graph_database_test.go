package dataruntime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/tarp"
)

func TestRestoreGraphStorageBuildsSourceOrderBeforeRestoringLegacyCut(t *testing.T) {
	ctx := t.Context()
	db, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	key := graphstore.EdgeKey[string]{Tail: "tail", Head: "head"}
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		return errors.Join(
			rows.PutVertex(b, g, key.Tail, graphstore.Vertex[*pb.Vertex]{Value: &pb.Vertex{Key: key.Tail}}),
			rows.PutVertex(b, g, key.Head, graphstore.Vertex[*pb.Vertex]{Value: &pb.Vertex{Key: key.Head}}),
			rows.PutEdge(b, g, key, graphstore.Edge{LastOrdinal: 1}),
			rows.PutSource(b, g, key, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: 7}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	var saw int
	graph.EnableSearchIndex(func(_ string, _ *pb.Vertex) search.Document {
		cur, err := db.Read()
		if err != nil {
			t.Fatal(err)
		}
		defer cur.Close()
		ready, err := rows.HasSourceOrder(cur.Snapshot, cur.Generation)
		if err != nil || !ready || cur.Revision != legacy.Revision || cur.Generation != legacy.Generation {
			t.Fatalf("Source order during restore: %v, %v, cut %d/%d", ready, err, cur.Generation, cur.Revision)
		}
		saw++
		return search.Text("")
	}, strings.Compare)
	if _, err := RestoreGraphStorage(ctx, graph, db); err != nil {
		t.Fatal(err)
	}
	if saw != 2 {
		t.Fatalf("restored Vertex count = %d", saw)
	}
	if ready, err := rows.HasSourceOrder(legacy.Snapshot, legacy.Generation); err != nil || ready {
		t.Fatalf("old cut observed Source migration: %v, %v", ready, err)
	}
	if weight, found := graph.GetWeight(key.Tail, key.Head); !found || weight != 7 {
		t.Fatalf("restored Edge = %v, %v", weight, found)
	}
}

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
