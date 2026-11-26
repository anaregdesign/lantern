package dataruntime

import (
	"path/filepath"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestTarpSnapshotStreamsPinnedGraphAndExactReceiptCut(t *testing.T) {
	for _, format := range []pb.SnapshotFormat{pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT} {
		t.Run(format.String(), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "store")
			var r *ServingRuntime
			var s *Executor
			cfg := durableRuntimeTestConfig("")
			switch format {
			case pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT:
				cfg.BaselineCodec = &receiptBaselineTestCodec{}
				r, s = openTarpReceiptTestRuntime(t, dir, cfg, true)
			default:
				r, s = openTarpTestRuntime(t, dir, 8)
			}
			rep, err := r.NewReplication(s)
			if err != nil {
				t.Fatal(err)
			}
			if r.receipt != nil {
				source, err := NewReceiptWholeStateSource(s, r.receipt.store)
				if err != nil {
					t.Fatal(err)
				}
				if err = rep.ConfigureReceiptSnapshot(source, r.receipt.policy); err != nil {
					t.Fatal(err)
				}
			}
			request := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "a", Value: &pb.Vertex_String_{String_: "before"}}, {Key: "b", Value: &pb.Vertex_Int64{Int64: 7}}}}
			if r.receipt != nil {
				request.ReceiptContext = publicReceiptContext(t, r, 0x31, 2)
			}
			if _, err = s.PutVertices(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			if _, err = s.PutEdges(t.Context(), &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 2}}}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.AddEdges(t.Context(), &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 3}}}); err != nil {
				t.Fatal(err)
			}
			rec := &replicationSnapshotRecorder{onSend: func(f *pb.SnapshotResponse) error {
				if f.GetHeader() != nil {
					_, e := s.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "after"}}})
					return e
				}
				return nil
			}}
			if err = rep.Snapshot(t.Context(), &pb.SnapshotRequest{RequiredFormat: format}, rec); err != nil {
				t.Fatal(err)
			}
			footer := rec.frames[len(rec.frames)-1].GetFooter()
			if footer.GetVertexCount() != 2 || footer.GetEdgeCount() != 1 || footer.GetEdgeCausalBarrierCount() != 1 {
				t.Fatalf("footer %v", footer)
			}
			var vertex *pb.Vertex
			for _, f := range rec.frames {
				if v := f.GetVertex().GetVertex(); v.GetKey() == "a" {
					vertex = v
				}
			}
			if vertex.GetString_() != "before" || rec.frames[0].GetHeader().GetCutoffLocalSeq() != 3 {
				t.Fatalf("mixed-cut snapshot %v", rec.frames)
			}
			if r.receipt != nil {
				metadata := rec.frames[0].GetHeader().GetReceiptMetadata()
				retired, _, err := retiredCatalogConfig(cfg.Receipt, int64(metadata.ClockHighWaterUnixMs))
				if err != nil {
					t.Fatal(err)
				}
				capture, err := DecodeReceiptSnapshotFrames(rec.frames, r.receipt.policy, retired)
				if err != nil || len(capture.Receipts.Receipts) != 2 {
					t.Fatalf("invalid receipt Snapshot: %v", err)
				}
			}

		})
	}
}
