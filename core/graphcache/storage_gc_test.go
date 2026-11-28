package graphcache

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStorageExpirySurvivesClockRollback(t *testing.T) {
	for _, online := range []bool{false, true} {
		name := "Standalone"
		if online {
			name = "Online"
		}
		t.Run(name, func(t *testing.T) { testStorageExpirySurvivesClockRollback(t, online) })
	}
}

func testStorageExpirySurvivesClockRollback(t *testing.T, online bool) {
	t.Helper()
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
		watermark, err := graphstore.VertexExpiryWatermark(t.Context(), s, gen)
		if err != nil || !watermark.Equal(start) {
			t.Fatalf("startup expiry watermark = %v, %v", watermark, err)
		}
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
	now = deadline.Add(time.Second)
	w := c.edges.bucket(pair.Tail, pair.Head)
	if online {
		// The bounded path and clock-rollback reads must not hydrate an
		// entire cold bucket to prune one row or fold the surviving rows.
		load := w.load
		w.load = func(summaryAt *time.Time) (storedWeight, error) {
			if summaryAt == nil {
				t.Fatal("online expiry hydrated a cold bucket")
			}
			return load(summaryAt)
		}
		gate := func(commit func() error) error { return commit() }
		if vertices, edges, dangling, err := c.reclaimOnlineStorage(t.Context(), gate, now); err != nil || vertices != 1 || edges != 1 || dangling != 0 {
			t.Fatalf("online expired vertices=%d edges=%d dangling=%d err=%v", vertices, edges, dangling, err)
		}
		now = start.Add(-time.Hour)
		if value, _, found := w.snapshotAt(now); !found || value != 5 || w.values != nil {
			t.Fatalf("cold bucket rollback scan returned %v %v", value, found)
		}
		w.load = load
	} else {
		if n := c.flushVerticesAt(now); n != 1 {
			t.Fatalf("expired %d vertices", n)
		}
		c.flushAt(now)
		if err := c.flushStorage(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	now = start.Add(-time.Hour)
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
		watermark, err := graphstore.VertexExpiryWatermark(t.Context(), s, g)
		if err != nil || !watermark.Equal(deadline) {
			t.Fatalf("live sweep expiry watermark = %v, %v", watermark, err)
		}
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

func TestOnlineStorageStructuralExpiryRechecksEndpoint(t *testing.T) {
	ctx := t.Context()
	db, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	pair := graphstore.EdgeKey[string]{Tail: "tail", Head: "head"}
	const missing = "missing"
	dangling := graphstore.EdgeKey[string]{Tail: missing, Head: "head"}
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return errors.Join(
			rows.PutVertex(w, g, "head", graphstore.Vertex[string]{}),
			rows.PutEdge(w, g, pair, graphstore.Edge{LastOrdinal: 3}),
			rows.PutSource(w, g, pair, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: 2}),
			rows.PutSource(w, g, pair, graphstore.SourceID{Ordinal: 3}, graphstore.Source{Weight: 3}),
			rows.PutEdge(w, g, dangling, graphstore.Edge{Base: &graphstore.Source{Weight: 1}}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(c.RestoreStorage(ctx, rows, view), view.Close(), c.CompleteStorageRecovery(ctx)); err != nil {
		t.Fatal(err)
	}
	c.SetGCEdgeBudget(1)
	calls := 0
	gate := func(commit func() error) error {
		calls++
		if calls == 2 {
			// This simulates a foreground endpoint restoration after candidate
			// discovery. Its whole Edge and both sibling Sources must survive.
			if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
				return rows.PutVertex(w, g, "tail", graphstore.Vertex[string]{Value: "renewed"})
			}); err != nil {
				return err
			}
		}
		return commit()
	}
	now := time.Unix(1000, 0)
	if _, _, n, err := c.reclaimOnlineStorage(ctx, gate, now); err != nil || n != 1 || calls != 1 || c.GCSweepBacklog() != 1 {
		t.Fatalf("first tail n=%d gates=%d backlog=%d err=%v", n, calls, c.GCSweepBacklog(), err)
	}
	if _, _, n, err := c.reclaimOnlineStorage(ctx, gate, now); err != nil || n != 0 || calls != 2 || c.GCSweepBacklog() != 0 {
		t.Fatalf("renewed tail n=%d gates=%d backlog=%d err=%v", n, calls, c.GCSweepBacklog(), err)
	}
	if err := db.WithCommittedSnapshot(func(g uint64, snap *tarp.Snapshot) error {
		if _, found, err := rows.GetEdge(snap, g, dangling); found || err != nil {
			t.Fatalf("dangling edge present=%t err=%v", found, err)
		}
		for _, ordinal := range []uint64{1, 3} {
			if _, found, err := rows.GetSource(snap, g, pair, graphstore.SourceID{Ordinal: ordinal}); !found || err != nil {
				t.Fatalf("renewed Source %d present=%t err=%v", ordinal, found, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, n, err := c.reclaimOnlineStorage(ctx, gate, now); err != nil || n != 0 || calls != 2 {
		t.Fatalf("no-work tick n=%d gates=%d err=%v", n, calls, err)
	}
}

func TestOnlineStorageExpiryErrorOutcomes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name  string
		err   error
		fence bool
	}{
		{name: "busy staged admission", err: &tarp.OpError{Outcome: tarp.NotApplied, Err: tarp.ErrBusy}},
		{name: "healthy cancellation", err: ctx.Err()},
		{name: "unhealthy cancellation", err: errors.Join(tarp.ErrUnhealthy, ctx.Err()), fence: true},
		{name: "uncertain outcome", err: &tarp.OpError{Outcome: tarp.Unknown, Err: tarp.ErrBusy}, fence: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &GraphCache[string, string]{storage: &storageResidency[string, string]{}}
			if err := c.onlineStorageError(ctx, test.err); err != test.err {
				t.Fatalf("online storage error = %v, want %v", err, test.err)
			}
			if err := c.StorageError(); (err != nil) != test.fence {
				t.Fatalf("fenced = %v, want %t", err, test.fence)
			}
		})
	}
}
