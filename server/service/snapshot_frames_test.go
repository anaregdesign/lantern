package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
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
			Edges: []graphcache.SnapshotEdge[string]{{Tail: "tail", Head: "head", Contributions: []graphcache.SnapshotContribution{
				{Weight: 2, Expiration: expiration, ContribID: graphcache.ContribID{1}, HLC: stamp},
				{Weight: 3, ContribID: graphcache.ContribID{2}, HLC: stamp},
			}}},
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
		footer.GetVertexTombstoneCount() != 1 || footer.GetEdgeTombstoneCount() != 1 ||
		header.GetReceiptMetadata() != nil || footer.GetReceiptCount() != 0 || footer.GetReceiptOriginCount() != 0 {
		t.Fatalf("graph-only header/footer drift: %+v, %+v", header, footer)
	}
	if graphOnly.frames[1].GetVertexCausalBarrier() == nil || graphOnly.frames[2].GetEdgeCausalBarrier() == nil ||
		graphOnly.frames[3].GetVertexTombstone() == nil || graphOnly.frames[4].GetEdgeTombstone() == nil ||
		graphOnly.frames[5].GetVertex() == nil || graphOnly.frames[6].GetVertex().GetVertex().GetNil() != true ||
		graphOnly.frames[7].GetEdge() == nil || graphOnly.frames[8].GetFooter() == nil {
		t.Fatalf("graph-only frame order or nil endpoint changed: %+v", graphOnly.frames)
	}
	contributions := graphOnly.frames[7].GetEdge().GetContributions()
	if len(contributions) != 2 || contributions[0].GetExpiration() == nil ||
		contributions[1].GetExpiration() != nil {
		t.Fatalf("graph-only contribution expirations = %+v", contributions)
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

func receiptSnapshotTestCapture(t *testing.T, withReceipt, withGraph bool) (ReceiptWholeStateCapture, mutationreceipt.Config) {
	t.Helper()
	issued := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x61}, Retention: time.Hour,
		MaxEntries: 8, MaxBytes: 1 << 20,
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	if withReceipt {
		id, err := mutationreceipt.NewID(policy.Epoch, issued, [24]byte{0x62})
		if err != nil {
			t.Fatal(err)
		}
		intent := mutationreceipt.Intent{
			ID: id, Group: mutationreceipt.GroupID{0x63}, Count: 1,
			Kind: mutationreceipt.AddEdge, Digest: mutationreceipt.IntentDigest([]byte("snapshot-add")),
			HasContrib: true, ContribID: mutationreceipt.ContribID{0x64},
		}
		tx, err := store.Begin(issued)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Abort()
		if class, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil || class != mutationreceipt.Fresh {
			t.Fatalf("Classify = %v, %v", class, err)
		}
		if err := tx.Reserve([][]byte{{0xde, 0xad}}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Stage(); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
	}
	receipts, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	policy.ClockHighWater = receipts.ClockHighWater()

	origin := hlc.NodeID{0x65}
	stamp := hlc.Timestamp{WallNs: issued.Add(time.Minute).UnixNano(), Logical: 2, NodeID: origin}
	cut := replicationSnapshotCut{
		cutoffPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 7},
		cutoffHLC:       stamp,
		cutoffLocalSeq:  11,
	}
	if withGraph {
		cut.graph.Vertices = []graphcache.SnapshotVertex[string, *pb.Vertex]{{
			Key: "live", Value: &pb.Vertex{Key: "live"}, HLC: stamp,
		}}
	}
	graph := &snapshotFrameSink{}
	if err := sendSnapshotFrames(context.Background(), cut, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1, graph); err != nil {
		t.Fatal(err)
	}
	return ReceiptWholeStateCapture{
		Graph: graph.frames, Receipts: receipts, Policy: policy,
		Origins: []OriginState{{Origin: origin, LastSeq: 7, LastHLC: stamp}},
	}, policy
}

func cloneReceiptSnapshotFrames(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
	out := make([]*pb.SnapshotResponse, len(frames))
	for i, frame := range frames {
		if frame != nil {
			out[i] = proto.Clone(frame).(*pb.SnapshotResponse)
		}
	}
	return out
}

func receiptSnapshotPutEdgeFrame(tail, head string) *pb.SnapshotResponse {
	return &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{
		Tail: tail,
		Head: head,
		Contributions: []*pb.SnapshotEdgeContribution{{
			Weight: 1,
		}},
	}}}
}

