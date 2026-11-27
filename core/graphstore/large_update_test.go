package graphstore

import (
	"bytes"
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
	lo, hi := tarp.PrefixRange(append(ServicePrefix(view.Generation), 2))
	for v, err := range view.Snapshot.ScanRange(ctx, lo, hi) {
		if err != nil || len(v.Value) != 4 || binary.BigEndian.Uint32(v.Value) != uint32(n) {
			t.Fatal("invalid row", n, err)
		}
		n++
	}
	if n != tarp.MaxWriteBatchEntries {
		t.Fatal("incomplete update", n)
	}
}

func TestUpdateRowsSegmentBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		coalesce   bool
		secondSize int
		generation uint64
	}{
		{"ExactFit", false, 64, 1},
		{"OneByteOver", false, 65, 2},
		{"CoalescedExactFit", true, 64, 1},
		{"CoalescedOneByteOver", true, 65, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			node := hlc.NodeID{1}
			// Two 10-byte keys with 63/64-byte values and the application
			// head fit this segment exactly.
			raw, err := tarp.Open(dir, tarp.Options{MaxRecordBytes: 256, TargetSegmentBytes: 400})
			if err != nil {
				t.Fatal(err)
			}
			db, err := openDB(raw, node, true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			old, err := db.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			first := bytes.Repeat([]byte("a"), 63)
			second := bytes.Repeat([]byte("b"), tc.secondSize)
			err = db.UpdateRows(t.Context(), func(gen uint64, _ *tarp.Snapshot, w Writer) error {
				a := append(ServicePrefix(gen), 1)
				if tc.coalesce {
					// Force re-planning by input count, then fold to the same two keys.
					for range tarp.MaxWriteBatchEntries {
						w.Delete(a)
					}
				}
				w.Put(a, first)
				w.Put(append(ServicePrefix(gen), 2), second)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := old.Snapshot.Get(append(ServicePrefix(old.Generation), 1)); !errors.Is(err, tarp.ErrNotFound) {
				t.Fatalf("old snapshot contains updated row: %v", err)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenExisting(t.Context(), dir, node)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			view, err := reopened.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			if view.Revision != 1 || view.Generation != tc.generation {
				t.Fatalf("reopened revision %d generation %d", view.Revision, view.Generation)
			}
			for i, want := range [][]byte{first, second} {
				got, err := view.Snapshot.Get(append(ServicePrefix(view.Generation), byte(i+1)))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("row %d after reopen: %x, %v", i, got, err)
				}
			}
		})
	}
}

func TestUpdateRowsCoalescesAbsentDeletesBeforeGenerationCopy(t *testing.T) {
	ctx := t.Context()
	node := hlc.NodeID{1}
	dir := t.TempDir()
	raw, err := tarp.Open(dir, tarp.Options{MaxRecordBytes: 256, TargetSegmentBytes: 400})
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDB(raw, node, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key := func(generation uint64, id byte) []byte {
		return append(ServicePrefix(generation), id)
	}
	if err := db.UpdateRows(ctx, func(generation uint64, _ *tarp.Snapshot, b Writer) error {
		b.Put(key(generation, 0), []byte("change"))
		b.Put(key(generation, 1), []byte("keep"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	preparations := 0
	if err := db.UpdateRows(ctx, func(generation uint64, _ *tarp.Snapshot, b Writer) error {
		preparations++
		b.Put(key(generation, 0), []byte("changed"))
		for i := byte(2); i < 20; i++ {
			b.Delete(key(generation, i))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if preparations != 2 || after.Generation != old.Generation || after.Revision != old.Revision+1 {
		t.Fatalf("unnecessary replacement or wrong commit: preparations=%d generation=%d revision=%d", preparations, after.Generation, after.Revision)
	}
	for _, check := range []struct {
		view *ReadView
		id   byte
		want string
	}{
		{old, 0, "change"}, {after, 0, "changed"}, {after, 1, "keep"},
	} {
		value, err := check.view.Snapshot.Get(key(check.view.Generation, check.id))
		if err != nil || string(value) != check.want {
			t.Fatalf("id %d: got %q, %v; want %q", check.id, value, err, check.want)
		}
	}
}
