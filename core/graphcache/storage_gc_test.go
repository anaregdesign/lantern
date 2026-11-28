package graphcache

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStorageExpirySurvivesClockRollback(t *testing.T) {
	db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1000, 0)
	deadline := start.Add(time.Minute)
	pair := graphstore.EdgeKey[string]{Tail: "x", Head: "y"}
	startupEdge := graphstore.EdgeKey[string]{Tail: "startup", Head: "y"}
	tickEdge := graphstore.EdgeKey[string]{Tail: "tick", Head: "y"}
	old := hlc.Timestamp{WallNs: 12, NodeID: hlc.NodeID{2}}
	floor := graphstore.FloorKey[string]{Kind: graphstore.ContributionTombstone, Tail: "x", Head: "y", ContribID: [24]byte{2}}
	if err := db.UpdateRows(t.Context(), func(g uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return errors.Join(
			rows.PutVertex(w, g, "x", graphstore.Vertex[string]{Value: "tail"}),
			rows.PutVertex(w, g, "y", graphstore.Vertex[string]{Value: "head"}),
			rows.PutVertex(w, g, "startup", graphstore.Vertex[string]{Value: "old", HLC: old, Expiration: start}),
			rows.PutVertex(w, g, "tick", graphstore.Vertex[string]{Value: "old", HLC: old, Expiration: deadline}),
			rows.PutEdge(w, g, pair, graphstore.Edge{LastOrdinal: 8, Base: &graphstore.Source{Weight: 1}}),
			rows.PutEdge(w, g, startupEdge, graphstore.Edge{HLC: old, Base: &graphstore.Source{Weight: 1, Expiration: start}}),
			rows.PutEdge(w, g, tickEdge, graphstore.Edge{HLC: old, Base: &graphstore.Source{Weight: 1, Expiration: deadline}}),
			rows.PutSource(w, g, pair, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: 100, Expiration: start}),
			rows.PutSource(w, g, pair, graphstore.SourceID{Ordinal: 7}, graphstore.Source{Weight: 10, Expiration: deadline}),
			rows.PutSource(w, g, pair, graphstore.SourceID{Ordinal: 8}, graphstore.Source{Weight: 4}),
			rows.PutFloor(w, g, floor, graphstore.Floor{HLC: old, Expiration: deadline}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	now := start
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { return now }
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(c.RestoreStorage(t.Context(), rows, view), view.Close()); err != nil {
		t.Fatal(err)
	}
	now = start.Add(-time.Hour)
	if err := c.CompleteStorageRecovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkIncoming := func(gen uint64, s *tarp.Snapshot, want ...graphstore.EdgeKey[string]) {
		t.Helper()
		if ready, err := rows.HasIncomingEdgeIndex(s, gen); !ready || err != nil {
			t.Fatalf("incoming readiness = %v, %v", ready, err)
		}
		var got []graphstore.EdgeKey[string]
		for key, err := range rows.IncomingEdges(t.Context(), s, gen, "y") {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, key)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("incoming y = %v, want %v", got, want)
		}
		size := uint64(len(want))
		if stats, ready, err := rows.GetTopologyStats(s, gen); err != nil || !ready || stats.Edges != size || stats.NonemptyTails != size {
			t.Fatalf("topology after GC = %+v, ready=%t, err=%v", stats, ready, err)
		}
		if degree, err := rows.GetTopologyDegree(s, gen, "y"); err != nil || degree.In != size || degree.Out != 0 {
			t.Fatalf("degree after GC = %+v, err=%v", degree, err)
		}
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, s *tarp.Snapshot) error {
		checkIncoming(gen, s, tickEdge, pair)
		if _, ok, err := rows.GetVertex(s, gen, "startup"); err != nil || ok {
			t.Fatalf("expired startup row found=%v err=%v", ok, err)
		}
		if _, ok, err := rows.GetSource(s, gen, pair, graphstore.SourceID{Ordinal: 1}); err != nil || ok {
			t.Fatalf("expired startup source found=%v err=%v", ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now = deadline
	if n := c.flushVerticesAt(now); n != 1 {
		t.Fatalf("expired %d vertices", n)
	}
	c.flushAt(now)
	if err := c.flushStorage(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	now = start.Add(-time.Hour)
	w := c.edges.bucket(pair.Tail, pair.Head)
	if w == nil || !w.cold || w.values != nil {
		t.Fatal("swept bucket kept its payload")
	}
	if value, _, found := w.snapshotAt(now); !found || value != 5 || w.values != nil {
		t.Fatalf("cold bucket rollback scan returned %v %v", value, found)
	}
	if _, _, found, cold := c.vertices.PeekResident("x"); !found || !cold {
		t.Fatal("startup retained the live vertex payload")
	}
	if err := db.WithCommittedSnapshot(func(g uint64, s *tarp.Snapshot) error {
		checkIncoming(g, s, pair)
		if _, found, err := rows.GetVertex(s, g, "tick"); found || err != nil {
			t.Fatalf("swept vertex found=%v err=%v", found, err)
		}
		for _, key := range []string{"startup", "tick"} {
			f, found, err := rows.GetFloor(s, g, graphstore.FloorKey[string]{Kind: graphstore.VertexBarrier, Key: key})
			if err != nil || !found || f.HLC != old {
				t.Fatalf("floor for %q = %+v, %v, %v", key, f, found, err)
			}
		}
		if _, found, err := rows.GetFloor(s, g, floor); found || err != nil {
			t.Fatalf("expired contribution tombstone found=%v err=%v", found, err)
		}
		e, found, err := rows.GetEdge(s, g, pair)
		if err != nil || !found || e.LastOrdinal != 8 || e.Base == nil || e.Base.Weight != 1 {
			t.Fatalf("edge = %+v, %v, %v", e, found, err)
		}
		var ordinals []uint64
		for entry, err := range rows.Sources(t.Context(), s, g, pair) {
			if err != nil {
				return err
			}
			ordinals = append(ordinals, entry.ID.Ordinal)
		}
		if len(ordinals) != 1 || ordinals[0] != 8 {
			t.Fatalf("surviving ordinals %v", ordinals)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if applied, value := w.addWithExpirationContribAt(2, time.Time{}, ContribID{}, now); !applied || value != 7 {
		t.Fatalf("add on reopened bucket = %v, %v", applied, value)
	}
	e, sources := storageEdge(w)
	if e.LastOrdinal != 9 || sources[graphstore.SourceID{Ordinal: 9}].Weight != 2 {
		t.Fatalf("lost source high-water mark: %+v %+v", e, sources)
	}
}
