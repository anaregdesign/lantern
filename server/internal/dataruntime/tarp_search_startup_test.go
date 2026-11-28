package dataruntime

import (
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

func TestTarpSearchStartupReopensSelectedBaseAndReplaysAcceptedSuffix(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	graphDir, indexDir := filepath.Join(dir, "graph"), filepath.Join(dir, "search")
	runtime, executor := openTarpTestRuntime(t, graphDir, 16)
	derived, err := tarp.Open(indexDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = derived.Close() })
	plan := tarpSearchStartup{
		corpus: "test", fingerprint: "test search projection/v1",
		project:        func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) },
		analyzer:       search.NewScriptAwareAnalyzer(),
		maxChangeBytes: 1 << 20, maxChangeKeys: 64,
	}
	baseID, err := runtime.tarp.openDiskSearch(ctx, derived, plan)
	if err != nil || baseID != 1 {
		t.Fatalf("empty authoritative graph did not select a base: %d, %v", baseID, err)
	}
	if _, err := executor.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "after-base", Value: &pb.Vertex_String_{String_: "retained"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := derived.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, executor = openTarpTestRuntime(t, graphDir, 16)
	after, err := tarp.OpenExisting(ctx, indexDir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = after.Close() })
	reused, err := runtime.tarp.openDiskSearch(ctx, after, plan)
	if err != nil || reused != baseID {
		t.Fatalf("did not reuse the authoritative base: %d, %v", reused, err)
	}
	got, err := (searchReader{service: executor}).Capture(ctx, query.Spec{
		Query: "retained", Limit: 5,
		Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX,
	})
	if err != nil || got.BackendError != nil || len(got.Hits) != 1 ||
		got.Hits[0].GetKey() != "after-base" || got.Hits[0].GetVertex().GetString_() != "retained" {
		t.Fatalf("reopen lost accepted Search postimage: %+v, %v", got, err)
	}
}
