package graphcache

import (
	"errors"
	"slices"
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

func TestWriteStorageGenerationStagesIncomingEdgeIndex(t *testing.T) {
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
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		if err := rows.PutEdge(w, gen, graphstore.EdgeKey[string]{Tail: "old", Head: "東"}, graphstore.Edge{}); err != nil {
			return err
		}
		return rows.MarkIncomingEdgeIndex(w, gen)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()

	target, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Abort()
	const head = "東"
	candidate := NewGraphCache[string, string](time.Hour)
	candidate.PutEdgeWithExpiration("z", head, 3, time.Time{})
	candidate.PutEdgeWithExpiration("a\x00", head, 2, time.Time{})
	if err := candidate.WriteStorageGeneration(ctx, rows, target); err != nil {
		t.Fatal(err)
	}
	check := func(snap *tarp.Snapshot, gen uint64, want ...string) {
		t.Helper()
		if ready, err := rows.HasIncomingEdgeIndex(snap, gen); !ready || err != nil {
			t.Fatalf("generation %d incoming readiness = %v, %v", gen, ready, err)
		}
		var got []string
		for key, err := range rows.IncomingEdges(ctx, snap, gen, head) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, key.Tail)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("incoming %q in generation %d = %q, want %q", head, gen, got, want)
		}
	}
	if err := target.Publish(ctx, func(active, staged uint64, snap *tarp.Snapshot) error {
		check(snap, active, "old")
		check(snap, staged, "a\x00", "z")
		if stats, ready, err := rows.GetTopologyStats(snap, staged); err != nil || !ready || stats.Edges != 2 || stats.NonemptyTails != 2 {
			t.Fatalf("staged topology = %+v, ready=%t, err=%v", stats, ready, err)
		}
		if degree, err := rows.GetTopologyDegree(snap, staged, head); err != nil || degree.In != 2 || degree.Out != 0 {
			t.Fatalf("staged head degree = %+v, err=%v", degree, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check(before.Snapshot, before.Generation, "old")
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		check(snap, gen, "a\x00", "z")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteStorageGenerationStreamsColdContributions(t *testing.T) {
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
	now := time.Unix(4000, 0)
	deadline := now.Add(time.Second)
	ts := hlc.Timestamp{WallNs: 5, NodeID: hlc.NodeID{1}}
	key := graphstore.EdgeKey[string]{Tail: "tail\x00", Head: "head"}
	id := graphstore.SourceID{ContribID: [24]byte{9}}
	const survivors = 520
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, w graphstore.Writer) error {
		for i := range survivors {
			local := graphstore.SourceID{Ordinal: uint64(i + 2)}
			if err := rows.PutSource(w, gen, key, local, graphstore.Source{Weight: 2}); err != nil {
				return err
			}
		}
		return errors.Join(
			rows.PutVertex(w, gen, key.Tail, graphstore.Vertex[string]{Value: "tail"}),
			rows.PutVertex(w, gen, key.Head, graphstore.Vertex[string]{Value: "head"}),
			rows.PutEdge(w, gen, key, graphstore.Edge{HLC: ts, LastOrdinal: survivors + 3, Base: &graphstore.Source{Weight: 1, HLC: ts, Expiration: deadline}}),
			rows.PutSource(w, gen, key, graphstore.SourceID{Ordinal: 1}, graphstore.Source{Weight: 10, Expiration: deadline}),
			rows.PutSource(w, gen, key, id, graphstore.Source{Weight: 7, HLC: hlc.Timestamp{WallNs: 8, NodeID: hlc.NodeID{1}}}),
		)
	}); err != nil {
		t.Fatal(err)
	}
	old, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.applicationClock = func() time.Time { return now }
	if err := c.RestoreStorage(ctx, rows, old); err != nil {
		t.Fatal(err)
	}
	bucket := c.edges.bucket(key.Tail, key.Head)
	if bucket == nil || !bucket.cold {
		t.Fatal("restored bucket must be cold")
	}
	bucket.load = func(*time.Time) (storedWeight, error) {
		t.Fatal("a cold Snapshot bucket was materialized")
		return storedWeight{}, nil
	}
	target, err := db.BeginGeneration()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Abort()
	now = deadline
	if err := c.WriteStorageGeneration(ctx, rows, target); err != nil {
		t.Fatal(err)
	}
	if err := target.Publish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		edge, found, err := rows.GetEdge(snap, gen, key)
		if err != nil || !found || edge.Base != nil || edge.HLC != ts || edge.LastOrdinal != survivors+3 {
			t.Fatalf("published Edge = %+v %v %v", edge, found, err)
		}
		var count int
		for source, err := range rows.Sources(ctx, snap, gen, key) {
			if err != nil || source.ID.Ordinal == 1 {
				t.Fatalf("published Source = %+v %v", source, err)
			}
			count++
		}
		if count != survivors+1 {
			t.Fatalf("published %d Sources, want %d", count, survivors+1)
		}
		explicit, found, err := rows.GetSource(snap, gen, key, id)
		if err != nil || !found || explicit.Weight != 7 || explicit.HLC.WallNs != 8 {
			t.Fatalf("explicit Contribution = %+v %v %v", explicit, found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if edge, found, err := rows.GetEdge(old.Snapshot, old.Generation, key); err != nil || !found || edge.Base == nil {
		t.Fatalf("pinned source lost its base: %+v %v %v", edge, found, err)
	}
}