func TestPrepareReceiptSnapshotFramesCarriesCompleteDeterministicCut(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	frames, err := prepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 {
		t.Fatalf("frame count = %d, want header/receipt/vertex/footer", len(frames))
	}
	header := frames[0].GetHeader()
	metadata := header.GetReceiptMetadata()
	wirePolicy := metadata.GetPolicy()
	row := frames[1].GetReceipt()
	footer := frames[3].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		!bytes.Equal(wirePolicy.GetDeploymentEpoch(), capture.Receipts.Epoch[:]) ||
		!bytes.Equal(wirePolicy.GetFingerprint(), capture.Receipts.PolicyFingerprint[:]) ||
		wirePolicy.GetRetentionMs() != uint64(policy.Retention/time.Millisecond) ||
		wirePolicy.GetMaxEntries() != uint64(policy.MaxEntries) ||
		wirePolicy.GetMaxBytes() != policy.MaxBytes ||
		metadata.GetClockHighWaterUnixMs() != uint64(capture.Receipts.ClockHighWaterMillis) ||
		len(metadata.GetOriginCutoffs()) != 1 ||
		metadata.GetOriginCutoffs()[0].GetLastSeq() != 7 ||
		header.GetCutoffLocalSeq() != 11 {
		t.Fatalf("receipt header lost policy or cutoff metadata: %+v", header)
	}
	if row == nil ||
		row.GetKind() != pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_ADD_EDGE ||
		!bytes.Equal(row.GetOriginalResult(), []byte{0xde, 0xad}) ||
		len(row.GetOperationId()) != 49 || len(row.GetLogicalCallId()) != 16 ||
		len(row.GetIntentSha256()) != 32 ||
		len(row.GetContribution().GetContributionId()) != 24 {
		t.Fatalf("receipt row lost Store state: %+v", row)
	}
	if frames[2].GetVertex().GetVertex().GetKey() != "live" ||
		footer.GetReceiptCount() != 1 || footer.GetReceiptOriginCount() != 1 ||
		footer.GetVertexCount() != 1 {
		t.Fatalf("receipt body/footer drift: %+v", frames)
	}

	again, err := prepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	marshal := proto.MarshalOptions{Deterministic: true}
	for i := range frames {
		first, err := marshal.Marshal(frames[i])
		if err != nil {
			t.Fatal(err)
		}
		second, err := marshal.Marshal(again[i])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("frame %d is not deterministic", i)
		}
	}
}

func TestPrepareReceiptSnapshotFramesReceiptOnlyAndZeroRows(t *testing.T) {
	for _, tc := range []struct {
		name         string
		withReceipt  bool
		wantFrames   int
		wantReceipts uint64
	}{
		{"receipt only", true, 3, 1},
		{"zero rows", false, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture, policy := receiptSnapshotTestCapture(t, tc.withReceipt, false)
			frames, err := prepareReceiptSnapshotFrames(capture, policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != tc.wantFrames || frames[0].GetHeader() == nil ||
				frames[len(frames)-1].GetFooter().GetReceiptCount() != tc.wantReceipts ||
				frames[len(frames)-1].GetFooter().GetVertexCount() != 0 ||
				frames[len(frames)-1].GetFooter().GetEdgeCount() != 0 {
				t.Fatalf("receipt-only stream = %+v", frames)
			}
			if err := validateReceiptSnapshotFrames(frames); err != nil {
				t.Fatalf("valid receipt-only stream: %v", err)
			}
		})
	}
}

