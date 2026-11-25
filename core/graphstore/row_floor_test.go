package graphstore

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestRowFloorFamiliesPreserveDeadlines(t *testing.T) {
	r, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var batch tarp.WriteBatch
	sample := hlc.Timestamp{WallNs: 902}
	deadline := time.Date(2401, 1, 1, 1, 2, 3, 4, time.UTC)
	records := map[FloorKey[string]]Floor{
		{Kind: VertexBarrier, Key: "a"}:                                             {HLC: sample},
		{Kind: EdgeBarrier, Tail: "a", Head: "b"}:                                   {HLC: sample},
		{Kind: VertexTombstone, Key: "v"}:                                           {sample, deadline},
		{Kind: EdgeTombstone, Tail: "a", Head: "b"}:                                 {sample, deadline},
		{Kind: ContributionTombstone, Tail: "a", Head: "b", ContribID: [24]byte{1}}: {sample, deadline},
	}
	for k, f := range records {
		if err := r.PutFloor(&batch, 1, k, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.PutFloor(&batch, 1, FloorKey[string]{Kind: EdgeBarrier}, Floor{sample, deadline}); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("expiring barrier err=%v", err)
	}
	if err := db.Apply(&batch); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	for row, err := range r.Floors(t.Context(), snap, 1) {
		if err != nil {
			t.Fatal(err)
		}
		want, ok := records[row.Key]
		if !ok || row.Floor != want {
			t.Fatalf("unexpected row %#v", row)
		}
		read, ok, err := r.GetFloor(snap, 1, row.Key)
		if err != nil || !ok || read != want {
			t.Fatalf("read floor %#v %v %v", read, ok, err)
		}
		delete(records, row.Key)
	}
	if len(records) != 0 {
		t.Fatalf("missing %v", records)
	}
}
