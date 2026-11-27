package graphstore

import (
	"errors"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestTopologyStatsCoCommitFinalBucketTransitions(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureIncomingEdgeIndex(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureTopologyStats(ctx); err != nil {
		t.Fatal(err)
	}
	const a, b, c = "a\x00", "東", "unreadable"
	loop := EdgeKey[string]{Tail: a, Head: a}
	ab := EdgeKey[string]{Tail: a, Head: b}
	cb := EdgeKey[string]{Tail: c, Head: b}
	ba := EdgeKey[string]{Tail: b, Head: a}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w Writer) error {
		return errors.Join(
			rows.PutEdge(w, gen, loop, Edge{}),
			rows.PutEdge(w, gen, ab, Edge{Base: &Source{Weight: 1}}),
			rows.PutEdge(w, gen, cb, Edge{}),
			rows.PutEdge(w, gen, ab, Edge{Base: &Source{Weight: -1}}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	first, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	check := func(snap *tarp.Snapshot, gen uint64, stats TopologyStats, want map[string]TopologyDegree) {
		t.Helper()
		if actual, ready, err := rows.GetTopologyStats(snap, gen); !ready || err != nil || actual != stats {
			t.Fatalf("stats = %+v, ready=%t, err=%v; want %+v", actual, ready, err, stats)
		}
		for key, want := range want {
			if actual, err := rows.GetTopologyDegree(snap, gen, key); err != nil || actual != want {
				t.Fatalf("degree %q = %+v, err=%v; want %+v", key, actual, err, want)
			}
		}
	}
	before := map[string]TopologyDegree{a: {In: 1, Out: 2}, b: {In: 2}, c: {Out: 1}, "absent": {}}
	check(first.Snapshot, first.Generation, TopologyStats{NonemptyTails: 2, Edges: 3}, before)

	reject := errors.New("prepared operation rejected")
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w Writer) error {
		if err := rows.DeleteEdge(w, gen, ab); err != nil {
			return err
		}
		return reject
	}); err != reject {
		t.Fatal(err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		check(snap, gen, TopologyStats{NonemptyTails: 2, Edges: 3}, before)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ephemeral := EdgeKey[string]{Tail: "once", Head: a}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w Writer) error {
		return errors.Join(
			rows.DeleteEdge(w, gen, loop),
			rows.PutEdge(w, gen, loop, Edge{}),
			rows.PutEdge(w, gen, ephemeral, Edge{}),
			rows.DeleteEdge(w, gen, ephemeral),
			rows.DeleteEdge(w, gen, cb),
			rows.DeleteEdge(w, gen, cb),
			rows.PutEdge(w, gen, ba, Edge{}),
			rows.UpdateEdge(w, gen, ab, Edge{LastOrdinal: 4}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		check(snap, gen, TopologyStats{NonemptyTails: 2, Edges: 3}, map[string]TopologyDegree{
			a: {In: 2, Out: 2}, b: {In: 1, Out: 1}, c: {}, "once": {},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check(first.Snapshot, first.Generation, TopologyStats{NonemptyTails: 2, Edges: 3}, before)
}

func TestTopologyStatsRejectsCounterUnderflow(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	key := EdgeKey[string]{Tail: "tail", Head: "head"}
	if err := db.Update(func(gen uint64, _ *tarp.Snapshot, w *tarp.WriteBatch) error {
		if err := rows.PutEdge(w, gen, key, Edge{}); err != nil {
			return err
		}
		totals, err := familyPrefix(gen, topologyFamily)
		if err != nil {
			return err
		}
		w.Put(totals, encodeTopologyCounts(1, 1))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w Writer) error {
		return rows.DeleteEdge(w, gen, key)
	})
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("Delete against malformed counters returned %v", err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		if _, found, err := rows.GetEdge(snap, gen, key); err != nil || !found {
			t.Fatalf("Edge after rejected write: found=%t, err=%v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