func TestValidateReceiptSnapshotFramesRejectsMalformedStream(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	valid, err := prepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]*pb.SnapshotResponse) []*pb.SnapshotResponse
	}{
		{"truncated", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			return frames[:len(frames)-1]
		}},
		{"missing metadata", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().ReceiptMetadata = nil
			return frames
		}},
		{"short epoch", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().GetReceiptMetadata().GetPolicy().DeploymentEpoch = []byte{1}
			return frames
		}},
		{"clock high-water beyond cutoff", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			cutoff := frames[0].GetHeader().GetCutoffHlc().GetWallNs() / int64(time.Millisecond)
			frames[0].GetHeader().GetReceiptMetadata().ClockHighWaterUnixMs = uint64(cutoff + 1)
			return frames
		}},
		{"unknown top-level field", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			return frames
		}},
		{"unknown nested header field", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().GetReceiptMetadata().GetPolicy().ProtoReflect().
				SetUnknown([]byte{0xa0, 0x06, 0x01})
			return frames
		}},
		{"unknown nested receipt field", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1].GetReceipt().GetContribution().ProtoReflect().
				SetUnknown([]byte{0xa0, 0x06, 0x01})
			return frames
		}},
		{"unknown nested body field", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			return frames
		}},
		{"receipt after graph", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1], frames[2] = frames[2], frames[1]
			return frames
		}},
		{"receipt count", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().ReceiptCount++
			return frames
		}},
		{"origin count", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().ReceiptOriginCount++
			return frames
		}},
		{"unknown receipt kind", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1].GetReceipt().Kind = pb.SnapshotReceiptKind(99)
			return frames
		}},
		{"missing Add contribution", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1].GetReceipt().Contribution = nil
			return frames
		}},
		{"nil live vertex", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().Vertex = nil
			return frames
		}},
		{"typed-nil frame entry", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].Entry = (*pb.SnapshotResponse_Vertex)(nil)
			return frames
		}},
		{"empty live vertex key", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Key = ""
			return frames
		}},
		{"invalid live vertex timestamp", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Expiration = &timestamppb.Timestamp{Seconds: 253402300800}
			return frames
		}},
		{"invalid live vertex HLC", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetHlc().NodeId = make([]byte, 16)
			return frames
		}},
		{"live vertex HLC beyond cutoff", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetHlc().WallNs++
			return frames
		}},
		{"live vertex HLC from unknown origin", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetHlc().NodeId = append([]byte{0x66}, make([]byte, 15)...)
			return frames
		}},
		{"live vertex HLC beyond origin frontier", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().GetReceiptMetadata().GetOriginCutoffs()[0].GetLastHlc().WallNs--
			return frames
		}},
		{"typed-nil timestamp value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Timestamp)(nil)
			return frames
		}},
		{"typed-nil duration value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Duration)(nil)
			return frames
		}},
		{"typed-nil nil value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Nil)(nil)
			return frames
		}},
		{"typed-nil float64 value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Float64)(nil)
			return frames
		}},
		{"typed-nil float32 value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Float32)(nil)
			return frames
		}},
		{"typed-nil int32 value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Int32)(nil)
			return frames
		}},
		{"typed-nil int64 value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Int64)(nil)
			return frames
		}},
		{"typed-nil uint32 value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Uint32)(nil)
			return frames
		}},
		{"typed-nil uint64 value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Uint64)(nil)
			return frames
		}},
		{"typed-nil bool value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Bool)(nil)
			return frames
		}},
		{"typed-nil string value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_String_)(nil)
			return frames
		}},
		{"typed-nil bytes value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = (*pb.Vertex_Bytes)(nil)
			return frames
		}},
		{"duplicate live vertex", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			duplicate := proto.Clone(frames[2]).(*pb.SnapshotResponse)
			frames[len(frames)-1].GetFooter().VertexCount++
			return insertReceiptSnapshotFrames(frames, len(frames)-1, duplicate)
		}},
		{"nil live edge", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().EdgeCount++
			return insertReceiptSnapshotFrames(frames, len(frames)-1, &pb.SnapshotResponse{
				Entry: &pb.SnapshotResponse_Edge{},
			})
		}},
		{"duplicate live edge", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().EdgeCount += 2
			return insertReceiptSnapshotFrames(
				frames,
				len(frames)-1,
				receiptSnapshotPutEdgeFrame("live", "live"),
				receiptSnapshotPutEdgeFrame("live", "live"),
			)
		}},
		{"malformed Add contribution ID", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			edge := receiptSnapshotPutEdgeFrame("live", "live")
			edge.GetEdge().Contributions[0].ContribId = []byte{1}
			edge.GetEdge().Contributions[0].Hlc =
				proto.Clone(frames[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			frames[len(frames)-1].GetFooter().EdgeCount++
			return insertReceiptSnapshotFrames(frames, len(frames)-1, edge)
		}},
		{"Add contribution HLC beyond cutoff", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			stamp := proto.Clone(frames[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			stamp.WallNs++
			edge := receiptSnapshotPutEdgeFrame("live", "live")
			edge.GetEdge().Contributions[0].ContribId = make([]byte, 24)
			edge.GetEdge().Contributions[0].ContribId[0] = 1
			edge.GetEdge().Contributions[0].Hlc = stamp
			frames[len(frames)-1].GetFooter().EdgeCount++
			return insertReceiptSnapshotFrames(frames, len(frames)-1, edge)
		}},
		{"missing edge endpoint", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().EdgeCount++
			return insertReceiptSnapshotFrames(
				frames,
				len(frames)-1,
				receiptSnapshotPutEdgeFrame("live", "missing"),
			)
		}},
		{"causal barrier and tombstone overlap", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			stamp := proto.Clone(frames[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			frames[len(frames)-1].GetFooter().VertexCausalBarrierCount++
			frames[len(frames)-1].GetFooter().VertexTombstoneCount++
			return insertReceiptSnapshotFrames(
				frames,
				2,
				&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexCausalBarrier{
					VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{
						Key: "live",
						Hlc: stamp,
					},
				}},
				&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexTombstone{
					VertexTombstone: &pb.SnapshotVertexTombstone{
						Key:        "live",
						Hlc:        proto.Clone(stamp).(*pb.HLCTimestamp),
						Expiration: timestamppb.New(time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)),
					},
				}},
			)
		}},
		{"live vertex and tombstone overlap", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			stamp := proto.Clone(frames[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			frames[len(frames)-1].GetFooter().VertexTombstoneCount++
			return insertReceiptSnapshotFrames(frames, 2, &pb.SnapshotResponse{
				Entry: &pb.SnapshotResponse_VertexTombstone{
					VertexTombstone: &pb.SnapshotVertexTombstone{
						Key:        "live",
						Hlc:        stamp,
						Expiration: timestamppb.New(time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)),
					},
				},
			})
		}},
		{"live vertex older than causal barrier", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			stamp := proto.Clone(frames[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			frames[2].GetVertex().GetHlc().WallNs--
			frames[len(frames)-1].GetFooter().VertexCausalBarrierCount++
			return insertReceiptSnapshotFrames(frames, 2, &pb.SnapshotResponse{
				Entry: &pb.SnapshotResponse_VertexCausalBarrier{
					VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "live", Hlc: stamp},
				},
			})
		}},
		{"origin HLC beyond cutoff", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[0].GetHeader().GetReceiptMetadata().GetOriginCutoffs()[0].GetLastHlc().WallNs++
			return frames
		}},
		{"oversized receipt", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[1].GetReceipt().OriginalResult = make([]byte, receiptSnapshotMaxFrameBytes)
			return frames
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := tc.mutate(cloneReceiptSnapshotFrames(valid))
			if err := validateReceiptSnapshotFrames(frames); err == nil {
				t.Fatal("malformed receipt Snapshot was accepted")
			}
		})
	}
}

