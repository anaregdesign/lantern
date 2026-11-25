package dataruntime

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/tarp"
)

func openTarpTestRuntime(t *testing.T, dir string, capacity int) (*ServingRuntime, *Executor) {
	t.Helper()
	db, err := graphstore.Open(t.Context(), dir, hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	graph := graphcache.NewGraphCache[string, *pb.Vertex](0)
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err = graph.RestoreStorage(t.Context(), rows, view); err != nil {
		t.Fatal(err)
	}
	_ = view.Close()
	stage := func(ctx context.Context, gen uint64, snap *tarp.Snapshot, batch graphstore.Writer, keys graphcache.StorageKeys[string], writer graphcache.StorageRowWriter[string, *pb.Vertex]) error {
		if writer == nil {
			writer = graph
		}
		return writer.StoreRows(ctx, rows, snap, batch, gen, keys)
	}
	rt, err := NewTarpGraphServingRuntime(graph, db, mutationlog.Options{Capacity: capacity}, stage, "")
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, rt.NewExecutor(nil)
}

func TestTarpRuntimeRestoresLocalGraphAndRetainedRelay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "graph")
	rt, s := openTarpTestRuntime(t, dir, 3)
	for _, key := range []string{"a", "b", "c"} {
		if _, err := s.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: key, Value: &pb.Vertex_String_{String_: key}}}}); err != nil {
			t.Fatal(err)
		}
	}
	in := &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 3}}}
	if _, err := s.AddEdges(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	r, s := openTarpTestRuntime(t, dir, 2)
	if got := s.LocalSeq(hlc.NodeID{1}); got != 4 {
		t.Fatalf("origin frontier %d", got)
	}
	if got := r.log.RetainedEntries(); len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 4 {
		t.Fatalf("restored ring %v", got)
	}
	vertex, err := s.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "a"})
	if err != nil || vertex.GetVertex().GetString_() != "a" {
		t.Fatalf("vertex %v, %v", vertex, err)
	}
	edge, err := s.GetEdge(t.Context(), &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil || edge.GetEdge().GetWeight() != 3 {
		t.Fatalf("edge %v, %v", edge, err)
	}
	if _, err = s.AddEdges(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if got := s.LocalSeq(hlc.NodeID{1}); got != 5 {
		t.Fatalf("new origin seq %d", got)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r, s = openTarpTestRuntime(t, dir, 3)
	edge, err = s.GetEdge(t.Context(), &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil || edge.GetEdge().GetWeight() != 6 {
		t.Fatalf("next restored edge %v, %v", edge, err)
	}
}

func TestTarpGraphSnapshotPreservesCutAcrossReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "graph")
	rt, service := openTarpTestRuntime(t, dir, 3)
	_, err := service.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "kept"}}})
	if err != nil {
		t.Fatal(err)
	}
	finish, err := service.BeginSnapshotInstallChecked(t.Context(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	remote := hlc.NodeID{2}
	stamp := hlc.Timestamp{NodeID: remote, WallNs: 1000}
	rt.graph.PutVertexWithExpirationHLC("from-peer", &pb.Vertex{Key: "from-peer"}, time.Time{}, stamp)
	if err = service.ApplySnapshotWatermarks(map[string]uint64{hex.EncodeToString(remote[:]): 9}, stamp); err != nil {
		t.Fatal(err)
	}
	if err = finish(true); err != nil {
		t.Fatal(err)
	}
	if rt.log.Len() != 0 {
		t.Fatal("prior ring survived snapshot")
	}
	if err = rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, service = openTarpTestRuntime(t, dir, 3)
	for _, k := range []string{"from-peer", "kept"} {
		got, err := service.GetVertex(t.Context(), &pb.GetVertexRequest{Key: k})
		if err != nil || got.GetVertex().GetKey() != k {
			t.Fatalf("reopened %q: %v, %v", k, got, err)
		}
	}
	if service.LocalSeq(remote) != 9 || service.LocalSeq(hlc.NodeID{1}) != 1 {
		t.Fatalf("incorrect restored origins %v", service.OriginStates())
	}
	if tip, ok := rt.log.LastSeq(); !ok || tip != 2 {
		t.Fatalf("incorrect restored snapshot boundary %v/%d", ok, tip)
	}
	_, err = service.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "successor"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTarpFailedGraphSnapshotReopensOldCut(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "graph")
	rt, service := openTarpTestRuntime(t, dir, 3)
	_, err := service.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "before"}})
	if err != nil {
		t.Fatal(err)
	}
	finish, err := service.BeginSnapshotInstallChecked(t.Context(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	rt.graph.PutVertexWithExpirationHLC("partial", &pb.Vertex{Key: "partial"}, time.Time{}, hlc.Timestamp{WallNs: 1000, NodeID: hlc.NodeID{2}})
	if err = finish(false); err != nil {
		t.Fatal(err)
	}
	if err = rt.Close(); err != nil {
		t.Fatal(err)
	}
	_, service = openTarpTestRuntime(t, dir, 3)
	if _, ok := service.cache.GetVertex("partial"); ok {
		t.Fatal("failed merge published")
	}
	if _, ok := service.cache.GetVertex("before"); !ok {
		t.Fatal("previous generation lost")
	}
}
