package graphcache

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStoredContributionDeleteStagesOnlyTargetedRows(t *testing.T) {
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
	stamp := func(n int64) hlc.Timestamp { return hlc.Timestamp{WallNs: n, NodeID: hlc.NodeID{1}} }
	identity := func(i uint32) ContribID {
		var id ContribID
		binary.BigEndian.PutUint32(id[:4], i)
		return id
	}
	const siblings = 2048
	now := time.Unix(1000, 0)
	deadline := now.Add(time.Second)
	pair := graphstore.EdgeKey[string]{Tail: "tail", Head: "head"}
	emptyPair := graphstore.EdgeKey[string]{Tail: "tail", Head: "last"}
	requested := EdgeContributionKey[string]{Tail: pair.Tail, Head: pair.Head, ContribID: identity(1)}
	missing := EdgeContributionKey[string]{Tail: pair.Tail, Head: pair.Head, ContribID: identity(siblings + 2)}
	last := EdgeContributionKey[string]{Tail: emptyPair.Tail, Head: emptyPair.Head, ContribID: identity(1)}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		if err := errors.Join(
			rows.PutVertex(w, gen, pair.Tail, graphstore.Vertex[string]{Value: "kept"}),
			rows.PutVertex(w, gen, pair.Head, graphstore.Vertex[string]{}),
			rows.PutVertex(w, gen, emptyPair.Head, graphstore.Vertex[string]{}),
			rows.PutEdge(w, gen, pair, graphstore.Edge{}),
			rows.PutEdge(w, gen, emptyPair, graphstore.Edge{HLC: stamp(1)}),
			rows.ReplaceSource(w, gen, pair, graphstore.SourceID{ContribID: requested.ContribID}, nil, &graphstore.Source{Weight: 9, HLC: stamp(2)}),
			rows.ReplaceSource(w, gen, emptyPair, graphstore.SourceID{ContribID: last.ContribID}, nil, &graphstore.Source{Weight: 2, HLC: stamp(2)}),
		); err != nil {
			return err
		}
		for i := uint32(2); i <= siblings+1; i++ {
			if err := rows.ReplaceSource(w, gen, pair, graphstore.SourceID{ContribID: identity(i)}, nil, &graphstore.Source{
				Weight: 1, Expiration: deadline, HLC: stamp(3),
			}); err != nil {
				return err
			}
		}
		return rows.MarkSourceOrder(w, gen)
	}); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureIncomingEdgeIndex(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureTopologyStats(ctx); err != nil {
		t.Fatal(err)
	}
	oldView, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oldView.Close() })
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { return now }
	if err := c.RestoreStorage(ctx, rows, oldView); err != nil {
		t.Fatal(err)
	}
	now = deadline
	before := c.edges.bucket(pair.Tail, pair.Head)
	before.load = func(*time.Time) (storedWeight, error) {
		t.Error("exact contribution Delete loaded the entire bucket")
		return storedWeight{}, errors.New("full bucket load")
	}
	keys := []EdgeContributionKey[string]{requested, missing, requested, last}
	storageKeys := StorageKeys[string]{Contributions: keys}
	prepare := func() *EdgeContributionDeleteTransaction[string, string] {
		t.Helper()
		tx, err := c.PrepareStoredEdgeContributionDelete(ctx, keys, stamp(4), deadline.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(tx.Abort)
		props := tx.Result()
		if !slices.Equal(props.Existed, []bool{true, false, false, true}) || len(props.Accepted) != len(keys) {
			t.Fatalf("stored contribution Delete = %+v", props)
		}
		if got := len(tx.stage.stored.buckets[EdgeKey[string](pair)].removed); got != 1 {
			t.Fatalf("stored plan retained %d source rows for one identity", got)
		}
		tx.Apply()
		return tx
	}
	failed := prepare()
	rejected := errors.New("reject before publication")
	if err := db.UpdateRows(ctx, func(g uint64, s *tarp.Snapshot, w graphstore.Writer) error {
		if err := failed.StoreRows(ctx, rows, s, w, g, storageKeys); err != nil {
			return err
		}
		return rejected
	}); !errors.Is(err, rejected) {
		t.Fatalf("staged rejection = %v", err)
	}
	failed.Abort()
	if c.edges.bucket(pair.Tail, pair.Head) != before || len(c.edgeContributionTombstones) != 0 {
		t.Fatal("aborted contribution Delete changed the cache")
	}
	committed := prepare()
	if err := db.UpdateRows(ctx, func(g uint64, s *tarp.Snapshot, w graphstore.Writer) error {
		return committed.StoreRows(ctx, rows, s, w, g, storageKeys)
	}); err != nil {
		t.Fatal(err)
	}
	c.StoragePublished(storageKeys)
	committed.Commit()
	kept := c.edges.bucket(pair.Tail, pair.Head)
	if kept == nil || !kept.cold || len(kept.values) != 0 || kept.storedCount != siblings || kept.storedSum != siblings || !kept.storedExpiry.Equal(deadline) {
		t.Fatalf("unexpected retained bucket: %+v", kept)
	}
	if c.edges.bucket(emptyPair.Tail, emptyPair.Head) != nil {
		t.Fatal("empty bucket remains in the cache")
	}
	if _, present, err := rows.GetSource(oldView.Snapshot, oldView.Generation, pair, graphstore.SourceID{ContribID: requested.ContribID}); err != nil || !present {
		t.Fatalf("old reader lost its source: %v, %v", present, err)
	}
	if err := db.WithCommittedSnapshot(func(g uint64, s *tarp.Snapshot) error {
		if _, present, err := rows.GetSource(s, g, pair, graphstore.SourceID{ContribID: requested.ContribID}); err != nil || present {
			t.Fatalf("deleted source on disk: %v, %v", present, err)
		}
		for _, key := range []EdgeContributionKey[string]{requested, missing, last} {
			floor, present, err := rows.GetFloor(s, g, contributionDeleteFloor(key))
			if err != nil || !present || floor.HLC != stamp(4) {
				t.Fatalf("durable contribution floor: %+v, %v, %v", floor, present, err)
			}
		}
		if _, present, err := rows.GetEdge(s, g, emptyPair); err != nil || present {
			t.Fatalf("empty header on disk: %v, %v", present, err)
		}
		if stats, ready, err := rows.GetTopologyStats(s, g); err != nil || !ready || stats.Edges != 1 || stats.NonemptyTails != 1 {
			t.Fatalf("committed topology: %+v, ready=%v, err=%v", stats, ready, err)
		}
		floor, present, err := rows.GetFloor(s, g, graphstore.FloorKey[string]{Kind: graphstore.EdgeBarrier, Tail: emptyPair.Tail, Head: emptyPair.Head})
		if err != nil || !present || floor.HLC != stamp(1) {
			t.Fatalf("last source lost its Put floor: %+v, %v, %v", floor, present, err)
		}
		oldTime, err := rows.EdgeAt(ctx, s, g, pair, deadline.Add(-time.Nanosecond))
		if err != nil || !oldTime.Found || oldTime.Weight != siblings {
			t.Fatalf("unrelated source was physically reclaimed: %+v, %v", oldTime, err)
		}
		atDeadline, err := rows.EdgeAt(ctx, s, g, pair, deadline)
		if err != nil || atDeadline.Found {
			t.Fatalf("expired siblings appeared live: %+v, %v", atDeadline, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
