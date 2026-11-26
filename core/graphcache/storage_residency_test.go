package graphcache

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStoredGraphStagingRestoresColdRowsOnAbort(t *testing.T) {
	db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	k := graphstore.EdgeKey[string]{Tail: "x", Head: "y"}
	if err := db.UpdateRows(t.Context(), func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return errors.Join(
			rows.PutVertex(w, gen, "x", graphstore.Vertex[string]{Value: "accepted"}),
			rows.PutVertex(w, gen, "y", graphstore.Vertex[string]{}),
			rows.PutEdge(w, gen, k, graphstore.Edge{Base: &graphstore.Source{Weight: 3}}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(c.RestoreStorage(t.Context(), rows, view), view.Close()); err != nil {
		t.Fatal(err)
	}
	tx, err := c.BeginVertexPut([]VertexItem[string, string]{{Key: "x", Value: "candidate"}}, hlc.Timestamp{WallNs: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	if _, _, present, cold := c.vertices.PeekResident("x"); !present || !cold {
		t.Fatal("aborting a staged Vertex write retained a previous payload")
	}
	if v, ok, err := c.ReadVertex("x"); err != nil || !ok || v != "accepted" {
		t.Fatalf("abort Vertex = %q, %v, %v", v, ok, err)
	}
	add, err := c.BeginEdgeAdd([]EdgeItem[string]{{Tail: "x", Head: "y", Weight: 2, ContribID: ContribID{1}}}, hlc.Timestamp{WallNs: 11})
	if err != nil {
		t.Fatal(err)
	}
	if effective := add.Result().Effective; len(effective) != 1 || effective[0] != 5 {
		t.Fatalf("staged add = %+v", effective)
	}
	add.Abort()
	w := c.edges.bucket("x", "y")
	if !w.cold || len(w.values) != 0 {
		t.Fatal("aborting a staged Edge write retained previous contributions")
	}
	if value, ok := c.GetWeight("x", "y"); !ok || value != 3 {
		t.Fatalf("abort Edge = %v, %v", value, ok)
	}
	// Losing a canonical row while a serving descriptor exists is an error.
	if err := db.UpdateRows(t.Context(), func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		return rows.DeleteVertex(b, g, "x")
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.ReadVertex("x"); ok || !errors.Is(err, graphstore.ErrInvalidRow) || !errors.Is(c.StorageError(), graphstore.ErrInvalidRow) {
		t.Fatalf("missing backing row = %v, %v (fence=%v)", ok, err, c.StorageError())
	}
}
