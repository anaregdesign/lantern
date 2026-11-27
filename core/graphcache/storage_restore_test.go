package graphcache

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestRestoreStorageRejectsUnownedSources(t *testing.T) {
	for _, tail := range []string{"a", "m", "z"} {
		t.Run(tail, func(t *testing.T) {
			db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
			if err != nil {
				t.Fatal(err)
			}
			key := graphstore.EdgeKey[string]{Tail: "m", Head: "h"}
			if err := db.UpdateRows(t.Context(), func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
				if err := rows.PutEdge(b, g, key, graphstore.Edge{LastOrdinal: 1}); err != nil {
					return err
				}
				// a/z have no header. m has a header but its local ordinal
				// exceeds the high-water mark even though the Add expired.
				sourceKey := graphstore.EdgeKey[string]{Tail: tail, Head: "h"}
				return rows.PutSource(b, g, sourceKey, graphstore.SourceID{Ordinal: 2}, graphstore.Source{Expiration: time.Unix(1, 0)})
			}); err != nil {
				t.Fatal(err)
			}
			view, err := db.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			c := NewGraphCacheWithStaging[string, string](time.Hour)
			if err := c.RestoreStorage(t.Context(), rows, view); !errors.Is(err, graphstore.ErrInvalidRow) {
				t.Fatalf("restore = %v, want invalid row", err)
			}
		})
	}
}

func TestRestoreStorageStreamingRetainsColdSummaries(t *testing.T) {
	ctx := t.Context()
	db, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := graphstore.NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	soon, later := now.Add(time.Hour), now.Add(2*time.Hour)
	zero := graphstore.EdgeKey[string]{Tail: "tail", Head: "zero"}
	nan := graphstore.EdgeKey[string]{Tail: "tail", Head: "nan"}
	dead := graphstore.EdgeKey[string]{Tail: "tail", Head: "dead"}
	floor := hlc.Timestamp{WallNs: 14, NodeID: hlc.NodeID{2}}
	const largeOrdinal = 700
	id := graphstore.SourceID{ContribID: [24]byte{2}}
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		for _, vertex := range []string{"tail", "zero", "nan", "dead"} {
			if err := rows.PutVertex(b, g, vertex, graphstore.Vertex[string]{Value: vertex}); err != nil {
				return err
			}
		}
		return errors.Join(
			rows.PutEdge(b, g, zero, graphstore.Edge{HLC: floor, LastOrdinal: largeOrdinal,
				Base: &graphstore.Source{Weight: 99, Expiration: now, HLC: floor}}),
			rows.PutSource(b, g, zero, id, graphstore.Source{Weight: 5, Expiration: later, HLC: hlc.Timestamp{WallNs: 17}}),
			rows.PutSource(b, g, zero, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: -5, Expiration: soon}),
			rows.PutEdge(b, g, nan, graphstore.Edge{Base: &graphstore.Source{Weight: math.Float32frombits(0x7fc12345)}}),
			rows.PutEdge(b, g, dead, graphstore.Edge{HLC: floor, LastOrdinal: 1,
				Base: &graphstore.Source{Weight: 1, Expiration: now, HLC: floor}}),
			rows.PutSource(b, g, dead, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: 1, Expiration: now}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	for first := uint64(2); first <= largeOrdinal; first += 128 {
		if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
			for ordinal := first; ordinal <= min(largeOrdinal, first+127); ordinal++ {
				if err := rows.PutSource(b, g, zero, graphstore.SourceID{Ordinal: ordinal}, graphstore.Source{}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	legacyView, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer legacyView.Close()
	if err := rows.BuildSourceOrder(ctx, db); err != nil {
		t.Fatal(err)
	}
	orderedView, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer orderedView.Close()
	if orderedView.Generation != legacyView.Generation || orderedView.Revision != legacyView.Revision {
		t.Fatal("backfill advanced the application cut")
	}
	restore := func(v *graphstore.ReadView) *GraphCache[string, string] {
		t.Helper()
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.applicationClock = func() time.Time { return now }
		if err := c.RestoreStorage(ctx, rows, v); err != nil {
			t.Fatal(err)
		}
		return c
	}
	legacy, ordered := restore(legacyView), restore(orderedView)
	if legacy.storage.sourceOrderVerified || !ordered.storage.sourceOrderVerified || !ordered.storage.recoveryExpired {
		t.Fatal("legacy and ordered recovery selected the wrong path or missed expiry")
	}
	for _, k := range []graphstore.EdgeKey[string]{zero, nan} {
		want, got := legacy.edges.bucket(k.Tail, k.Head), ordered.edges.bucket(k.Tail, k.Head)
		if want == nil || got == nil || !got.cold || got.values != nil || got.load == nil ||
			got.storedCount != want.storedCount || got.storedExplicit != want.storedExplicit ||
			got.storedExpiry != want.storedExpiry || got.storedLatest != want.storedLatest || got.lastOrdinal != want.lastOrdinal ||
			got.lastHLC != want.lastHLC || math.Float32bits(got.storedSum) != math.Float32bits(want.storedSum) {
			t.Fatalf("%+v: streaming summary %+v, legacy %+v", k, got, want)
		}
	}
	mixed := ordered.edges.bucket(zero.Tail, zero.Head)
	if mixed.storedSum != 0 || !mixed.storedExplicit || mixed.storedCount != largeOrdinal+1 || mixed.storedExpiry != soon || mixed.storedLatest != later {
		t.Fatalf("mixed summary %+v", mixed)
	}
	if got := ordered.edges.bucket(nan.Tail, nan.Head); math.Float32bits(got.storedSum) != 0x7fc12345 {
		t.Fatalf("NaN payload changed: %08x", math.Float32bits(got.storedSum))
	}
	if ordered.edges.bucket(dead.Tail, dead.Head) != nil || ordered.edgeCausalBarriers[EdgeKey[string]{dead.Tail, dead.Head}] != floor {
		t.Fatal("expired bucket did not become a causal barrier")
	}
	if err := ordered.CompleteStorageRecovery(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureSourceOrder(ctx, db); err != nil {
		t.Fatalf("expiry invalidated the Source projection: %v", err)
	}
	if sum, _, found := mixed.snapshotAt(soon); !found || sum != 5 {
		t.Fatalf("cold loader TTL transition = %v, %v", sum, found)
	}
}
