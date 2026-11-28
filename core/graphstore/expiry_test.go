package graphstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestReclaimExpiredResumesWithFreshCut(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir, hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 1)
	expiredDeadline := now.Add(-time.Second)
	old := hlc.Timestamp{WallNs: 11, NodeID: hlc.NodeID{1}}
	renewed := hlc.Timestamp{WallNs: 12, NodeID: hlc.NodeID{1}}
	future := now.Add(time.Hour)
	key := func(i int) string { return fmt.Sprintf("expired:\x00%03d", i) }
	pair := EdgeKey[string]{Tail: "gone", Head: "pair"}
	mixed := EdgeKey[string]{Tail: "mixed", Head: "pair"}
	tombstone := FloorKey[string]{Kind: ContributionTombstone, Tail: mixed.Tail, Head: mixed.Head, ContribID: [24]byte{11}}
	if err := db.UpdateRows(t.Context(), func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		for i := range expiryChunkRows + 3 {
			if err := rows.PutVertex(b, gen, key(i), Vertex[string]{Value: "expired", HLC: old, Expiration: expiredDeadline}); err != nil {
				return err
			}
		}
		for i := range expiryChunkRows + 3 {
			if err := rows.PutSource(b, gen, mixed, SourceID{Ordinal: uint64(i + 1)}, Source{Weight: 2, HLC: old, Expiration: now}); err != nil {
				return err
			}
		}
		return errors.Join(
			rows.PutVertex(b, gen, "permanent", Vertex[string]{Value: "kept"}),
			rows.PutFloor(b, gen, FloorKey[string]{Kind: VertexBarrier, Key: key(0)}, Floor{HLC: renewed}),
			rows.PutFloor(b, gen, tombstone, Floor{HLC: old, Expiration: now}),
			rows.PutEdge(b, gen, pair, Edge{HLC: old, Base: &Source{Expiration: now}}),
			rows.PutEdge(b, gen, mixed, Edge{HLC: old, LastOrdinal: expiryChunkRows + 4, Base: &Source{Expiration: now}}),
			rows.PutSource(b, gen, mixed, SourceID{Ordinal: expiryChunkRows + 4}, Source{Weight: 3}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	pinned, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	cursor, err := reclaimTestChunk(t.Context(), rows, db, now, ExpiryCursor{})
	if err != nil || cursor.after == nil || cursor.family != 0 {
		t.Fatalf("first chunk cursor = %+v, err %v", cursor, err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, s *tarp.Snapshot) error {
		watermark, err := VertexExpiryWatermark(t.Context(), s, gen)
		if err != nil || !watermark.Equal(expiredDeadline) {
			t.Fatalf("first committed expiry watermark = %v, %v", watermark, err)
		}
		if _, found, err := rows.GetVertex(s, gen, key(expiryChunkRows-1)); found || err != nil {
			t.Fatalf("last key in first chunk found=%v err=%v", found, err)
		}
		if _, found, err := rows.GetVertex(s, gen, key(expiryChunkRows)); !found || err != nil {
			t.Fatalf("next chunk key found=%v err=%v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := rows.GetVertex(pinned.Snapshot, pinned.Generation, key(0)); !found || err != nil {
		t.Fatalf("pinned pre-chunk row found=%v err=%v", found, err)
	}
	if watermark, err := VertexExpiryWatermark(t.Context(), pinned.Snapshot, pinned.Generation); err != nil || !watermark.IsZero() {
		t.Fatalf("pinned pre-chunk watermark = %v, %v", watermark, err)
	}
	if err := pinned.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	stopped, err := reclaimTestChunk(cancelled, rows, db, now, cursor)
	if !errors.Is(err, context.Canceled) || string(stopped.after) != string(cursor.after) {
		t.Fatalf("cancelled chunk advanced cursor = %+v, err %v", stopped, err)
	}
	if err := db.UpdateRows(t.Context(), func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		return errors.Join(
			rows.PutVertex(b, gen, key(expiryChunkRows), Vertex[string]{Value: "renewed", Expiration: future, HLC: renewed}),
			rows.PutFloor(b, gen, tombstone, Floor{HLC: renewed, Expiration: future}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExisting(t.Context(), dir, hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	db = reopened
	if err := rows.ReclaimExpired(t.Context(), db, now); err != nil {
		t.Fatal(err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, s *tarp.Snapshot) error {
		watermark, err := VertexExpiryWatermark(t.Context(), s, gen)
		if err != nil || !watermark.Equal(expiredDeadline) {
			t.Fatalf("reopened expiry watermark = %v, %v", watermark, err)
		}
		for _, k := range []string{key(0), key(expiryChunkRows + 1), key(expiryChunkRows + 2)} {
			if _, found, err := rows.GetVertex(s, gen, k); found || err != nil {
				t.Fatalf("expired %q found=%v err=%v", k, found, err)
			}
		}
		v, found, err := rows.GetVertex(s, gen, key(expiryChunkRows))
		if err != nil || !found || v.Value != "renewed" {
			t.Fatalf("renewed Vertex = %+v %v %v", v, found, err)
		}
		if _, found, err := rows.GetVertex(s, gen, "permanent"); !found || err != nil {
			t.Fatalf("permanent Vertex found=%v err=%v", found, err)
		}
		for _, test := range []struct {
			floor FloorKey[string]
			clock hlc.Timestamp
		}{
			{FloorKey[string]{Kind: VertexBarrier, Key: key(0)}, renewed},
			{FloorKey[string]{Kind: EdgeBarrier, Tail: pair.Tail, Head: pair.Head}, old},
			{tombstone, renewed},
		} {
			floor, found, err := rows.GetFloor(s, gen, test.floor)
			if err != nil || !found || floor.HLC != test.clock {
				t.Fatalf("floor %+v = %+v %v %v", test.floor, floor, found, err)
			}
		}
		if _, found, err := rows.GetEdge(s, gen, pair); found || err != nil {
			t.Fatalf("dead Edge found=%v err=%v", found, err)
		}
		edge, found, err := rows.GetEdge(s, gen, mixed)
		if err != nil || !found || edge.Base != nil || edge.HLC != old || edge.LastOrdinal != expiryChunkRows+4 {
			t.Fatalf("mixed Edge = %+v %v %v", edge, found, err)
		}
		count := 0
		for source, err := range rows.Sources(t.Context(), s, gen, mixed) {
			if err != nil || source.ID.Ordinal != edge.LastOrdinal || source.Source.Weight != 3 {
				t.Fatalf("mixed Source = %+v %v", source, err)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("mixed bucket retained %d Sources", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	revision := db.head.revision
	if err := rows.ReclaimExpired(t.Context(), db, now); err != nil || db.head.revision != revision {
		t.Fatalf("repeated reclaim changed revision %d to %d, err %v", revision, db.head.revision, err)
	}
}

func TestReclaimExpiredRestartsOnGenerationChange(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir(), hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2000, 0)
	if err := db.UpdateRows(t.Context(), func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		for i := range expiryChunkRows + 1 {
			key := fmt.Sprintf("old-%03d", i)
			if err := rows.PutVertex(b, gen, key, Vertex[string]{Expiration: now}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cursor, err := reclaimTestChunk(t.Context(), rows, db, now, ExpiryCursor{})
	if err != nil || cursor.after == nil {
		t.Fatalf("initial cursor = %+v, err %v", cursor, err)
	}
	stale, err := rows.DiscoverExpiryChunk(t.Context(), db, now, cursor)
	if err != nil || !stale.Pending() {
		t.Fatalf("old generation has no pending expiry row: %+v, err %v", stale, err)
	}
	next, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Abort()
	if err := next.Apply(func(gen uint64, b Writer) error {
		return rows.PutVertex(b, gen, "earlier-key", Vertex[string]{Expiration: now})
	}); err != nil {
		t.Fatal(err)
	}
	if err := next.Publish(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	restarted, effects, err := rows.ReclaimExpiredChunk(t.Context(), db, now, stale)
	if err != nil || restarted.generation != next.ID() || restarted.after != nil || len(effects.Vertices) != 0 {
		t.Fatalf("stale plan reset = %+v, effects %+v, err %v", restarted, effects, err)
	}
	resumed, err := reclaimTestChunk(t.Context(), rows, db, now, restarted)
	if err != nil || resumed.generation != next.ID() {
		t.Fatalf("resumed cursor = %+v, err %v", resumed, err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, s *tarp.Snapshot) error {
		if _, found, err := rows.GetVertex(s, gen, "earlier-key"); found || err != nil {
			t.Fatalf("new generation earlier key found=%v err=%v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func reclaimTestChunk(ctx context.Context, rows *Rows[string, string], db *DB, now time.Time, cursor ExpiryCursor) (ExpiryCursor, error) {
	plan, err := rows.DiscoverExpiryChunk(ctx, db, now, cursor)
	if err != nil {
		return cursor, err
	}
	next, _, err := rows.ReclaimExpiredChunk(ctx, db, now, plan)
	return next, err
}

func TestReclaimExpiredChunkRevalidatesSelectedRows(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, t.TempDir(), hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(3000, 0).UTC()
	future := now.Add(time.Hour)
	old := hlc.Timestamp{WallNs: 1, NodeID: hlc.NodeID{1}}
	newer := hlc.Timestamp{WallNs: 2, NodeID: hlc.NodeID{1}}
	source := Source{Weight: 1, Expiration: now, HLC: old}
	renewed := Source{Weight: 2, Expiration: future, HLC: newer}
	replaced := Source{Weight: 3, Expiration: now, HLC: newer}
	mixed := EdgeKey[string]{Tail: "a", Head: "b"}
	gone := EdgeKey[string]{Tail: "a", Head: "c"}
	grew := EdgeKey[string]{Tail: "a", Head: "d"}
	id := func(n byte) SourceID { return SourceID{ContribID: [24]byte{n}} }
	removeID, renewID, replaceID := id(1), id(2), id(3)
	floor := FloorKey[string]{Kind: ContributionTombstone, Tail: mixed.Tail, Head: mixed.Head, ContribID: [24]byte{4}}
	renewFloor := floor
	renewFloor.ContribID = [24]byte{5}
	update := func(f func(uint64, Writer) error) {
		t.Helper()
		if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error { return f(gen, b) }); err != nil {
			t.Fatal(err)
		}
	}
	update(func(gen uint64, b Writer) error {
		return errors.Join(
			rows.PutVertex(b, gen, "expired", Vertex[string]{Value: "expired", Expiration: now, HLC: old}),
			rows.PutVertex(b, gen, "renewed", Vertex[string]{Value: "old", Expiration: now, HLC: old}),
			rows.PutEdge(b, gen, mixed, Edge{HLC: old, Base: &source}),
			rows.PutEdge(b, gen, gone, Edge{HLC: old, Base: &source, LastOrdinal: 17}),
			rows.PutEdge(b, gen, grew, Edge{HLC: old}),
			rows.ReplaceSource(b, gen, mixed, removeID, nil, &source),
			rows.ReplaceSource(b, gen, mixed, renewID, nil, &source),
			rows.ReplaceSource(b, gen, mixed, replaceID, nil, &source),
			rows.PutFloor(b, gen, floor, Floor{HLC: old, Expiration: now}),
			rows.PutFloor(b, gen, renewFloor, Floor{HLC: old, Expiration: now}),
			rows.MarkSourceOrder(b, gen),
		)
	})
	discover := func(cursor ExpiryCursor) ExpiryPlan {
		t.Helper()
		plan, err := rows.DiscoverExpiryChunk(ctx, db, now, cursor)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	apply := func(plan ExpiryPlan) (ExpiryCursor, ExpiryEffects[string]) {
		t.Helper()
		next, effects, err := rows.ReclaimExpiredChunk(ctx, db, now, plan)
		if err != nil {
			t.Fatal(err)
		}
		return next, effects
	}

	vertexPlan := discover(ExpiryCursor{})
	if !vertexPlan.Pending() || vertexPlan.Next().Generation() != 0 {
		t.Fatalf("Vertex plan can advance without a commit: %+v", vertexPlan.Next())
	}
	revision := db.head.revision
	if _, effects, err := rows.ReclaimExpiredChunk(ctx, db, now.Add(-time.Nanosecond), vertexPlan); err != nil ||
		len(effects.Vertices) != 0 || db.head.revision != revision {
		t.Fatalf("rolled-back clock reclaimed a live row: %+v %v", effects, err)
	}
	update(func(gen uint64, b Writer) error {
		return rows.PutVertex(b, gen, "renewed", Vertex[string]{Value: "new", Expiration: future, HLC: newer})
	})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	stopped, rejected, err := rows.ReclaimExpiredChunk(cancelled, db, now, vertexPlan)
	if !errors.Is(err, context.Canceled) || stopped.Generation() != 0 || len(rejected.Vertices) != 0 {
		t.Fatalf("cancelled plan advanced or emitted effects: %+v %+v %v", stopped, rejected, err)
	}
	cursor, effects := apply(vertexPlan)
	if len(effects.Vertices) != 1 || effects.Vertices[0].Key != "expired" || effects.Vertices[0].Metadata.HLC != old {
		t.Fatalf("Vertex effects = %+v", effects)
	}

	sourcePlan := discover(cursor)
	if !sourcePlan.Pending() || sourcePlan.ScannedSources() != 3 {
		t.Fatalf("Source discovery did not measure three rows: %+v", sourcePlan)
	}
	update(func(gen uint64, b Writer) error {
		return errors.Join(
			rows.ReplaceSource(b, gen, mixed, renewID, &source, &renewed),
			rows.ReplaceSource(b, gen, mixed, replaceID, &source, &replaced),
		)
	})
	cursor, effects = apply(sourcePlan)
	if len(effects.Sources) != 1 || effects.Sources[0].Key != mixed || effects.Sources[0].Source != (SourceEntry{ID: removeID, Source: source}) {
		t.Fatalf("Source effects = %+v", effects)
	}

	edgePlan := discover(cursor)
	if !edgePlan.Pending() || edgePlan.ScannedEdges() != 3 {
		t.Fatalf("Edge discovery did not measure three rows: %+v", edgePlan)
	}
	update(func(gen uint64, b Writer) error {
		return rows.ReplaceSource(b, gen, grew, id(8), nil, &renewed)
	})
	cursor, effects = apply(edgePlan)
	if len(effects.Edges) != 2 || effects.Edges[0].Key != mixed || effects.Edges[0].Removed ||
		effects.Edges[0].Before.Base == nil || effects.Edges[1].Key != gone || !effects.Edges[1].Removed ||
		effects.Edges[1].Before.LastOrdinal != 17 {
		t.Fatalf("Edge effects = %+v", effects)
	}
	for range 2 { // No Vertex or Edge tombstones in this fixture.
		plan := discover(cursor)
		if plan.Pending() {
			t.Fatal("unexpected tombstone before contribution family")
		}
		cursor = plan.Next()
	}
	floorPlan := discover(cursor)
	update(func(gen uint64, b Writer) error {
		return rows.PutFloor(b, gen, renewFloor, Floor{HLC: newer, Expiration: future})
	})
	cursor, effects = apply(floorPlan)
	if !cursor.Done() || len(effects.Floors) != 1 || effects.Floors[0].Key != floor ||
		effects.Floors[0].Floor.HLC != old {
		t.Fatalf("tombstone progress/effects: %+v %+v", cursor, effects)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		vertex, found, err := rows.GetVertex(snap, gen, "renewed")
		if err != nil || !found || vertex.Value != "new" {
			t.Fatalf("renewed Vertex = %+v %t %v", vertex, found, err)
		}
		edge, found, err := rows.GetEdge(snap, gen, mixed)
		if err != nil || !found || edge.Base != nil || edge.HLC != old {
			t.Fatalf("mixed Edge = %+v %t %v", edge, found, err)
		}
		for key, want := range map[EdgeKey[string]]bool{gone: false, grew: true} {
			_, found, err := rows.GetEdge(snap, gen, key)
			if err != nil || found != want {
				t.Fatalf("Edge %+v found=%t err=%v", key, found, err)
			}
		}
		for k, ts := range map[FloorKey[string]]hlc.Timestamp{
			{Kind: VertexBarrier, Key: "expired"}:                 old,
			{Kind: EdgeBarrier, Tail: gone.Tail, Head: gone.Head}: old,
			renewFloor: newer,
		} {
			f, found, err := rows.GetFloor(snap, gen, k)
			if err != nil || !found || f.HLC != ts {
				t.Fatalf("Floor %+v = %+v %t %v", k, f, found, err)
			}
		}
		for id, want := range map[SourceID]Source{renewID: renewed, replaceID: replaced} {
			actual, found, err := rows.GetSource(snap, gen, mixed, id)
			if err != nil || !found || actual != want {
				t.Fatalf("Source %+v = %+v %t %v", id, actual, found, err)
			}
		}
		summary, err := rows.SummarizeEdgeSources(ctx, snap, gen, mixed, edge, now, nil)
		if err != nil || summary.RetainedAdds != 2 || summary.Count != 1 || summary.Weight != renewed.Weight {
			t.Fatalf("remaining ordered Sources = %+v %v", summary, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
