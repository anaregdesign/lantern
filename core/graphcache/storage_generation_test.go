package graphcache

import (
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func TestStorageChunkStagesVertexPairsAndLargeRow(t *testing.T) {
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
	if err := db.UpdateRows(ctx, func(g uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		return rows.PutVertex(w, g, "a", graphstore.Vertex[string]{Value: "active"})
	}); err != nil {
		t.Fatal(err)
	}

	target, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Abort()
	chunk := storageChunk{ctx: ctx, target: target}
	payload := strings.Repeat("s", 1<<20)
	put := func(key, value string) {
		t.Helper()
		chunk.check(rows.PutVertex(&chunk, target.ID(), key, graphstore.Vertex[string]{Value: value}))
		if chunk.err != nil {
			t.Fatal(chunk.err)
		}
	}
	for _, key := range []string{"a", "b", "c"} {
		put(key, payload)
	}
	// Each encoded value exceeds 1 MiB and has its own projection row.
	if got := len(chunk.records); got != 6 {
		t.Fatalf("buffered rows = %d, want three value/projection pairs", got)
	}
	put("d", payload)
	if got := len(chunk.records); got != 2 {
		t.Fatalf("buffered rows after the fourth Vertex = %d, want one pair", got)
	}
	large := strings.Repeat("l", 5<<20)
	put("large", large)
	if err := chunk.flush(); err != nil {
		t.Fatal(err)
	}

	check := func(snap *tarp.Snapshot, gen uint64, key, want string) {
		t.Helper()
		got, found, err := rows.GetVertex(snap, gen, key)
		if err != nil || !found || got.Value != want {
			t.Fatalf("Vertex %q: length=%d, found=%t, err=%v; want %d matching bytes", key, len(got.Value), found, err, len(want))
		}
		if live, err := rows.HasVertexAt(snap, gen, key, time.Time{}); err != nil || !live {
			t.Fatalf("Vertex projection %q: live=%t, err=%v", key, live, err)
		}
	}
	before, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	if before.Generation == target.ID() || before.Revision != 1 {
		t.Fatalf("staging changed the active cut: generation=%d, revision=%d", before.Generation, before.Revision)
	}
	check(before.Snapshot, before.Generation, "a", "active")
	if _, found, err := rows.GetVertex(before.Snapshot, before.Generation, "large"); err != nil || found {
		t.Fatalf("hidden Vertex visible before publication: found=%t, err=%v", found, err)
	}

	if err := target.Publish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	after, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if after.Generation != target.ID() || after.Revision != before.Revision+1 {
		t.Fatalf("unexpected published cut: generation=%d, revision=%d", after.Generation, after.Revision)
	}
	for _, key := range []string{"a", "b", "c", "d"} {
		check(after.Snapshot, after.Generation, key, payload)
	}
	check(after.Snapshot, after.Generation, "large", large)
	check(before.Snapshot, before.Generation, "a", "active")
}
