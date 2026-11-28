package dataruntime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

func TestOwnedTarpSearchRefreshRebuildsAfterNativeGeneration(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	graphDir, indexDir := filepath.Join(dir, "graph"), filepath.Join(dir, "search")
	runtime, executor := openTarpTestRuntime(t, graphDir, 16)
	project := func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) }
	config := tarpSearchStartup{
		corpus: "test", fingerprint: "search/v1", project: project,
		analyzer:       search.NewAnalyzer(nil, search.WhitespaceTokenizer{}, nil),
		maxChangeBytes: 1 << 20, maxChangeKeys: 64,
	}
	derived, err := tarp.Open(indexDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.startOwnedDiskSearch(ctx, derived, config, 25*time.Millisecond, 4*time.Second); err != nil {
		_ = derived.Close()
		t.Fatal(err)
	}
	old := runtime.tarp.diskSearch.base.Load()
	if _, err := executor.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "docs:local", Value: &pb.Vertex_String_{String_: "local"},
	}}); err != nil {
		t.Fatal(err)
	}
	awaitSearchRefresh(t, func() bool {
		fresh := runtime.tarp.diskSearch.base.Load()
		return fresh != nil && fresh.BaseID() == old.BaseID() && fresh.Source().Sequence == old.Source().Sequence+1
	})
	beforeSnapshot := runtime.tarp.diskSearch.base.Load()
	finish, err := executor.BeginSnapshotInstallChecked(ctx, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	peer := hlc.NodeID{2}
	stamp := hlc.Timestamp{NodeID: peer, WallNs: time.Now().UnixNano()}
	runtime.graph.PutVertexWithExpirationHLC("docs:peer", &pb.Vertex{
		Key: "docs:peer", Value: &pb.Vertex_String_{String_: "native"},
	}, time.Time{}, stamp)
	if err := executor.ApplySnapshotWatermarks(map[string]uint64{"02000000000000000000000000000000": 1}, stamp); err != nil {
		_ = finish(false)
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	awaitSearchRefresh(t, func() bool {
		fresh := runtime.tarp.diskSearch.base.Load()
		return fresh != nil && fresh.BaseID() != beforeSnapshot.BaseID() && fresh.Source().Epoch != beforeSnapshot.Source().Epoch
	})
	fresh := runtime.tarp.diskSearch.base.Load()
	awaitSearchRefresh(t, func() bool {
		_, err := search.OpenDiskIndex(ctx, derived, beforeSnapshot.BaseID(), beforeSnapshot.Source(), config.analyzer, nil)
		return errors.Is(err, search.ErrDiskIndex)
	})
	spec := query.Spec{Query: "native", Limit: 5, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}
	got, err := (searchReader{service: executor}).Capture(ctx, spec)
	if err != nil || got.BackendError != nil || len(got.Hits) != 1 || got.Hits[0].GetKey() != "docs:peer" ||
		got.Hits[0].GetVertex().GetString_() != "native" {
		t.Fatalf("rebuilt Search lost the authoritative native cut: %+v %v", got, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.tarp.searchRefresh.done:
	default:
		t.Fatal("Graph runtime closed before joining the Search worker")
	}
	// The derived Tarp LOCK was released by the runtime, not by the test.
	reopened, err := tarp.OpenExisting(ctx, indexDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runtime, executor = openTarpTestRuntime(t, graphDir, 16)
	id, err := runtime.tarp.openDiskSearch(ctx, reopened, config)
	if err != nil || id != fresh.BaseID() || !sameTarpDiskSource(runtime.tarp.diskSearch.base.Load().Source(), fresh.Source()) {
		t.Fatalf("restart did not reopen the selected new epoch: %d %v", id, err)
	}
	got, err = (searchReader{service: executor}).Capture(ctx, spec)
	if err != nil || got.BackendError != nil || len(got.Hits) != 1 || got.Hits[0].GetKey() != "docs:peer" {
		t.Fatalf("restart could not serve the native cut: %+v %v", got, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	// An empty replacement sidecar models loss of derived files. Graph's
	// certified cut remains intact even though it names a missing base.
	runtime, executor = openTarpTestRuntime(t, graphDir, 16)
	replacement, err := tarp.Open(filepath.Join(dir, "replacement-search"), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.startOwnedDiskSearch(ctx, replacement, config, time.Hour, 4*time.Second); err != nil {
		_ = replacement.Close()
		t.Fatal(err)
	}
	restored := runtime.tarp.diskSearch.base.Load().Source()
	if restored.Epoch != fresh.Source().Epoch || restored.Sequence != fresh.Source().Sequence || restored.Digest != fresh.Source().Digest {
		t.Fatalf("lost-sidecar recovery detached Graph authority: %+v", restored)
	}
	got, err = (searchReader{service: executor}).Capture(ctx, spec)
	if err != nil || got.BackendError != nil || len(got.Hits) != 1 || got.Hits[0].GetKey() != "docs:peer" {
		t.Fatalf("replacement sidecar did not reconstruct Graph: %+v %v", got, err)
	}

}

func TestTarpSearchRefreshResolvesPendingSelectorBeforeRetirement(t *testing.T) {
	ctx := t.Context()
	runtime, executor := openTarpTestRuntime(t, t.TempDir(), 16)
	derived, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer derived.Close()
	config := tarpSearchStartup{
		corpus: "test", fingerprint: "v1",
		project:        func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) },
		analyzer:       search.NewAnalyzer(nil, search.WhitespaceTokenizer{}, nil),
		maxChangeBytes: 1 << 20, maxChangeKeys: 64,
	}
	id, err := runtime.tarp.openDiskSearch(ctx, derived, config)
	if err != nil {
		t.Fatal(err)
	}
	old := runtime.tarp.diskSearch.base.Load()
	if _, err := executor.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "docs:new", Value: &pb.Vertex_String_{String_: "selected"},
	}}); err != nil {
		t.Fatal(err)
	}
	cut, err := runtime.GraphStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	defer cut.Close()
	input, err := captureTarpDiskSearchInput(ctx, cut, config.corpus, config.fingerprint, time.Now(), config.project)
	if err != nil {
		t.Fatal(err)
	}
	newID, _, err := runtime.tarp.buildTarpSearchBase(ctx, input, derived, config)
	if err != nil {
		t.Fatal(err)
	}
	worker := tarpSearchRefresh{relay: runtime.tarp, derived: derived, config: config, active: id, pending: newID}
	// This is the durable state after a selector commit whose outcome was
	// not delivered to the worker. Resolve from Graph before deleting rows.
	if err := worker.refreshWithin(ctx, ctx); err != nil {
		t.Fatal(err)
	}
	if worker.active != newID || worker.pending != id || runtime.tarp.diskSearch.base.Load().BaseID() != newID {
		t.Fatal("pending resolution did not adopt Graph's proven selected base")
	}
	if err := worker.refreshWithin(ctx, ctx); err != nil {
		t.Fatal(err)
	}
	if worker.pending != 0 {
		t.Fatal("obsolete base retained after its selector changed")
	}
	if _, err := search.OpenDiskIndex(ctx, derived, id, old.Source(), config.analyzer, nil); !errors.Is(err, search.ErrDiskIndex) {
		t.Fatalf("obsolete base reopened after retirement: %v", err)
	}
	if _, err := search.OpenDiskIndex(ctx, derived, newID, input.source, config.analyzer, nil); err != nil {
		t.Fatalf("worker retired Graph's selected base: %v", err)
	}
}

func awaitSearchRefresh(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatal("bounded Search refresh did not complete")
		case <-tick.C:
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func TestOwnedTarpSearchRefreshRecoversBoundedLagAndRetiresCrashArtifacts(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	graphDir, indexDir := filepath.Join(dir, "graph"), filepath.Join(dir, "search")
	runtime, executor := openTarpTestRuntime(t, graphDir, 16)
	config := tarpSearchStartup{
		corpus: "test", fingerprint: "lag/v1",
		project:        func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) },
		analyzer:       search.NewAnalyzer(nil, search.WhitespaceTokenizer{}, nil),
		maxChangeBytes: 1 << 20, maxChangeKeys: 1,
	}
	derived, err := tarp.Open(indexDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.tarp.openDiskSearch(ctx, derived, config); err != nil {
		t.Fatal(err)
	}
	base := runtime.tarp.diskSearch.base.Load()
	abandoned, err := reserveTarpSearchBase(ctx, derived)
	if err != nil {
		t.Fatal(err)
	}
	docs := func(yield func(search.DiskDocument, error) bool) {
		yield(search.DiskDocument{Key: "abandoned", Document: search.Text("partial")}, nil)
	}
	if err := search.BuildDiskBase(ctx, derived, abandoned, base.Source(), docs, config.analyzer); err != nil {
		t.Fatal(err)
	}
	if err := derived.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	runtime, executor = openTarpTestRuntime(t, graphDir, 16)
	derived, err = tarp.Open(indexDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.startOwnedDiskSearch(ctx, derived, config, 25*time.Millisecond, 2*time.Second); err != nil {
		_ = derived.Close()
		t.Fatal(err)
	}
	awaitSearchRefresh(t, func() bool {
		id, err := search.NextDiskBase(ctx, derived, base.BaseID())
		return err == nil && id == 0
	})
	_, err = executor.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "docs:a", Value: &pb.Vertex_String_{String_: "lagged"}},
		{Key: "docs:b", Value: &pb.Vertex_String_{String_: "lagged"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	awaitSearchRefresh(t, func() bool {
		fresh := runtime.tarp.diskSearch.base.Load()
		return fresh != nil && fresh.BaseID() != base.BaseID() && fresh.Source().Sequence > base.Source().Sequence
	})
	result, err := (searchReader{service: executor}).Capture(ctx, query.Spec{Query: "lagged", Limit: 10})
	if err != nil || result.BackendError != nil || len(result.Hits) != 2 {
		t.Fatalf("rebuild lost authoritative rows: %+v, %v", result, err)
	}
}

func TestTarpSearchDerivedReadFaultRebuildsOnlyTheSelectedBase(t *testing.T) {
	ctx := t.Context()
	runtime, _ := openTarpTestRuntime(t, t.TempDir(), 16)
	derived, err := tarp.Open(t.TempDir(), tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer derived.Close()
	config := tarpSearchStartup{
		corpus: "test", fingerprint: "read-fault/v1",
		project:        func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) },
		analyzer:       search.NewAnalyzer(nil, search.WhitespaceTokenizer{}, nil),
		maxChangeBytes: 1 << 20, maxChangeKeys: 64,
	}
	id, err := runtime.tarp.openDiskSearch(ctx, derived, config)
	if err != nil {
		t.Fatal(err)
	}
	reader := runtime.tarp.diskSearch
	base := reader.base.Load()
	worker := tarpSearchRefresh{relay: runtime.tarp, derived: derived, config: config, active: id}
	if reader.noteDerivedFault(base, errors.Join(search.ErrIndexIncomplete, tarp.ErrDataUnavailable)) {
		t.Fatal("Graph-stream read failure scheduled a derived rebuild")
	}
	if !reader.noteDerivedFault(base, tarp.ErrDataUnavailable) || !reader.noteDerivedFault(base, tarp.ErrCorrupt) {
		t.Fatal("derived storage faults did not request a rebuild")
	}
	if err := worker.refreshWithin(ctx, ctx); err != nil {
		t.Fatal(err)
	}
	fresh := reader.base.Load()
	if fresh == nil || fresh.BaseID() == id || fresh.Source().Sequence != base.Source().Sequence {
		t.Fatal("worker did not rebuild the same committed source")
	}
	if !reader.noteDerivedFault(base, tarp.ErrDataUnavailable) || reader.badBase.Load() != nil {
		t.Fatal("an obsolete handle requested rebuild of the new selection")
	}
}
