package dataruntime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/proto"
)

func openTarpReceiptTestRuntime(t *testing.T, dir string, cfg ReceiptDatabaseConfig, fresh bool) (*ServingRuntime, *Executor) {
	t.Helper()
	db, err := graphstore.Open(t.Context(), dir, cfg.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](cfg.DefaultTTL)
	if err = cfg.ConfigureGraph(graph); err != nil {
		t.Fatal(err)
	}
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
	r, err := NewTarpReceiptServingRuntime(graph, db, stage, cfg, fresh)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	s := r.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	rep, err := r.NewReplication(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.CertifyInstallation(s, rep); err != nil {
		t.Fatal(err)
	}
	if err = r.CertifyReceiptBackup(s, rep); err != nil {
		t.Fatal(err)
	}
	if err = r.ActivatePublicReceipts(s, rep); err != nil {
		t.Fatal(err)
	}
	return r, s
}

func TestTarpReceiptsSurviveRelayEvictionAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	config := durableRuntimeTestConfig("")
	config.BaselineCodec = &receiptBaselineTestCodec{}
	config.Log.Capacity = 1
	rt, s := openTarpReceiptTestRuntime(t, dir, config, true)
	gen := rt.receipt.generation
	req := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "original", Value: &pb.Vertex_String_{String_: "first"}}}, IfAbsent: true, ReceiptContext: publicReceiptContext(t, rt, 0x57, 1)}
	first, err := s.PutVertices(t.Context(), req)
	if err != nil || first.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("first put %v, %v", first, err)
	}
	_, err = s.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "original", Value: &pb.Vertex_String_{String_: "overwritten"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, s = openTarpReceiptTestRuntime(t, dir, config, false)
	if rt.receipt.generation != gen {
		t.Fatal("receipt endpoint rotated on ordinary restart")
	}
	replay, err := s.PutVertices(t.Context(), req)
	if err != nil || !proto.Equal(first, replay) {
		t.Fatalf("replay %v, %v", replay, err)
	}
	record, err := s.GetReceiptStatuses(t.Context(), &pb.GetReceiptStatusesRequest{OperationIds: req.ReceiptContext.OperationIds})
	if err != nil || len(record.GetStatuses()) != 1 || record.GetStatuses()[0].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED || record.GetStatuses()[0].GetReceipt().GetOriginalResult().GetPutVertexOutcome() != first.Outcomes[0] {
		t.Fatalf("status %v, %v", record, err)
	}
	vertex, err := s.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "original"})
	if err != nil || vertex.GetVertex().GetString_() != "overwritten" || s.LocalSeq(config.NodeID) != 2 {
		t.Fatalf("receipt replay reapplied graph or relay: %v %v", vertex, err)
	}
}

func TestTarpReceiptSnapshotRestoresBoundMarkerAndRetiredCatalog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	config := baselineRuntimeTestConfig("")
	image := newReceiptBaselineTestImage(t, config)
	retired, _ := mustRetiredCatalogSnapshot(t, config.Receipt, image.capture.Receipts.ClockHighWaterMillis, 0x47)
	setReceiptBaselineTestRetired(&image, retired)
	config.BaselineCodec = image.codec
	rt, s := openTarpReceiptTestRuntime(t, dir, config, true)
	genesis := rt.receipt.generation
	if err := s.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	if rt.receipt.generation == genesis {
		t.Fatal("snapshot did not rotate receipt endpoint")
	}
	newGen := rt.receipt.generation
	ref := rt.receipt.committedBaseline
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, s = openTarpReceiptTestRuntime(t, dir, config, false)
	if rt.receipt.generation != newGen || rt.receipt.committedBaseline != ref || s.LocalSeq(image.origin) != 1 {
		t.Fatal("snapshot marker/endpoint/origin mismatch")
	}
	view, err := rt.GraphStore().Read()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	w, err := rt.TarpBackupWitness(view.Snapshot, view.Generation)
	if err != nil || w.ReceiptRetiredCount != 2 || w.ReceiptCount != 1 || w.ReceiptBaselineDigest != ref.Digest {
		t.Fatalf("restored witness %+v, %v", w, err)
	}
}

func TestTarpReceiptRestartPreservesAcceptedRemoteClockFloor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	config := durableRuntimeTestConfig("")
	config.BaselineCodec = &receiptBaselineTestCodec{}
	rt, s := openTarpReceiptTestRuntime(t, dir, config, true)
	remote := hlc.NodeID{7}
	future := time.Now().Add(hlc.DefaultMaxSkew / 2).UnixNano()
	m := &pb.Mutation{Origin: remote[:], Seq: 1, Hlc: newHLC(future, remote), Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "remote", Value: &pb.Vertex_String_{String_: "accepted"}}}}}}
	if err := s.ApplyMutation(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	rt, _ = openTarpReceiptTestRuntime(t, dir, config, false)
	if now := rt.clock.Now(); now.WallNs < future || (now.WallNs == future && now.Logical == 0) {
		t.Fatalf("reopened clock %v below accepted remote floor", now)
	}
}
