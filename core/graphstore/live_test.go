package graphstore

import (
	"math"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestEdgeAtCanonicalCausalOrderAndExpiration(t *testing.T) {
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	k := EdgeKey[string]{"v", "v"}
	now := time.Unix(0, 0).UTC()
	later := now.Add(time.Hour)
	var b tarp.WriteBatch
	if err := rows.PutVertex(&b, 1, "v", Vertex[string]{Expiration: later}); err != nil {
		t.Fatal(err)
	}
	if err := rows.PutEdge(&b, 1, k, Edge{}); err != nil {
		t.Fatal(err)
	}
	// ID ordering gives ((1<<50)+9)-(1<<50) == 0, but causal order
	// gives ((1<<50)-(1<<50))+9 == 9.
	for _, row := range []struct {
		id     byte
		ts     int64
		weight float32
		expiry time.Time
	}{
		{1, 1, 1 << 50, time.Time{}}, {2, 3, 9, later}, {3, 2, -(1 << 50), time.Time{}}, {4, 4, 8, now},
	} {
		id := SourceID{ContribID: [24]byte{row.id}}
		if err := rows.PutSource(&b, 1, k, id, Source{Weight: row.weight, HLC: hlc.Timestamp{WallNs: row.ts}, Expiration: row.expiry}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Apply(&b); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	d, err := rows.EdgeAt(t.Context(), snap, 1, k, now)
	if err != nil || !d.Found || math.Float32bits(d.Weight) != math.Float32bits(9) || !d.Expiration.Equal(later) {
		t.Fatalf("edge=%+v err=%v", d, err)
	}
	d, err = rows.EdgeAt(t.Context(), snap, 1, k, later)
	if err != nil || d.Found {
		t.Fatalf("expired vertex exposed edge=%+v err=%v", d, err)
	}
}
