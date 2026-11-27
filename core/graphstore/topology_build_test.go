package graphstore

import (
	"context"
	"errors"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestTopologyBuildCertifiesExistingCutAndClearsPartialRows(t *testing.T) {
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
	const a, b, c = "", "a\x00", "東"
	if err := db.Update(func(gen uint64, _ *tarp.Snapshot, w *tarp.WriteBatch) error {
		stale, err := topologyKey(gen, "stale")
		if err != nil {
			return err
		}
		w.Put(stale, encodeTopologyCounts(2, 4))
		return errors.Join(
			rows.PutEdge(w, gen, EdgeKey[string]{Tail: a, Head: b}, Edge{}),
			rows.PutEdge(w, gen, EdgeKey[string]{Tail: c, Head: c}, Edge{}),
			rows.PutEdge(w, gen, EdgeKey[string]{Tail: c, Head: b}, Edge{}),
			rows.MarkIncomingEdgeIndex(w, gen),
		)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.EnsureTopologyStats(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled build returned %v", err)
	}
	if err := db.EnsureTopologyStats(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if before.Generation != after.Generation || before.Revision != after.Revision {
		t.Fatalf("changed application cut: %d/%d -> %d/%d", before.Generation, before.Revision, after.Generation, after.Revision)
	}
	if ready, err := rows.HasTopologyStats(before.Snapshot, before.Generation); ready || err != nil {
		t.Fatalf("previous cut certification = %t, %v", ready, err)
	}
	if stats, ready, err := rows.GetTopologyStats(after.Snapshot, after.Generation); !ready || err != nil || stats != (TopologyStats{2, 3}) {
		t.Fatalf("stats = %+v, ready=%t, err=%v", stats, ready, err)
	}
	for key, expected := range map[string]TopologyDegree{a: {Out: 1}, b: {In: 2}, c: {In: 1, Out: 2}, "stale": {}} {
		if actual, err := rows.GetTopologyDegree(after.Snapshot, after.Generation, key); err != nil || actual != expected {
			t.Fatalf("degree %q = %+v, err=%v; want %+v", key, actual, err, expected)
		}
	}
	if err := db.EnsureTopologyStats(ctx); err != nil {
		t.Fatal(err)
	}
	repeated, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer repeated.Close()
	if repeated.Snapshot.Sequence() != after.Snapshot.Sequence() {
		t.Fatal("a repeated build wrote new rows")
	}
}

func TestTopologyBuildRejectsUnequalProjectionCount(t *testing.T) {
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
		primary, err := rows.edgeKey(gen, edgeFamily, key)
		if err != nil {
			return err
		}
		w.Put(primary, encodeEdge(Edge{}))
		return rows.MarkIncomingEdgeIndex(w, gen)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureTopologyStats(ctx); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("mismatched projections returned %v", err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		if ready, err := rows.HasTopologyStats(snap, gen); ready || err != nil {
			t.Fatalf("incorrectly certified topology: %t, %v", ready, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
