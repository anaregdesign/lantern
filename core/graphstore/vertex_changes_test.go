package graphstore

import (
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestTrackVertexWritesComparesThePinnedPreimage(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	old := Vertex[string]{Value: "old"}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, writer Writer) error {
		return rows.PutVertex(writer, gen, "a", old)
	}); err != nil {
		t.Fatal(err)
	}
	type effect struct {
		key     string
		changed bool
		value   string
	}
	var got []effect
	if err := db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, writer Writer) error {
		tracker := rows.TrackVertexWrites(ctx, snap, writer, func(key string, changed bool, payload []byte) error {
			e := effect{key: key, changed: changed}
			if payload != nil {
				p, err := rows.DecodeVertexPostimage(payload)
				if err != nil {
					return err
				}
				e.value = p.Value
			}
			got = append(got, e)
			return nil
		})
		if err := rows.PutVertex(tracker, gen, "a", Vertex[string]{Value: "new"}); err != nil {
			return err
		}
		if err := rows.PutVertex(tracker, gen, "a", old); err != nil {
			return err
		}
		if err := rows.DeleteVertex(tracker, gen, "a"); err != nil {
			return err
		}
		if err := rows.DeleteVertex(tracker, gen, "missing"); err != nil {
			return err
		}
		return tracker.Err()
	}); err != nil {
		t.Fatal(err)
	}
	want := []effect{
		{key: "a", changed: true, value: "new"},
		{key: "a", changed: false, value: "old"},
		{key: "a", changed: true},
		{key: "missing", changed: false},
	}
	if len(got) != len(want) {
		t.Fatalf("saw effects %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("saw effects %+v, want %+v", got, want)
		}
	}
}
