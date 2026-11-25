package provider

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func TestGraphStoreReopensNodeAndNamespacedValues(t *testing.T) {
	t.Setenv("LANTERN_DATA_DIR", filepath.Join(t.TempDir(), "graph"))
	t.Setenv("LANTERN_NODE_ID", "")
	config, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	open := func(node ReplicationConfig) (*dataruntime.ServingRuntime, func(), error) {
		return NewServingRuntime(config.ReceiptWAL, config.Cache, config.Search, config.MutationLog, node, backup.Config{}, nil)
	}
	rt, closeRT, err := open(config.Replication)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRT()
	id := rt.NodeID()
	if id == (hlc.NodeID{}) {
		t.Fatal("missing persisted origin")
	}
	primary := rt.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "data:sys:literal", Value: &pb.Vertex_String_{String_: "retained"}}}); err != nil {
		t.Fatal(err)
	}
	closeRT()
	if _, cleanup, err := open(ReplicationConfig{NodeID: hlc.NodeID{9}, nodeIDExplicit: true}); !errors.Is(err, graphstore.ErrNodeMismatch) {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("foreign node opened database: %v", err)
	}
	reopened, cleanup, err := open(config.Replication)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if reopened.NodeID() != id {
		t.Fatal("restart changed node identity")
	}
	primary = reopened.NewExecutor(nil)
	got, err := primary.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "data:sys:literal"})
	if err != nil || got.GetVertex().GetString_() != "retained" || primary.LocalSeq(id) != 1 {
		t.Fatalf("reopen got %v, seq %d, err %v", got, primary.LocalSeq(id), err)
	}
}
