package graphstore

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestFoldedEdgesMergesOrderedSources(t *testing.T) {
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 0).UTC()
	later := now.Add(time.Hour)
	k := EdgeKey[string]{"a\x00", "a"}
	next := EdgeKey[string]{"a\x00", "東"}
	missing := EdgeKey[string]{"a\x00z", "dangling"}
	var b tarp.WriteBatch
	var pairCancel = EdgeKey[string]{"a\x00c", "d"}
	if err := errors.Join(
		rows.PutVertex(&b, 1, k.Tail, Vertex[string]{}),
		rows.PutVertex(&b, 1, k.Head, Vertex[string]{}),
		rows.PutEdge(&b, 1, EdgeKey[string]{"a", "outside"}, Edge{Base: &Source{Weight: 1}}),
		rows.PutEdge(&b, 1, k, Edge{Base: &Source{Weight: 1 << 50}, LastOrdinal: 2}),
		rows.PutEdge(&b, 1, next, Edge{Base: &Source{Weight: float32(math.Inf(-1)), DerivedAggregate: true}}),
		rows.PutEdge(&b, 1, pairCancel, Edge{Base: &Source{Weight: 1}, LastOrdinal: 2}),
		rows.PutSource(&b, 1, pairCancel, SourceID{Ordinal: 2}, Source{Weight: -1}),
		rows.PutEdge(&b, 1, missing, Edge{LastOrdinal: 1}),
		rows.PutSource(&b, 1, missing, SourceID{Ordinal: 1}, Source{Weight: 5, Expiration: later}),
		rows.PutSource(&b, 1, k, SourceID{Ordinal: 1}, Source{Weight: -1 << 50}),
		rows.PutSource(&b, 1, k, SourceID{Ordinal: 2}, Source{Weight: 1, Expiration: now}),
		rows.PutSource(&b, 1, k, SourceID{ContribID: [24]byte{4}}, Source{HLC: hlc.Timestamp{WallNs: 3}, Weight: 9, Expiration: later}),
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	var found []FoldedEdge[string]
	for row, err := range rows.FoldedEdges(t.Context(), snap, 1, "a\x00", now) {
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, row)
	}
	if len(found) != 3 || found[0].Key != k || found[0].Detail.Weight != 9 || !found[0].Detail.Expiration.Equal(later) ||
		found[1].Key != next || !math.IsInf(float64(found[1].Detail.Weight), -1) ||
		found[2].Key != missing || found[2].Detail.Weight != 5 {
		t.Fatalf("folded rows: %+v", found)
	}
	point, err := rows.EdgeAt(t.Context(), snap, 1, k, now)
	if err != nil || point != found[0].Detail {
		t.Fatalf("point/scan mismatch: %+v, %v", point, err)
	}
	var orphan tarp.WriteBatch
	if err := rows.PutSource(&orphan, 1, EdgeKey[string]{"0", "x"}, SourceID{Ordinal: 1}, Source{Weight: 7}); err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(&orphan); err != nil {
		t.Fatal(err)
	}
	latest, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer latest.Close()
	var scanErr error
	for _, err := range rows.FoldedEdges(t.Context(), latest, 1, "", now) {
		if err != nil {
			scanErr = err
		}
	}
	if !errors.Is(scanErr, ErrInvalidRow) {
		t.Fatalf("unowned source error = %v", scanErr)
	}
}
