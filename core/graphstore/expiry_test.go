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
	old := hlc.Timestamp{WallNs: 11, NodeID: hlc.NodeID{1}}
	renewed := hlc.Timestamp{WallNs: 12, NodeID: hlc.NodeID{1}}
	future := now.Add(time.Hour)
	key := func(i int) string { return fmt.Sprintf("expired:\x00%03d", i) }
	pair := EdgeKey[string]{Tail: "gone", Head: "pair"}
	mixed := EdgeKey[string]{Tail: "mixed", Head: "pair"}
	tombstone := FloorKey[string]{Kind: ContributionTombstone, Tail: mixed.Tail, Head: mixed.Head, ContribID: [24]byte{11}}
	if err := db.UpdateRows(t.Context(), func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		for i := range expiryChunkRows + 3 {
			if err := rows.PutVertex(b, gen, key(i), Vertex[string]{Value: "expired", HLC: old, Expiration: now}); err != nil {
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
	cursor, err := rows.reclaimExpiryNext(t.Context(), db, now, expiryCursor{})
	if err != nil || cursor.after == nil || cursor.family != 0 {
		t.Fatalf("first chunk cursor = %+v, err %v", cursor, err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, s *tarp.Snapshot) error {
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
	if err := pinned.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	stopped, err := rows.reclaimExpiryNext(cancelled, db, now, cursor)
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
	cursor, err := rows.reclaimExpiryNext(t.Context(), db, now, expiryCursor{})
	if err != nil || cursor.after == nil {
		t.Fatalf("initial cursor = %+v, err %v", cursor, err)
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
	resumed, err := rows.reclaimExpiryNext(t.Context(), db, now, cursor)
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
