package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

func TestGraphStoreRequiredRestoreNeedsInitializedLanternManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uninitialized")
	raw, err := tarp.Open(dir, tarp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_DATA_DIR", dir)
	config, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	points := backup.Config{RestoreOnStart: true, RestoreRequired: true, Dir: t.TempDir()}
	_, closeRuntime, err := NewServingRuntime(config.Receipts, config.Cache, config.Search, config.MutationLog, config.Replication, points, nil)
	if closeRuntime != nil {
		closeRuntime()
	}
	if !errors.Is(err, graphstore.ErrInvalidManifest) {
		t.Fatalf("required restore from an uninitialized Tarp database returned %v", err)
	}
}

func TestGraphStoreReopensNodeAndNamespacedValues(t *testing.T) {
	t.Setenv("LANTERN_DATA_DIR", filepath.Join(t.TempDir(), "graph"))
	t.Setenv("LANTERN_NODE_ID", "")
	config, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	open := func(node ReplicationConfig) (*dataruntime.ServingRuntime, func(), error) {
		return NewServingRuntime(config.Receipts, config.Cache, config.Search, config.MutationLog, node, backup.Config{}, nil)
	}
	rt, closeRT, err := open(config.Replication)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRT()
	id := rt.NodeID()
	if id == (hlc.NodeID{}) {
		t.Fatal("missing persisted origin")
	}
	primary := rt.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "data:sys:literal", Value: &pb.Vertex_String_{String_: "retained"}}}); err != nil {
		t.Fatal(err)
	}
	closeRT()
	if _, cleanup, err := open(ReplicationConfig{NodeID: hlc.NodeID{9}, nodeIDExplicit: true}); !errors.Is(err, graphstore.ErrNodeMismatch) {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("foreign node opened database: %v", err)
	}
	reopened, cleanup, err := open(config.Replication)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if reopened.NodeID() != id {
		t.Fatal("restart changed node identity")
	}
	primary = reopened.NewExecutor(nil)
	got, err := primary.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "data:sys:literal"})
	if err != nil || got.GetVertex().GetString_() != "retained" || primary.LocalSeq(id) != 1 {
		t.Fatalf("reopen got %v, seq %d, err %v", got, primary.LocalSeq(id), err)
	}
}

func TestGraphStoreProductionSearchReopensWithoutRAMPostings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "graph")
	t.Setenv("LANTERN_DATA_DIR", dir)
	config, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	open := func(enabled bool) (*dataruntime.ServingRuntime, *dataruntime.Executor, func()) {
		t.Helper()
		search := config.Search
		search.Enabled = enabled
		search.AnalysisLimits.MaxDocumentBytes = 64
		rt, closeRT, err := NewServingRuntime(config.Receipts, config.Cache, search, config.MutationLog, config.Replication, backup.Config{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(closeRT)
		limits := query.DefaultLimits()
		limits.Enabled = enabled
		return rt, rt.NewExecutor(nil).WithSearchLimits(limits), closeRT
	}
	put := func(executor *dataruntime.Executor, key, value string) error {
		t.Helper()
		_, err := executor.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: key, Value: &pb.Vertex_String_{String_: value},
		}})
		return err
	}
	rt, executor, closeRT := open(true)
	for _, key := range []string{"data:docs:a", "data:docs:b"} {
		if err := put(executor, key, "sharedtoken"); err != nil {
			t.Fatal(err)
		}
	}
	if stats := rt.GraphCache().SearchIndexMemoryStats(); stats.Documents != 0 || stats.Postings != 0 {
		t.Fatalf("production Graph retained search postings in RAM: %+v", stats)
	}
	status, err := executor.GetServerStatus(t.Context(), &pb.GetServerStatusRequest{})
	if stats := status.GetSearch().GetIndexStats(); err != nil || stats.GetHealth() != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY || stats.GetDocuments() != 2 {
		t.Fatalf("production Search status did not inspect the committed cut: %v, %v", stats, err)
	}
	if err := put(executor, "data:docs:rejected", strings.Repeat("x", 128)); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("over-limit document returned %v", err)
	}
	if _, err := executor.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "data:docs:rejected"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("rejected searchable document escaped staging: %v", err)
	}
	request := &pb.SearchVerticesRequest{
		Query: "sharedtoken", Limit: 1, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX,
	}
	first, err := executor.SearchVertices(t.Context(), request)
	if err != nil || len(first.GetHits()) != 1 || len(first.GetNextCursor()) == 0 ||
		first.GetHits()[0].GetKey() != "data:docs:a" || first.GetHits()[0].GetVertex().GetString_() != "sharedtoken" {
		t.Fatalf("disk search first page: %v, %v", first, err)
	}
	request.Cursor = first.GetNextCursor()
	next, err := executor.SearchVertices(t.Context(), request)
	if err != nil || len(next.GetHits()) != 1 || next.GetHits()[0].GetKey() != "data:docs:b" {
		t.Fatalf("disk search continuation: %v, %v", next, err)
	}
	closeRT()

	_, executor, closeRT = open(false)
	if err := put(executor, "data:docs:a", "zebra"); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.DeleteVertex(t.Context(), &pb.DeleteVertexRequest{Key: "data:docs:b"}); err != nil {
		t.Fatal(err)
	}
	closeRT()

	_, executor, closeRT = open(true)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		fresh, err := executor.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "zebra"})
		if err == nil {
			if len(fresh.GetHits()) != 1 || fresh.GetHits()[0].GetKey() != "data:docs:a" {
				t.Fatalf("re-enabled disk search missed disabled-period changes: %v", fresh)
			}
			break
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("disk search did not finish rebuilding")
		case <-tick.C:
		}
	}
	stale, err := executor.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "sharedtoken"})
	if err != nil || len(stale.GetHits()) != 0 {
		t.Fatalf("re-enabled disk search retained stale postings: %v, %v", stale, err)
	}
	closeRT()

	indexDir := dir + ".search"
	if err := os.Rename(indexDir, indexDir+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexDir, []byte("unavailable"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, executor, _ = open(true)
	if _, err := executor.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "zebra"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unavailable derived storage returned %v", err)
	}
	vertex, err := executor.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "data:docs:a"})
	if err != nil || vertex.GetVertex().GetString_() != "zebra" {
		t.Fatalf("derived Search failure affected Graph reads: %v, %v", vertex, err)
	}
}
