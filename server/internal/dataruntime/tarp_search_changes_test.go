package dataruntime

import (
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
)

func TestTarpSearchChangesFollowAtomicGraphCut(t *testing.T) {
	ctx := t.Context()
	db, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	baseline := tarpSearchCut{epoch: 17, first: 1, digest: sha256.Sum256([]byte("base"))}
	var next tarpSearchCut
	keys := []string{"data:b", "data:a", "data:b", "data:" + strings.Repeat("a", 40_000), "data:" + strings.Repeat("b", 40_000)}
	before, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = before.Close() })

	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, batch graphstore.Writer) error {
		batch.Put(tarpServiceKey(gen, 99), []byte("graph-effect"))
		var err error
		next, err = stageTarpSearchChanges(batch, gen, baseline, sha256.Sum256([]byte("accepted")), keys)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := readTarpSearchCut(ctx, before.Snapshot, before.Generation); !errors.Is(err, tarp.ErrNotFound) {
		t.Fatalf("old cut saw new Search state: %v", err)
	}
	read := func() *graphstore.ReadView {
		t.Helper()
		view, err := db.Read()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = view.Close() })
		return view
	}
	after := read()
	cut, err := readTarpSearchCut(ctx, after.Snapshot, after.Generation)
	if err != nil || cut != next || cut.last != 1 || after.Revision != before.Revision+1 {
		t.Fatalf("unexpected search cut %v, %v", cut, err)
	}
	if effect, err := after.Snapshot.Get(tarpServiceKey(after.Generation, 99)); err != nil || string(effect) != "graph-effect" {
		t.Fatalf("missing accepted effect: %q, %v", effect, err)
	}
	var seen []string
	if err := visitTarpSearchChanges(ctx, after.Snapshot, after.Generation, cut, 0, baseline.digest, 100_000, 4, func(seq uint64, key string) error {
		if seq != 1 {
			t.Fatalf("wrong accepted sequence: %d", seq)
		}
		seen = append(seen, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(keys)
	slices.Sort(want)
	if !slices.Equal(seen, slices.Compact(want)) {
		t.Fatalf("unexpected changed identities: %d", len(seen))
	}

	testVisit := func(view *graphstore.ReadView, digest [sha256.Size]byte, maxBytes int) error {
		return visitTarpSearchChanges(ctx, view.Snapshot, view.Generation, cut, 0, digest, maxBytes, 4, func(uint64, string) error { return nil })
	}
	if err := testVisit(after, baseline.digest, 64<<10); !errors.Is(err, errTarpSearchCut) {
		t.Fatalf("expected byte budget failure: %v", err)
	}
	if err := testVisit(after, [sha256.Size]byte{2}, 100_000); !errors.Is(err, errTarpSearchCut) {
		t.Fatalf("accepted unrelated source cut: %v", err)
	}
	if err := db.UpdateRows(ctx, func(gen uint64, _ *tarp.Snapshot, batch graphstore.Writer) error {
		batch.Delete(tarpSearchChangeKey(gen, 1, 2))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := testVisit(read(), baseline.digest, 100_000); !errors.Is(err, errTarpSearchCut) {
		t.Fatalf("accepted a missing change chunk: %v", err)
	}
	if err := testVisit(after, baseline.digest, 100_000); err != nil {
		t.Fatalf("pinned previous cut became invalid: %v", err)
	}
}

func TestTarpSearchChangesTrackAcceptedGraphPostimages(t *testing.T) {
	ctx := t.Context()
	runtime, rpc := openTarpTestRuntime(t, t.TempDir(), 16)
	runtime.tarp.trackSearch = true
	view := func() *graphstore.ReadView {
		t.Helper()
		v, err := runtime.GraphStore().Read()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = v.Close() })
		return v
	}
	if _, err := rpc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "seed"}}); err != nil {
		t.Fatal(err)
	}
	first := view()
	base, err := readTarpSearchCut(ctx, first.Snapshot, first.Generation)
	if err != nil || base.last != 1 {
		t.Fatalf("expected first indexed mutation, got %v %v", base, err)
	}
	if _, err := rpc.AddEdge(ctx, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "seed", Head: "implicit", Weight: 1}}); err != nil {
		t.Fatal(err)
	}
	current := view()
	cut, err := readTarpSearchCut(ctx, current.Snapshot, current.Generation)
	if err != nil || cut.last != 2 {
		t.Fatalf("expected implicit endpoint to advance the source, got %v %v", cut, err)
	}
	var affected []string
	if err := visitTarpSearchChanges(ctx, current.Snapshot, current.Generation, cut, base.last, base.digest, 1024, 8, func(_ uint64, key string) error {
		affected = append(affected, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(affected, []string{"implicit"}) {
		t.Fatalf("edge touched incorrect index identities: %v", affected)
	}
	if _, err := rpc.AddEdge(ctx, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "seed", Head: "implicit", Weight: 2}}); err != nil {
		t.Fatal(err)
	}
	recent := view()
	if after, err := readTarpSearchCut(ctx, recent.Snapshot, recent.Generation); err != nil || after != cut {
		t.Fatalf("pure Edge update advanced the Search source: %v, %v", after, err)
	}
}
