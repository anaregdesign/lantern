package graphstore

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestRowsPersistExactGraphAndDifferentiateFailures(t *testing.T) {
	rows, err := NewRows[string, float32](tarp.StringCodec{}, tarp.Float32Codec{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := tarp.Open(dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Unix(-44, 567).UTC()
	ts := hlc.Timestamp{WallNs: -2, Logical: 9, NodeID: hlc.NodeID{3}}
	weight := math.Float32frombits(0x7fc00123)
	key := EdgeKey[string]{"a\x00", "東"}
	base := Source{Weight: weight, HLC: ts, Expiration: deadline, DerivedAggregate: true}
	var b tarp.WriteBatch
	for _, err := range []error{
		rows.PutVertex(&b, 1, key.Tail, Vertex[float32]{weight, deadline, ts}),
		rows.PutEdge(&b, 1, key, Edge{HLC: ts, LastOrdinal: 2, Base: &base}),
		rows.PutSource(&b, 1, key, SourceID{Ordinal: 1}, Source{Weight: 1}),
		rows.PutSource(&b, 1, key, SourceID{Ordinal: 2}, Source{Weight: 2}),
		rows.PutSource(&b, 1, key, SourceID{ContribID: [24]byte{1}}, Source{Weight: 3, HLC: ts}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = tarp.OpenExisting(t.Context(), dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snap, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	vertex, found, err := rows.GetVertex(snap, 1, key.Tail)
	if err != nil || !found || vertex.HLC != ts || !vertex.Expiration.Equal(deadline) || math.Float32bits(vertex.Value) != math.Float32bits(weight) {
		t.Fatalf("vertex %#v %v %v", vertex, found, err)
	}
	edge, found, err := rows.GetEdge(snap, 1, key)
	if err != nil || !found || edge.Base == nil || !edge.Base.DerivedAggregate || edge.LastOrdinal != 2 || edge.HLC != ts || math.Float32bits(edge.Base.Weight) != math.Float32bits(weight) {
		t.Fatalf("edge %#v %v %v", edge, found, err)
	}
	var count int
	for entry, err := range rows.Sources(t.Context(), snap, 1, key) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if entry.Source.Weight != float32(count) {
			t.Fatalf("source %#v", entry)
		}
	}
	if count != 3 {
		t.Fatalf("sources=%d", count)
	}
	if _, found, err := rows.GetVertex(snap, 2, key.Tail); err != nil || found {
		t.Fatalf("other generation returned %v %v", found, err)
	}
	if err := rows.PutSource(&b, 1, key, SourceID{}, Source{}); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("invalid source %v", err)
	}
	if err := rows.PutSource(&b, 1, key, SourceID{Ordinal: 1}, base); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("invalid aggregate %v", err)
	}

	b.Reset()
	if err := rows.DeleteSources(t.Context(), snap, &b, 1, key); err != nil {
		t.Fatal(err)
	}
	physical, err := rows.vertexKey(1, vertexFamily, key.Tail)
	if err != nil {
		t.Fatal(err)
	}
	b.Put(physical, []byte("malformed"))
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	next, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := rows.GetVertex(next, 1, key.Tail); found || !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("malformed row returned %v %v", found, err)
	}
	if _, found, err := rows.GetSource(next, 1, key, SourceID{Ordinal: 1}); found || err != nil {
		t.Fatalf("removed source returned %v %v", found, err)
	}
	if _, found, err := rows.GetSource(snap, 1, key, SourceID{Ordinal: 1}); !found || err != nil {
		t.Fatalf("pinned source returned %v %v", found, err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := rows.GetVertex(next, 1, key.Tail); found || !errors.Is(err, tarp.ErrClosed) {
		t.Fatalf("closed row returned %v %v", found, err)
	}
}
