package dataruntime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	rt, err := NewTarpGraphServingRuntime(t.Context(), graph, db, mutationlog.Options{Capacity: capacity}, stage, "")
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, rt.NewExecutor(nil)
}

func TestTarpRuntimeStartupCancellationLeavesFreshStoreUninitialized(t *testing.T) {
	db, err := graphstore.Open(t.Context(), t.TempDir(), hlc.NodeID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	graph := graphcache.NewGraphCache[string, *pb.Vertex](0)
	stage, err := RestoreGraphStorage(t.Context(), graph, db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rt, err := NewTarpGraphServingRuntime(ctx, graph, db, mutationlog.Options{Capacity: 16}, stage, "")
	if rt != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup = %v, %v", rt, err)
	}
	view, err := db.Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if view.Revision != 0 {
		t.Fatalf("cancelled startup initialized revision %d", view.Revision)
	}
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
	// This bucket remains cold while the peer Snapshot merges unrelated
	// data. Its local contribution history must cross the generation switch.
	if _, err := service.AddEdges(t.Context(), &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "kept", Head: "other", Weight: 2},
		{Tail: "kept", Head: "other", Weight: 4},
	}}); err != nil {
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
	edge, err := service.GetEdge(t.Context(), &pb.GetEdgeRequest{Tail: "kept", Head: "other"})
	if err != nil || edge.GetEdge().GetWeight() != 6 {
		t.Fatalf("reopened cold Edge = %v, %v", edge, err)
	}
	if service.LocalSeq(remote) != 9 || service.LocalSeq(hlc.NodeID{1}) != 2 {
		t.Fatalf("incorrect restored origins %v", service.OriginStates())
	}
	if tip, ok := rt.log.LastSeq(); !ok || tip != 3 {
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

func TestTarpSnapshotRetryProvesCleanCut(t *testing.T) {
	for _, mutation := range []string{"none", "overwrite", "delete", "watermark"} {
		t.Run(mutation, func(t *testing.T) {
			rt, s := openTarpTestRuntime(t, filepath.Join(t.TempDir(), "graph"), 3)
			ctx := t.Context()
			if _, err := s.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "before"}}); err != nil {
				t.Fatal(err)
			}
			end, err := s.BeginSnapshotInstallChecked(ctx, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			now := hlc.Timestamp{NodeID: hlc.NodeID{3}, WallNs: time.Now().Add(time.Second).UnixNano()}
			switch mutation {
			case "overwrite":
				rt.graph.PutVertexWithExpirationHLC("before", &pb.Vertex{Key: "before", Value: &pb.Vertex_String_{String_: "partial"}}, time.Time{}, now)
			case "delete":
				rt.graph.DeleteVertexHLC("before", now, time.Now().Add(time.Minute))
			case "watermark":
				if err := s.ApplySnapshotWatermarks(map[string]uint64{hex.EncodeToString(now.NodeID[:]): 3}, now); err != nil {
					t.Fatal(err)
				}
			}
			if err := end(false); err != nil {
				t.Fatal(err)
			}
			retry, err := s.BeginSnapshotInstallChecked(ctx, func() error { return nil })
			if mutation != "none" {
				if err == nil {
					t.Fatal("retry accepted partial live state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "before"}); err == nil {
				t.Fatal("clean retry reopened reads before verified publication")
			}
			if err := retry(true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "before"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Kill the process with an older relay view held. The first crash occurs in
// the generation's final validator, after every staged row has reached disk;
// the second occurs after publication but before clean DB or pin shutdown.
func TestTarpSnapshotProcessCrashWithPinnedReplay(t *testing.T) {
	const phaseVar = "LANTERN_TEST_TARP_CRASH_PHASE"
	const dirVar = "LANTERN_TEST_TARP_CRASH_DIR"
	remote := hlc.NodeID{2}
	if phase := os.Getenv(phaseVar); phase != "" {
		dir := os.Getenv(dirVar)
		rt, svc := openTarpTestRuntime(t, dir, 2)
		if _, err := svc.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "before"}}); err != nil {
			t.Fatal(err)
		}
		ref := rt.log.RetainedEntries()[0]
		pin, err := rt.tarp.Pin()
		if err != nil {
			t.Fatal(err)
		}
		crash := func() error {
			if _, err := pin.Read(ref); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir+".ready", []byte(phase), 0600); err != nil {
				t.Fatal(err)
			}
			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			if err = process.Kill(); err != nil {
				t.Fatal(err)
			}
			select {}
		}
		finish, err := svc.BeginSnapshotInstallChecked(t.Context(), func() error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		stamp := hlc.Timestamp{WallNs: 1000, NodeID: remote}
		rt.graph.PutVertexWithExpirationHLC("peer", &pb.Vertex{Key: "peer"}, time.Time{}, stamp)
		if err := svc.ApplySnapshotWatermarks(map[string]uint64{hex.EncodeToString(remote[:]): 9}, stamp); err != nil {
			t.Fatal(err)
		}
		if phase == "staged" {
			// Invoke the same durable commit used by finish, with an explicit
			// crash at the last instruction before manifest publication.
			t.Fatal("did not crash before publication", svc.commitTarpGraphSnapshot(t.Context(), crash))
		}
		if err := finish(true); err != nil {
			t.Fatal(err)
		}
		t.Fatal("did not crash after publication", crash())
	}
	for _, phase := range []string{"staged", "published"} {
		t.Run(phase, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "db")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTarpSnapshotProcessCrashWithPinnedReplay$")
			cmd.Env = append(os.Environ(), phaseVar+"="+phase, dirVar+"="+dir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			ready, readErr := os.ReadFile(dir + ".ready")
			if !errors.As(err, &exit) || ctx.Err() != nil || readErr != nil || string(ready) != phase {
				t.Fatalf("subprocess missed %s boundary: %v, %v, %s", phase, err, readErr, output)
			}
			rt, svc := openTarpTestRuntime(t, dir, 2)
			_, before := svc.cache.GetVertex("before")
			_, peer := svc.cache.GetVertex("peer")
			tip, ok := rt.log.LastSeq()
			hasPeer := phase == "published"
			wantTip, wantOrigin, retained := uint64(1), uint64(0), 1
			if hasPeer {
				wantTip, wantOrigin, retained = 2, 9, 0
			}
			if !before || peer != hasPeer || svc.LocalSeq(remote) != wantOrigin || !ok || tip != wantTip || rt.log.Len() != retained {
				t.Fatalf("after crash at %s: before=%v peer=%v peer seq=%d local tip=%d retained=%d", phase, before, peer, svc.LocalSeq(remote), tip, rt.log.Len())
			}
			if _, err := svc.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "successor"}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTarpRemoteGapAndDuplicateAcrossRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	rt, s := openTarpTestRuntime(t, dir, 4)
	origin := hlc.NodeID{9}
	mutation := func(seq uint64) *pb.Mutation {
		return &pb.Mutation{Origin: origin[:], Seq: seq, Hlc: newHLC(int64(seq), origin), Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: fmt.Sprintf("remote-%d", seq)}}}}}
	}
	if err := s.ApplyMutation(t.Context(), mutation(2)); err != nil {
		t.Fatal(err)
	}
	if s.LocalSeq(origin) != 0 || rt.log.Len() != 0 {
		t.Fatal("future delivery advanced durable cursor")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, s = openTarpTestRuntime(t, dir, 4)
	for _, n := range []uint64{2, 1, 2} {
		if err := s.ApplyMutation(t.Context(), mutation(n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, s = openTarpTestRuntime(t, dir, 4)
	if err := s.ApplyMutation(t.Context(), mutation(1)); err != nil {
		t.Fatal(err)
	}
	if got := s.LocalSeq(origin); got != 2 || rt.log.Len() != 2 {
		t.Fatalf("duplicate changed cut %d/%d", got, rt.log.Len())
	}
	for _, k := range []string{"remote-1", "remote-2"} {
		if _, err := s.GetVertex(t.Context(), &pb.GetVertexRequest{Key: k}); err != nil {
			t.Fatal(err)
		}
	}
}
