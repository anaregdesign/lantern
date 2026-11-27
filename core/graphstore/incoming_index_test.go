package graphstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestIncomingEdgeIndexOrdersHeadsAndCoStagesMutations(t *testing.T) {
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(t.Context(), t.TempDir(), hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ordered := []EdgeKey[string]{
		{Tail: "x", Head: ""},
		{Tail: "x", Head: "a"},
		{Tail: "a", Head: "a\x00"},
		{Tail: "z\x00", Head: "a\x00"},
		{Tail: "x", Head: "a\x00b"},
		{Tail: "x", Head: "東"},
	}
	if err := db.Update(func(gen uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		for i := len(ordered) - 1; i >= 0; i-- {
			if err := rows.PutEdge(b, gen, ordered[i], Edge{}); err != nil {
				return err
			}
		}
		return rows.MarkIncomingEdgeIndex(b, gen)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	for head, want := range map[string][]EdgeKey[string]{
		"": ordered[:1], "a": ordered[1:2], "a\x00": ordered[2:4], "a\x00b": ordered[4:5], "missing": nil, "東": ordered[5:],
	} {
		readIncoming(t, rows, before, head, want)
	}

	updated, deleted := ordered[2], ordered[3]
	if err := db.Update(func(gen uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		if err := rows.UpdateEdge(b, gen, updated, Edge{LastOrdinal: 9}); err != nil {
			return err
		}
		if b.Len() != 1 {
			t.Fatalf("header-only update wrote %d rows", b.Len())
		}
		return rows.DeleteEdge(b, gen, deleted)
	}); err != nil {
		t.Fatal(err)
	}
	after, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	readIncoming(t, rows, after, "a\x00", []EdgeKey[string]{updated})
	readIncoming(t, rows, before, "a\x00", ordered[2:4])
	edge, found, err := rows.GetEdge(after.Snapshot, after.Generation, updated)
	if err != nil || !found || edge.LastOrdinal != 9 {
		t.Fatalf("updated Edge = %+v, %v, %v", edge, found, err)
	}
	if _, found, err := rows.GetEdge(after.Snapshot, after.Generation, deleted); err != nil || found {
		t.Fatalf("removed Edge found = %v, err = %v", found, err)
	}
	for _, err := range rows.VerifiedVertexBytes(t.Context(), after.Snapshot, after.Generation) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func readIncoming(t *testing.T, rows *Rows[string, string], view *ReadView, head string, want []EdgeKey[string]) {
	t.Helper()
	var got []EdgeKey[string]
	for k, err := range rows.IncomingEdges(t.Context(), view.Snapshot, view.Generation, head) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, k)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("incoming %q = %q, want %q", head, got, want)
	}
}

func TestEnsureIncomingEdgeIndexRetriesInterruptedBackfill(t *testing.T) {
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := Open(t.Context(), dir, hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()

	var malformed []byte
	const edgeCount = 513
	want := make([]EdgeKey[string], 0, edgeCount)
	if err := db.Update(func(gen uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		for i := range edgeCount {
			k := EdgeKey[string]{Tail: fmt.Sprintf("tail-%04d\x00", i), Head: "東"}
			key, err := rows.edgeKey(gen, edgeFamily, k)
			if err != nil {
				return err
			}
			b.Put(key, encodeEdge(Edge{}))
			want = append(want, k)
		}
		prefix, err := familyPrefix(gen, edgeFamily)
		if err != nil {
			return err
		}
		// Fail after valid keys have forced a chunk to commit.
		malformed = append(prefix, 0xff)
		b.Put(malformed, encodeEdge(Edge{}))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureIncomingEdgeIndex(t.Context(), db); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("first backfill returned %v", err)
	}
	partial, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	if ready, err := rows.HasIncomingEdgeIndex(partial.Snapshot, partial.Generation); err != nil || ready {
		t.Fatalf("partial readiness = %v, %v", ready, err)
	}
	partialCount := 0
	for _, err := range rows.IncomingEdges(t.Context(), partial.Snapshot, partial.Generation, "東") {
		if err != nil {
			t.Fatal(err)
		}
		partialCount++
	}
	if partialCount == 0 || partialCount >= edgeCount {
		t.Fatalf("expected some incomplete persisted rows after interruption, got %d", partialCount)
	}
	if err := partial.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(_ uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		b.Delete(malformed)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	appRevision, generation := db.head.revision, db.head.active
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenExisting(t.Context(), dir, hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := rows.EnsureIncomingEdgeIndex(cancelled, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backfill returned %v", err)
	}
	if err := rows.EnsureIncomingEdgeIndex(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	complete, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer complete.Close()
	if complete.Generation != generation || complete.Revision != appRevision {
		t.Fatalf("backfill changed application cut = %d/%d", complete.Generation, complete.Revision)
	}
	if ready, err := rows.HasIncomingEdgeIndex(complete.Snapshot, complete.Generation); err != nil || !ready {
		t.Fatalf("complete readiness = %v, %v", ready, err)
	}
	readIncoming(t, rows, complete, "東", want)
	if err := rows.EnsureIncomingEdgeIndex(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	again, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Snapshot.Sequence() != complete.Snapshot.Sequence() {
		t.Fatal("completed backfill wrote new storage records")
	}
}

func TestIncomingEdgeIndexRejectsNonemptyMarker(t *testing.T) {
	rows, err := NewRows[string, string](tarp.StringCodec{}, tarp.StringCodec{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(t.Context(), t.TempDir(), hlc.NodeID{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(func(gen uint64, _ *tarp.Snapshot, b *tarp.WriteBatch) error {
		key, err := familyPrefix(gen, incomingEdgeFamily)
		if err == nil {
			b.Put(key, []byte{1})
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := rows.EnsureIncomingEdgeIndex(t.Context(), db); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("malformed marker returned %v", err)
	}
}
