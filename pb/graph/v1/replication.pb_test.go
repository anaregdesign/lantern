package graphv1_test

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func roundTripReplicationProto(t *testing.T, src, dst proto.Message) {
	t.Helper()
	b, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal replication message: %v", err)
	}
	if err := proto.Unmarshal(b, dst); err != nil {
		t.Fatalf("unmarshal replication message: %v", err)
	}
	if !proto.Equal(src, dst) {
		t.Fatalf("round-trip mismatch:\n before=%v\n after =%v", src, dst)
	}
}

// TestAuthoritativePutReplicationWireRoundTrip pins the generated oneof
// surface used to preserve each accepted Put item's authoritative outcome and
// the causal-barrier Snapshot frames added with it.
func TestAuthoritativePutReplicationWireRoundTrip(t *testing.T) {
	t.Parallel()

	vertexBatch := &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{
		{Outcome: &pb.ReplicatedPutVertex_Live{Live: &pb.Vertex{Key: "live-v"}}},
		{Outcome: &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: "expired-v"}}},
	}}
	edgeBatch := &pb.ReplicatedPutEdges{Entries: []*pb.ReplicatedPutEdge{
		{Outcome: &pb.ReplicatedPutEdge_Live{Live: &pb.Edge{Tail: "live-t", Head: "live-h", Weight: 2}}},
		{Outcome: &pb.ReplicatedPutEdge_CausalBarrier{CausalBarrier: &pb.EdgeCausalBarrier{Tail: "expired-t", Head: "expired-h"}}},
	}}

	vertexOp := &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{ReplicatedPutVertices: vertexBatch}}
	vertexCopy := &pb.MutationOp{}
	roundTripReplicationProto(t, vertexOp, vertexCopy)
	gotVertices := vertexCopy.GetReplicatedPutVertices().GetEntries()
	if len(gotVertices) != 2 || gotVertices[0].GetLive().GetKey() != "live-v" || gotVertices[1].GetCausalBarrier().GetKey() != "expired-v" {
		t.Fatalf("vertex replication entries = %+v", gotVertices)
	}
	if gotVertices[0].GetOutcome() == nil || gotVertices[0].GetCausalBarrier() != nil || gotVertices[1].GetLive() != nil {
		t.Fatalf("vertex outcome oneofs were not preserved: %+v", gotVertices)
	}
	if vertexCopy.GetReplicatedPutEdges() != nil {
		t.Fatal("vertex mutation unexpectedly decoded as replicated edges")
	}

	edgeOp := &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{ReplicatedPutEdges: edgeBatch}}
	edgeCopy := &pb.MutationOp{}
	roundTripReplicationProto(t, edgeOp, edgeCopy)
	gotEdges := edgeCopy.GetReplicatedPutEdges().GetEntries()
	if len(gotEdges) != 2 || gotEdges[0].GetLive().GetTail() != "live-t" || gotEdges[0].GetLive().GetHead() != "live-h" || gotEdges[1].GetCausalBarrier().GetTail() != "expired-t" || gotEdges[1].GetCausalBarrier().GetHead() != "expired-h" {
		t.Fatalf("edge replication entries = %+v", gotEdges)
	}
	if gotEdges[0].GetOutcome() == nil || gotEdges[0].GetCausalBarrier() != nil || gotEdges[1].GetLive() != nil {
		t.Fatalf("edge outcome oneofs were not preserved: %+v", gotEdges)
	}
	if edgeCopy.GetReplicatedPutVertices() != nil {
		t.Fatal("edge mutation unexpectedly decoded as replicated vertices")
	}

	var nilVertex *pb.ReplicatedPutVertex
	var nilEdge *pb.ReplicatedPutEdge
	if nilVertex.GetOutcome() != nil || nilVertex.GetLive() != nil || nilVertex.GetCausalBarrier() != nil ||
		nilEdge.GetOutcome() != nil || nilEdge.GetLive() != nil || nilEdge.GetCausalBarrier() != nil {
		t.Fatal("nil generated oneof getters must remain nil-safe")
	}
}

func TestCausalBarrierSnapshotFramesRoundTrip(t *testing.T) {
	t.Parallel()

	header := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{CutoffLocalSeq: 9}}}
	vertexBarrier := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "v", Hlc: &pb.HLCTimestamp{WallNs: 10}}}}
	edgeBarrier := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeCausalBarrier{EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{Tail: "t", Head: "h", Hlc: &pb.HLCTimestamp{WallNs: 11}}}}
	vertex := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: &pb.Vertex{Key: "live-v"}, Hlc: &pb.HLCTimestamp{WallNs: 12}}}}
	edge := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{Tail: "live-t", Head: "live-h", Hlc: &pb.HLCTimestamp{WallNs: 13}}}}
	footer := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{VertexCount: 1, EdgeCount: 1, VertexCausalBarrierCount: 1, EdgeCausalBarrierCount: 1}}}

	frames := []*pb.SnapshotResponse{header, vertexBarrier, edgeBarrier, vertex, edge, footer}
	for i, frame := range frames {
		copy := &pb.SnapshotResponse{}
		roundTripReplicationProto(t, frame, copy)
		if copy.GetEntry() == nil || !proto.Equal(frame, copy) {
			t.Fatalf("frame %d round trip = %v, want %v", i, copy, frame)
		}
	}
	if header.GetHeader().GetCutoffLocalSeq() != 9 || header.GetVertex() != nil || header.GetEdge() != nil || header.GetFooter() != nil {
		t.Fatal("header oneof getter mismatch")
	}
	if got := vertexBarrier.GetVertexCausalBarrier(); got.GetKey() != "v" || got.GetHlc().GetWallNs() != 10 || vertexBarrier.GetEdgeCausalBarrier() != nil {
		t.Fatalf("vertex barrier frame = %+v", got)
	}
	if got := edgeBarrier.GetEdgeCausalBarrier(); got.GetTail() != "t" || got.GetHead() != "h" || got.GetHlc().GetWallNs() != 11 || edgeBarrier.GetVertexCausalBarrier() != nil {
		t.Fatalf("edge barrier frame = %+v", got)
	}
	if vertex.GetVertex().GetVertex().GetKey() != "live-v" || vertex.GetVertex().GetHlc().GetWallNs() != 12 {
		t.Fatalf("vertex frame = %+v", vertex.GetVertex())
	}
	if edge.GetEdge().GetTail() != "live-t" || edge.GetEdge().GetHead() != "live-h" || edge.GetEdge().GetHlc().GetWallNs() != 13 {
		t.Fatalf("edge frame = %+v", edge.GetEdge())
	}
	gotFooter := footer.GetFooter()
	if gotFooter.GetVertexCount() != 1 || gotFooter.GetEdgeCount() != 1 || gotFooter.GetVertexCausalBarrierCount() != 1 || gotFooter.GetEdgeCausalBarrierCount() != 1 {
		t.Fatalf("footer = %+v", gotFooter)
	}
	var nilResponse *pb.SnapshotResponse
	if nilResponse.GetEntry() != nil || nilResponse.GetHeader() != nil || nilResponse.GetVertex() != nil || nilResponse.GetEdge() != nil || nilResponse.GetFooter() != nil || nilResponse.GetVertexCausalBarrier() != nil || nilResponse.GetEdgeCausalBarrier() != nil {
		t.Fatal("nil SnapshotResponse getters must remain nil-safe")
	}
}