func TestPrepareReceiptSnapshotFramesRejectsMalformedCapture(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	for _, tc := range []struct {
		name   string
		mutate func(*ReceiptWholeStateCapture)
	}{
		{"missing footer", func(c *ReceiptWholeStateCapture) { c.Graph = c.Graph[:len(c.Graph)-1] }},
		{"graph footer count", func(c *ReceiptWholeStateCapture) { c.Graph[len(c.Graph)-1].GetFooter().VertexCount++ }},
		{"graph receipt metadata", func(c *ReceiptWholeStateCapture) {
			c.Graph[0].GetHeader().ReceiptMetadata = &pb.SnapshotReceiptMetadata{}
		}},
		{"clock high-water beyond cutoff", func(c *ReceiptWholeStateCapture) {
			cutoff := c.Graph[0].GetHeader().GetCutoffHlc().GetWallNs() / int64(time.Millisecond)
			c.Receipts.ClockHighWaterMillis = cutoff + 1
			c.Policy.ClockHighWater = time.UnixMilli(cutoff + 1)
		}},
		{"unknown nested graph field", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetVertex().ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"nil live vertex", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().Vertex = nil
		}},
		{"invalid live vertex HLC", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetHlc().NodeId = make([]byte, 16)
		}},
		{"live vertex HLC beyond cutoff", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetHlc().WallNs++
		}},
		{"live vertex HLC from unknown origin", func(c *ReceiptWholeStateCapture) {
			unknown := hlc.NodeID{0x66}
			c.Graph[1].GetVertex().GetHlc().NodeId = unknown[:]
		}},
		{"live vertex HLC beyond origin frontier", func(c *ReceiptWholeStateCapture) {
			c.Origins[0].LastHLC.WallNs--
		}},
		{"typed-nil timestamp value", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetVertex().Value = (*pb.Vertex_Timestamp)(nil)
		}},
		{"typed-nil duration value", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetVertex().Value = (*pb.Vertex_Duration)(nil)
		}},
		{"typed-nil nil value", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetVertex().Value = (*pb.Vertex_Nil)(nil)
		}},
		{"duplicate live vertex", func(c *ReceiptWholeStateCapture) {
			duplicate := proto.Clone(c.Graph[1]).(*pb.SnapshotResponse)
			c.Graph[len(c.Graph)-1].GetFooter().VertexCount++
			c.Graph = insertReceiptSnapshotFrames(c.Graph, len(c.Graph)-1, duplicate)
		}},
		{"malformed live edge contribution", func(c *ReceiptWholeStateCapture) {
			edge := receiptSnapshotPutEdgeFrame("live", "live")
			edge.GetEdge().Contributions[0].ContribId = []byte{1}
			edge.GetEdge().Contributions[0].Hlc =
				proto.Clone(c.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			c.Graph[len(c.Graph)-1].GetFooter().EdgeCount++
			c.Graph = insertReceiptSnapshotFrames(c.Graph, len(c.Graph)-1, edge)
		}},
		{"Add contribution HLC beyond cutoff", func(c *ReceiptWholeStateCapture) {
			stamp := proto.Clone(c.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			stamp.WallNs++
			edge := receiptSnapshotPutEdgeFrame("live", "live")
			edge.GetEdge().Contributions[0].ContribId = make([]byte, 24)
			edge.GetEdge().Contributions[0].ContribId[0] = 1
			edge.GetEdge().Contributions[0].Hlc = stamp
			c.Graph[len(c.Graph)-1].GetFooter().EdgeCount++
			c.Graph = insertReceiptSnapshotFrames(c.Graph, len(c.Graph)-1, edge)
		}},
		{"missing edge endpoint", func(c *ReceiptWholeStateCapture) {
			c.Graph[len(c.Graph)-1].GetFooter().EdgeCount++
			c.Graph = insertReceiptSnapshotFrames(
				c.Graph,
				len(c.Graph)-1,
				receiptSnapshotPutEdgeFrame("live", "missing"),
			)
		}},
		{"live vertex and tombstone overlap", func(c *ReceiptWholeStateCapture) {
			stamp := proto.Clone(c.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			c.Graph[len(c.Graph)-1].GetFooter().VertexTombstoneCount++
			c.Graph = insertReceiptSnapshotFrames(c.Graph, 1, &pb.SnapshotResponse{
				Entry: &pb.SnapshotResponse_VertexTombstone{
					VertexTombstone: &pb.SnapshotVertexTombstone{
						Key:        "live",
						Hlc:        stamp,
						Expiration: timestamppb.New(time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)),
					},
				},
			})
		}},
		{"live vertex older than causal barrier", func(c *ReceiptWholeStateCapture) {
			stamp := proto.Clone(c.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			c.Graph[1].GetVertex().GetHlc().WallNs--
			c.Graph[len(c.Graph)-1].GetFooter().VertexCausalBarrierCount++
			c.Graph = insertReceiptSnapshotFrames(c.Graph, 1, &pb.SnapshotResponse{
				Entry: &pb.SnapshotResponse_VertexCausalBarrier{
					VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "live", Hlc: stamp},
				},
			})
		}},
		{"policy capacity", func(c *ReceiptWholeStateCapture) { c.Policy.MaxBytes++ }},
		{"origin cutoff", func(c *ReceiptWholeStateCapture) { c.Origins[0].LastSeq++ }},
		{"origin HLC beyond cutoff", func(c *ReceiptWholeStateCapture) { c.Origins[0].LastHLC.WallNs++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := ReceiptWholeStateCapture{
				Graph: cloneReceiptSnapshotFrames(capture.Graph), Receipts: capture.Receipts,
				Policy: capture.Policy, Origins: append([]OriginState(nil), capture.Origins...),
			}
			tc.mutate(&bad)
			if frames, err := prepareReceiptSnapshotFrames(bad, policy); err == nil || frames != nil {
				t.Fatalf("malformed capture produced %d frames: %v", len(frames), err)
			}
		})
	}
}
