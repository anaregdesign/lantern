package dataruntime

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

func TestTarpDiskSearchPairsWithPinnedGraphSource(t *testing.T) {
	ctx := t.Context()
	now := time.Unix(1_800_000_000, 0)
	graph, err := graphstore.Open(ctx, t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = graph.Close() })
	rows, err := diskRows()
	if err != nil {
		t.Fatal(err)
	}
	index, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	baseline := tarpSearchCut{epoch: 1, first: 1, digest: sha256.Sum256([]byte("first source"))}
	write := func(value string) {
		t.Helper()
		if err := graph.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, b graphstore.Writer) error {
			cut, err := readTarpSearchCut(ctx, snap, gen)
			if errors.Is(err, tarp.ErrNotFound) {
				cut, err = baseline, nil
			}
			if err != nil {
				return err
			}
			v := &pb.Vertex{Key: "test", Value: &pb.Vertex_String_{String_: value}}
			if err := rows.PutVertex(b, gen, "test", graphstore.Vertex[*pb.Vertex]{Value: v}); err != nil {
				return err
			}
			_, err = stageTarpSearchChanges(b, gen, cut, sha256.Sum256([]byte(value)), []string{"test"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	capture := func() tarpDiskSearchInput {
		t.Helper()
		view, err := graph.Read()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = view.Close() })
		project := func(_ string, vertex *pb.Vertex) search.Document {
			return search.Text(vertex.GetString_())
		}
		bound, err := captureTarpDiskSearchInput(ctx, view, "test corpus", "text/v1", now, project)
		if err != nil {
			t.Fatal(err)
		}
		return bound
	}
	write("old")
	old := capture()
	// A newer graph commit does not move the input view.
	write("intermediate")
	write("new")
	newer := capture()
	var postimages []search.DiskDocument
	for doc, err := range newer.changes(ctx, old.source, 1024, 8) {
		if err != nil {
			t.Fatal(err)
		}
		postimages = append(postimages, doc)
	}
	if len(postimages) != 1 || postimages[0].Key != "test" || postimages[0].Document.String() != "new" {
		t.Fatalf("changed postimages do not match current Graph: %v", postimages)
	}
	analyzer := search.NewScriptAwareAnalyzer()
	if err := old.build(ctx, index, 1, analyzer); err != nil {
		t.Fatal(err)
	}
	if _, err := search.OpenDiskIndex(ctx, index, 1, newer.source, analyzer, nil); !errors.Is(err, search.ErrDiskIndex) {
		t.Fatalf("old index certified against newer graph cut: %v", err)
	}
	disk, err := search.OpenDiskIndex(ctx, index, 1, old.source, analyzer, nil)
	if err != nil {
		t.Fatal(err)
	}
	for term, wanted := range map[string]int{"old": 1, "new": 0} {
		results, _, err := disk.SearchAt(ctx, term, 5, nil, search.MatchOptions{}, false, search.Budget{}, now)
		if err != nil || len(results) != wanted {
			t.Fatalf("%s: %v, %v", term, results, err)
		}
	}
}

func TestTarpDiskSearchCaptureUsesCommittedChangesAndAdmittedKeys(t *testing.T) {
	ctx := t.Context()
	runtime, executor := openTarpTestRuntime(t, t.TempDir(), 16)
	runtime.tarp.trackSearch = true
	put := func(key, value string) {
		t.Helper()
		_, err := executor.PutVertex(ctx, &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: value}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	put("docs:allowed", "old")
	put("docs:denied", "hidden")
	open, err := runtime.GraphStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = open.Close() })
	project := func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) }
	source, err := captureTarpDiskSearchInput(ctx, open, "docs", "text/v1", time.Now(), project)
	if err != nil {
		t.Fatal(err)
	}
	index, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	analyzer := search.NewScriptAwareAnalyzer()
	if err := source.build(ctx, index, 1, analyzer); err != nil {
		t.Fatal(err)
	}
	if err := selectTarpSearchBase(ctx, runtime.GraphStore(), index, tarpSearchBaseRef{ID: 1, Source: source.source}, analyzer, nil, 1<<20, 32); err != nil {
		t.Fatal(err)
	}
	selected, err := runtime.GraphStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = selected.Close() })
	base, err := openSelectedTarpSearchBase(ctx, selected, index, analyzer, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime.tarp.diskSearch = &tarpDiskSearchReader{project: project, maxChangeBytes: 1 << 20, maxChangeKeys: 32}
	runtime.tarp.diskSearch.base.Store(base)
	put("docs:allowed", "replacement")
	put("docs:denied", "replacement")
	admitted, err := graphcache.NewQueryView([]graphcache.KeyRange{{Lower: "docs:allowed", Upper: "docs:allowed\x00"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx = graphcache.WithQueryView(ctx, admitted)
	reader := searchReader{service: executor}
	spec := query.Spec{Limit: 4, Prefix: "docs:", Query: "replacement", Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}
	got, err := reader.Capture(ctx, spec)
	if err != nil || got.BackendError != nil || len(got.Hits) != 1 ||
		got.Hits[0].GetKey() != "docs:allowed" || got.Hits[0].GetVertex().GetString_() != "replacement" {
		t.Fatalf("capture did not read the same committed, permitted postimage: %+v, %v", got, err)
	}
	spec.Query = "old"
	got, err = reader.Capture(ctx, spec)
	if err != nil || got.BackendError != nil || len(got.Hits) != 0 {
		t.Fatalf("stale search result: %+v, %v", got, err)
	}
	if err := runtime.tarp.diskSearch.advance(ctx, runtime.GraphStore(), index, 1, analyzer, nil, search.Budget{}); err != nil {
		t.Fatal(err)
	}
	advanced, err := runtime.GraphStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	defer advanced.Close()
	ref, err := readTarpSearchBaseRef(ctx, advanced)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := readTarpSearchCut(ctx, advanced.Snapshot, advanced.Generation)
	if err != nil || ref.Source.Sequence != durable.last || durable.first != durable.last+1 {
		t.Fatalf("wrong durable search base/change boundary: %v %v %v", ref, durable, err)
	}
	reopened, err := openSelectedTarpSearchBase(ctx, advanced, index, analyzer, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime.tarp.diskSearch.base.Store(reopened)
	spec.Query = "replacement"
	got, err = reader.Capture(ctx, spec)
	if err != nil || got.BackendError != nil || len(got.Hits) != 1 || got.Hits[0].GetKey() != "docs:allowed" {
		t.Fatalf("materialized search result diverged after retiring deltas: %+v, %v", got, err)
	}

	// Replacing the authoritative graph with a peer cut does not carry
	// forward the receiver's old Search epoch.
	finish, err := executor.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	peer := hlc.NodeID{2}
	stamp := hlc.Timestamp{NodeID: peer, WallNs: time.Now().UnixNano()}
	runtime.graph.PutVertexWithExpirationHLC("docs:peer", &pb.Vertex{Key: "docs:peer"}, time.Time{}, stamp)
	if err := executor.ApplySnapshotWatermarks(map[string]uint64{"02000000000000000000000000000000": 1}, stamp); err != nil {
		_ = finish(false)
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	got, err = reader.Capture(ctx, spec)
	if err != nil || !errors.Is(got.BackendError, search.ErrIndexIncomplete) || len(got.Hits) != 0 {
		t.Fatalf("replaced graph served an old Search epoch: %+v, %v", got, err)
	}
	gotPeer, err := executor.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "docs:peer"})
	if err != nil || gotPeer.GetVertex().GetKey() != "docs:peer" {
		t.Fatalf("derived failure affected authoritative graph readiness: %+v, %v", gotPeer, err)
	}

}
