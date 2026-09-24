package service

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type snapshotFrameSink struct{ frames []*pb.SnapshotResponse }

func (s *snapshotFrameSink) Send(frame *pb.SnapshotResponse) error {
	s.frames = append(s.frames, frame)
	return nil
}

func TestSendSnapshotFramesPreservesGraphOnlyProjection(t *testing.T) {
	origin := hlc.NodeID{0x51}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	expiration := time.Now().Add(time.Hour)
	cut := replicationSnapshotCut{
		cutoffPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 4},
		cutoffHLC:       stamp,
		cutoffLocalSeq:  9,
		barriers: graphcache.CausalBarrierSnapshot[string]{
			Vertices: []graphcache.SnapshotVertexCausalBarrier[string]{{Key: "past", HLC: stamp}},
			Edges:    []graphcache.SnapshotEdgeCausalBarrier[string]{{Tail: "old-tail", Head: "old-head", HLC: stamp}},
		},
		tombstones: graphcache.TombstoneSnapshot[string]{
			Vertices: []graphcache.SnapshotVertexTombstone[string]{{Key: "gone", HLC: stamp, Expiration: expiration}},
			Edges:    []graphcache.SnapshotEdgeTombstone[string]{{Tail: "gone-tail", Head: "gone-head", HLC: stamp, Expiration: expiration}},
		},
		graph: graphcache.GraphSnapshot[string, *pb.Vertex]{
			Vertices: []graphcache.SnapshotVertex[string, *pb.Vertex]{
				{Key: "tail", Value: &pb.Vertex{Key: "tail"}, HLC: stamp},
				{Key: "head"},
			},
			Edges: []graphcache.SnapshotEdge[string]{{Tail: "tail", Head: "head", Contributions: []graphcache.SnapshotContribution{{
				Weight: 2, Expiration: expiration, ContribID: graphcache.ContribID{1}, HLC: stamp,
			}}}},
		},
	}
	graphOnly := &snapshotFrameSink{}
	if err := sendSnapshotFrames(context.Background(), cut, pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, graphOnly); err != nil {
		t.Fatal(err)
	}
	if len(graphOnly.frames) != 9 {
		t.Fatalf("graph-only frame count = %d, want 9", len(graphOnly.frames))
	}
	header := graphOnly.frames[0].GetHeader()
	footer := graphOnly.frames[len(graphOnly.frames)-1].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 || header.GetCutoffLocalSeq() != 9 ||
		header.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin[:])] != 4 || footer.GetVertexCount() != 2 ||
		footer.GetEdgeCount() != 1 || footer.GetVertexCausalBarrierCount() != 1 || footer.GetEdgeCausalBarrierCount() != 1 ||
		footer.GetVertexTombstoneCount() != 1 || footer.GetEdgeTombstoneCount() != 1 {
		t.Fatalf("graph-only header/footer drift: %+v, %+v", header, footer)
	}
	if graphOnly.frames[1].GetVertexCausalBarrier() == nil || graphOnly.frames[2].GetEdgeCausalBarrier() == nil ||
		graphOnly.frames[3].GetVertexTombstone() == nil || graphOnly.frames[4].GetEdgeTombstone() == nil ||
		graphOnly.frames[5].GetVertex() == nil || graphOnly.frames[6].GetVertex().GetVertex().GetNil() != true ||
		graphOnly.frames[7].GetEdge() == nil || graphOnly.frames[8].GetFooter() == nil {
		t.Fatalf("graph-only frame order or nil endpoint changed: %+v", graphOnly.frames)
	}

	privateReceipt := &snapshotFrameSink{}
	if err := sendSnapshotFrames(context.Background(), cut, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1, privateReceipt); err != nil {
		t.Fatal(err)
	}
	if len(privateReceipt.frames) != len(graphOnly.frames) || privateReceipt.frames[0].GetHeader().GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 {
		t.Fatalf("private format changed graph frame shape: %+v", privateReceipt.frames)
	}
	for i := 1; i < len(graphOnly.frames); i++ {
		if !proto.Equal(graphOnly.frames[i], privateReceipt.frames[i]) {
			t.Fatalf("private format changed graph frame %d", i)
		}
	}
}
