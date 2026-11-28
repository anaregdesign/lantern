package graphcache

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStoredSourceChangesCanBePreparedTwice(t *testing.T) {
	db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	k := graphstore.EdgeKey[string]{Tail: "x", Head: "y"}
	now := time.Now()
	exp := now.Add(time.Second)
	if err := db.UpdateRows(t.Context(), func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		return errors.Join(
			r.PutVertex(b, g, "x", graphstore.Vertex[string]{}), r.PutVertex(b, g, "y", graphstore.Vertex[string]{}),
			r.PutEdge(b, g, k, graphstore.Edge{Base: &graphstore.Source{Weight: 3}, LastOrdinal: 4}),
			r.PutSource(b, g, k, graphstore.SourceID{Ordinal: 4}, graphstore.Source{Weight: 4, Expiration: exp}),
			r.PutSource(b, g, k, graphstore.SourceID{ContribID: [24]byte{1}}, graphstore.Source{Weight: 2}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	v, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { return now }
	if err := errors.Join(c.RestoreStorage(t.Context(), r, v), v.Close()); err != nil {
		t.Fatal(err)
	}
	w := c.edges.bucket("x", "y")
	if w == nil || !w.cold {
		t.Fatal("expected disk bucket")
	}
	if v, _, _ := w.snapshotAt(now); v != 9 {
		t.Fatalf("cached aggregate = %v", v)
	}
	if v, _, _ := w.snapshotAt(exp); v != 5 {
		t.Fatalf("aggregate at boundary = %v", v)
	}
	if v, _, _ := w.snapshotAt(now); v != 9 {
		t.Fatalf("aggregate before boundary = %v", v)
	}
	after := cloneWeightForStaging(w, exp)
	applied, total := after.addWithExpirationContribAt(6, time.Time{}, ContribID{2}, exp)
	if !applied || total != 11 {
		t.Fatalf("candidate add = %v, %v", applied, total)
	}
	// Only the expired local Add and newly accepted explicit Add should be
	// written with the header. The other explicit source stays on disk.
	want := sourceChanges{
		graphstore.SourceID{Ordinal: 4}:             {source: graphstore.Source{Weight: 4, Expiration: exp.UTC()}, deleted: true},
		graphstore.SourceID{ContribID: [24]byte{2}}: {source: graphstore.Source{Weight: 6}},
	}
	if !reflect.DeepEqual(after.changes, want) {
		t.Fatalf("changes = %+v", after.changes)
	}
	cut, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer cut.Close()
	for range 2 {
		var b tarp.WriteBatch
		if err := writeSourceChanges(after, r, cut.Snapshot, &b, cut.Generation, k); err != nil || b.Len() != 5 {
			t.Fatalf("prepare len=%d, err=%v", b.Len(), err)
		}
	}
	c.storage.bindWeight(k, after)
	if after.changes != nil {
		t.Fatal("published source changes stayed resident")
	}
}
