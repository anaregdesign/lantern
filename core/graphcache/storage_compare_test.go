package graphcache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestMatchesStorageGenerationDoesNotCollectExpiredState(t *testing.T) {
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
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	ts := hlc.Timestamp{WallNs: 17}
	key := graphstore.EdgeKey[string]{Tail: "x", Head: "y"}
	tomb := graphstore.FloorKey[string]{Kind: graphstore.EdgeTombstone, Tail: "dead", Head: "edge"}
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return errors.Join(rows.PutVertex(w, g, "x", graphstore.Vertex[string]{Value: "old", Expiration: expiry, HLC: ts}),
			rows.PutVertex(w, g, "y", graphstore.Vertex[string]{}),
			rows.PutEdge(w, g, key, graphstore.Edge{LastOrdinal: 1}),
			rows.PutSource(w, g, key, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: 3, Expiration: expiry}),
			rows.PutFloor(w, g, tomb, graphstore.Floor{HLC: ts, Expiration: expiry}))
	}); err != nil {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { return now }
	if err := c.RestoreStorage(ctx, rows, view); err != nil {
		t.Fatal(err)
	}
	now = expiry.Add(time.Hour)
	equal := func(want bool) {
		t.Helper()
		got, err := c.MatchesStorageGeneration(ctx, rows, view)
		if err != nil || got != want {
			t.Fatalf("matches = %t, %v; want %t", got, err, want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if ok, err := c.MatchesStorageGeneration(canceled, rows, view); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled proof = %t, %v", ok, err)
	}
	equal(true)
	if _, _, found, cold := c.vertices.PeekResident("x"); !found || !cold || c.vertexHLC["x"] != ts || len(c.vertexCausalBarriers) != 0 {
		t.Fatal("comparison mutated the expired Vertex")
	}
	w := c.edges.bucket("x", "y")
	if !w.cold || w.values != nil || w.storedCount != 1 || c.edgeTombstones[EdgeKey[string]{"dead", "edge"}].ts != ts {
		t.Fatal("comparison mutated the expired Source or tombstone")
	}
	w.storedSum++
	equal(false)
	w.storedSum--
	// A partial Snapshot can change a value without changing the row count,
	// physical key, timestamp or deadline.
	c.vertices.UpsertWithExpiration("x", "partial", expiry)
	equal(false)
	c.vertices.UpsertWithExpiration("x", "old", expiry)
	equal(true)
	w.cold = false
	w.values = []weightValue{{ordinal: 1, value: -3, expiration: expiry}}
	equal(false)
	w.values[0].value = 3
	equal(true)
	delete(c.edgeTombstones, EdgeKey[string]{"dead", "edge"})
	equal(false)
	c.edgeTombstones[EdgeKey[string]{"dead", "edge"}] = tombstoneEntry{ts: ts, expiration: expiry}
	// A cold descriptor can certify only its own active durable cut.
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, b graphstore.Writer) error {
		b.Put(append(graphstore.ServicePrefix(g), 42), []byte("new revision"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	equal(false)
}
