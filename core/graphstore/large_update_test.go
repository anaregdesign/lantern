package graphstore

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestOversizedUpdateSurvivesReopenAsOneRevision(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := Open(ctx, dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	key := func(gen uint64, family byte, index uint32) []byte {
		return binary.BigEndian.AppendUint32(append(ServicePrefix(gen), family), index)
	}
	err = db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w Writer) error {
		w.Put(key(gen, 1, 0), []byte("kept"))
		w.Put(key(gen, 1, 1), []byte("deleted"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	err = db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w Writer) error {
		w.Delete(key(gen, 1, 1))
		for i := range uint32(tarp.MaxWriteBatchEntries) {
			w.Put(key(gen, 2, i), binary.BigEndian.AppendUint32(nil, i))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := old.Snapshot.Get(key(old.Generation, 1, 1)); err != nil || string(v) != "deleted" {
		t.Fatal("original cut changed", err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenExisting(ctx, dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if view.Revision != old.Revision+1 || view.Generation == old.Generation {
		t.Fatal("invalid published revision or generation", view.Revision, view.Generation)
	}
	if v, err := view.Snapshot.Get(key(view.Generation, 1, 0)); err != nil || string(v) != "kept" {
		t.Fatal("lost unchanged row", err)
	}
	if _, err := view.Snapshot.Get(key(view.Generation, 1, 1)); !errors.Is(err, tarp.ErrNotFound) {
		t.Fatal("deleted row survived", err)
	}
	n := 0
	for v, err := range view.Snapshot.ScanPrefix(ctx, append(ServicePrefix(view.Generation), 2)) {
		if err != nil || len(v.Value) != 4 || binary.BigEndian.Uint32(v.Value) != uint32(n) {
			t.Fatal("invalid row", n, err)
		}
		n++
	}
	if n != tarp.MaxWriteBatchEntries {
		t.Fatal("incomplete update", n)
	}
}
