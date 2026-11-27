package graphstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestEnsureSourceOrderRetriesAndRejectsDivergence(t *testing.T) {
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
	source := Source{Weight: 2, Expiration: time.Unix(101, 0), HLC: hlc.Timestamp{WallNs: 2}}
	id := SourceID{ContribID: [24]byte{3}}
	var order []byte
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, b Writer) error {
		pair, err := rows.edgeTuple(gen, sourceFamily, key)
		if err != nil {
			return err
		}
		order, err = sourceOrderKey(pair, source.HLC, id)
		if err != nil {
			return err
		}
		// Simulate a committed working row before the interrupted build's marker.
		b.Put(order, nil)
		return errors.Join(rows.PutEdge(b, gen, key, Edge{LastOrdinal: 1}), rows.PutSource(b, gen, key, id, source),
			rows.PutSource(b, gen, key, SourceID{Ordinal: 1}, Source{Weight: 4}))
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := rows.EnsureSourceOrder(cancelled, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled build: %v", err)
	}
	checkReady := func(want bool) {
		t.Helper()
		cut, err := db.Read()
		if err != nil {
			t.Fatal(err)
		}
		defer cut.Close()
		got, err := hasSourceOrder(cut.Snapshot, cut.Generation)
		if err != nil || got != want {
			t.Fatalf("marker = %v, %v, want %v", got, err, want)
		}
	}
	checkReady(false)
	if err := rows.EnsureSourceOrder(ctx, db); err != nil {
		t.Fatal(err)
	}
	checkReady(true)
	if err := rows.ReclaimExpired(ctx, db, source.Expiration); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureSourceOrder(ctx, db); err != nil {
		t.Fatalf("expiry did not co-delete ordering key: %v", err)
	}
	if err := db.UpdateRows(ctx, func(_ uint64, _ *tarp.Snapshot, b Writer) error {
		b.Put(order, nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureSourceOrder(ctx, db); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("orphan ordering key verified: %v", err)
	}
}
