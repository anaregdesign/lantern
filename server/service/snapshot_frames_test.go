package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"math"
	"reflect"
	"strings"
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
		header.GetReceiptMetadata() != nil || footer.GetActiveReceiptCount() != 0 ||
		footer.GetRetiredEpochCount() != 0 || footer.GetRetiredReceiptCount() != 0 ||
		footer.GetOriginCount() != 0 {
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
	if err := sendSnapshotFrames(context.Background(), cut, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, privateReceipt); err != nil {
		t.Fatal(err)
	}
	if len(privateReceipt.frames) != len(graphOnly.frames) || privateReceipt.frames[0].GetHeader().GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		t.Fatalf("private format changed graph frame shape: %+v", privateReceipt.frames)
	}
	for i := 1; i < len(graphOnly.frames); i++ {
		if !proto.Equal(graphOnly.frames[i], privateReceipt.frames[i]) {
			t.Fatalf("private format changed graph frame %d", i)
		}
	}
}

func TestSendSnapshotFramesDerivedAggregate(t *testing.T) {
	baseExpiration := time.Now().Add(time.Hour)
	addExpiration := baseExpiration.Add(-time.Minute)
	addHLC := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: hlc.NodeID{0x52}}
	addID := graphcache.ContribID{0x52}
	cut := func(weight float32) replicationSnapshotCut {
		return replicationSnapshotCut{graph: graphcache.GraphSnapshot[string, *pb.Vertex]{
			Edges: []graphcache.SnapshotEdge[string]{{
				Tail: "tail", Head: "head",
				Contributions: []graphcache.SnapshotContribution{
					{Weight: weight, Expiration: baseExpiration, DerivedAggregate: true},
					{Weight: 7, Expiration: addExpiration, ContribID: addID, HLC: addHLC},
				},
			}},
		}}
	}
	for _, tc := range []struct {
		name   string
		weight float32
	}{
		{"positive infinity", float32(math.Inf(1))},
		{"negative infinity", float32(math.Inf(-1))},
		{"historical NaN", float32(math.NaN())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &snapshotFrameSink{}
			if err := sendSnapshotFrames(context.Background(), cut(tc.weight), pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, sink); err != nil {
				t.Fatal(err)
			}
			if len(sink.frames) != 3 || sink.frames[2].GetFooter().GetEdgeCount() != 1 {
				t.Fatalf("derived edge frames = %+v", sink.frames)
			}
			edge := sink.frames[1].GetEdge()
			aggregate := edge.GetDerivedAggregate()
			if aggregate == nil || len(edge.GetContributions()) != 0 ||
				math.Float32bits(aggregate.GetWeight()) != math.Float32bits(tc.weight) ||
				!aggregate.GetExpiration().AsTime().Equal(baseExpiration) ||
				len(aggregate.GetAdds()) != 1 {
				t.Fatalf("derived aggregate encoding = %+v", edge)
			}
			add := aggregate.GetAdds()[0]
			if add.GetWeight() != 7 || !add.GetExpiration().AsTime().Equal(addExpiration) ||
				!bytes.Equal(add.GetContribId(), addID[:]) ||
				add.GetHlc().GetWallNs() != addHLC.WallNs {
				t.Fatalf("independent finite Add lost identity, HLC, or TTL: %+v", add)
			}

			receipt := &snapshotFrameSink{}
			err := sendSnapshotFrames(context.Background(), cut(tc.weight), pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, receipt)
			if err == nil || !strings.Contains(err.Error(), "invalid derived aggregate") || len(receipt.frames) != 1 {
				t.Fatalf("receipt Snapshot of derived aggregate = (%v, %d frames)", err, len(receipt.frames))
			}
		})
	}

	for _, tc := range []struct {
		name   string
		change func(*replicationSnapshotCut)
	}{
		{"empty live edge", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions = nil }},
		{"finite forged marker", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions[0].Weight = 7 }},
		{"marked Add identity", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions[0].ContribID = graphcache.ContribID{1} }},
		{"marked Add HLC", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions[0].HLC = addHLC }},
		{"marked edge Put floor", func(c *replicationSnapshotCut) { c.graph.Edges[0].HLC = addHLC }},
		{"duplicate marker", func(c *replicationSnapshotCut) {
			c.graph.Edges[0].Contributions = append(c.graph.Edges[0].Contributions, c.graph.Edges[0].Contributions[0])
		}},
		{"unidentified later Add", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions[1].ContribID = graphcache.ContribID{} }},
		{"later Add without HLC", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions[1].HLC = hlc.Timestamp{} }},
		{"nonfinite later source", func(c *replicationSnapshotCut) { c.graph.Edges[0].Contributions[1].Weight = float32(math.Inf(1)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := cut(float32(math.Inf(1)))
			tc.change(&bad)
			sink := &snapshotFrameSink{}
			if err := sendSnapshotFrames(context.Background(), bad, pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, sink); err == nil || len(sink.frames) != 1 {
				t.Fatalf("malformed captured aggregate emitted %d frames, err=%v", len(sink.frames), err)
			}
		})
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
	retired, err := newEmptyRetiredCatalogSnapshot(policy, receipts.ClockHighWaterMillis)
	if err != nil {
		t.Fatal(err)
	}

	origin := hlc.NodeID{0x65}
	stamp := hlc.Timestamp{WallNs: issued.Add(time.Minute).UnixNano(), Logical: 2, NodeID: origin}
	cut := replicationSnapshotCut{
		cutoffPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 7},
		cutoffHLC:       stamp,
		cutoffLocalSeq:  11,
	}
	if withGraph {
		cut.graph.Vertices = []graphcache.SnapshotVertex[string, *pb.Vertex]{{
			Key: "live", Value: &pb.Vertex{
				Key:   "live",
				Value: &pb.Vertex_Nil{Nil: true},
			}, HLC: stamp,
		}}
	}
	graph := &snapshotFrameSink{}
	if err := sendSnapshotFrames(context.Background(), cut, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, graph); err != nil {
		t.Fatal(err)
	}
	return ReceiptWholeStateCapture{
		Graph: graph.frames, Receipts: receipts, Retired: retired, Policy: policy,
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

func receiptSnapshotTestRetiredMember(
	t *testing.T,
	epoch mutationreceipt.Epoch,
	highWater time.Time,
	nonce byte,
) mutationreceipt.RetiredEpochSnapshot {
	t.Helper()
	policy := mutationreceipt.Config{
		Epoch: epoch, Retention: 2 * time.Hour, MaxEntries: 8, MaxBytes: 1 << 20,
		ClockHighWater: highWater,
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	id, err := mutationreceipt.NewID(epoch, highWater, [24]byte{nonce})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID: id, Group: mutationreceipt.GroupID{nonce}, Count: 1,
		Kind:   mutationreceipt.PutVertex,
		Digest: mutationreceipt.IntentDigest([]byte{nonce}),
	}
	tx, err := store.Begin(highWater)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if class, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil ||
		class != mutationreceipt.Fresh {
		t.Fatalf("Classify retired receipt = (%v, %v)", class, err)
	}
	if err := tx.Reserve([][]byte{{nonce}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return mutationreceipt.RetiredEpochSnapshot{
		Policy: mutationreceipt.RetiredEpochPolicy{
			Epoch: epoch, Retention: policy.Retention,
			MaxEntries: policy.MaxEntries, MaxBytes: policy.MaxBytes,
		},
		State: state,
	}
}

func receiptSnapshotTestCaptureWithRetired(t *testing.T) (ReceiptWholeStateCapture, mutationreceipt.Config) {
	t.Helper()
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	highWater := capture.Receipts.ClockHighWater()
	capture.Retired = mutationreceipt.RetiredCatalogSnapshot{
		Version:              1,
		ClockHighWaterMillis: capture.Receipts.ClockHighWaterMillis,
		Epochs: []mutationreceipt.RetiredEpochSnapshot{
			receiptSnapshotTestRetiredMember(t, mutationreceipt.Epoch{0x11}, highWater, 0x12),
			receiptSnapshotTestRetiredMember(t, mutationreceipt.Epoch{0x41}, highWater, 0x42),
		},
	}
	if _, err := mutationreceipt.NewRetiredCatalogFromSnapshot(
		receiptSnapshotTestCatalogConfig(capture),
		capture.Retired,
	); err != nil {
		t.Fatalf("retired fixture: %v", err)
	}
	return capture, policy
}

func receiptSnapshotTestCatalogConfig(capture ReceiptWholeStateCapture) mutationreceipt.RetiredCatalogConfig {
	return mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    capture.Policy.Epoch,
		MaxEntries:     capture.Policy.MaxEntries,
		MaxBytes:       capture.Policy.MaxBytes,
		ClockHighWater: capture.Receipts.ClockHighWater(),
	}
}

func TestPrepareReceiptSnapshotFramesCarriesCompleteDeterministicCut(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	frames, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 {
		t.Fatalf("frame count = %d, want header/receipt/vertex/footer", len(frames))
	}
	header := frames[0].GetHeader()
	metadata := header.GetReceiptMetadata()
	wirePolicy := metadata.GetActivePolicy()
	row := frames[1].GetReceipt()
	footer := frames[3].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT ||
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
		footer.GetActiveReceiptCount() != 1 || footer.GetRetiredEpochCount() != 0 ||
		footer.GetRetiredReceiptCount() != 0 || footer.GetOriginCount() != 1 ||
		footer.GetVertexCount() != 1 {
		t.Fatalf("receipt body/footer drift: %+v", frames)
	}

	again, err := PrepareReceiptSnapshotFrames(capture, policy)
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

func TestReceiptSnapshotFrameCapacity(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name        string
		graphFrames int
		receiptRows int
		want        int
		wantErr     bool
	}{
		{name: "valid", graphFrames: 2, receiptRows: 3, want: 5},
		{name: "exact platform maximum", graphFrames: maxInt - 1, receiptRows: 1, want: maxInt},
		{name: "overflow", graphFrames: maxInt, receiptRows: 1, wantErr: true},
		{name: "negative graph count", graphFrames: -1, wantErr: true},
		{name: "negative receipt count", receiptRows: -1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := receiptSnapshotFrameCapacity(tt.graphFrames, tt.receiptRows)
			if tt.wantErr {
				if err == nil {
					t.Fatal("receiptSnapshotFrameCapacity() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("receiptSnapshotFrameCapacity() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("receiptSnapshotFrameCapacity() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReceiptSnapshotRetiredCatalogRoundTrip(t *testing.T) {
	capture, policy := receiptSnapshotTestCaptureWithRetired(t)
	frames, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	metadata := frames[0].GetHeader().GetReceiptMetadata()
	footer := frames[len(frames)-1].GetFooter()
	if len(metadata.GetRetiredPolicies()) != 2 ||
		!bytes.Equal(metadata.GetRetiredPolicies()[0].GetDeploymentEpoch(), capture.Retired.Epochs[0].Policy.Epoch[:]) ||
		!bytes.Equal(metadata.GetRetiredPolicies()[1].GetDeploymentEpoch(), capture.Retired.Epochs[1].Policy.Epoch[:]) ||
		footer.GetActiveReceiptCount() != 1 ||
		footer.GetRetiredEpochCount() != 2 ||
		footer.GetRetiredReceiptCount() != 2 ||
		footer.GetOriginCount() != 1 {
		t.Fatalf("retired receipt metadata/footer = %+v / %+v", metadata, footer)
	}
	var ids [][]byte
	for _, frame := range frames[1:] {
		if row := frame.GetReceipt(); row != nil {
			ids = append(ids, row.GetOperationId())
		}
	}
	if len(ids) != 3 ||
		bytes.Compare(ids[0], ids[1]) >= 0 ||
		bytes.Compare(ids[1], ids[2]) >= 0 {
		t.Fatalf("receipt row order = %x", ids)
	}
	decoded, err := DecodeReceiptSnapshotFrames(
		frames,
		policy,
		receiptSnapshotTestCatalogConfig(capture),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Retired, capture.Retired) {
		t.Fatalf("decoded retired catalog = %+v", decoded.Retired)
	}
}

func TestReceiptSnapshotCanonicalizesGraphOrderAndRejectsReordering(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	second := proto.Clone(capture.Graph[1]).(*pb.SnapshotResponse)
	second.GetVertex().Vertex.Key = "aaa"
	capture.Graph = insertReceiptSnapshotFrames(
		capture.Graph,
		len(capture.Graph)-1,
		second,
	)
	capture.Graph[len(capture.Graph)-1].GetFooter().VertexCount++
	frames, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	if frames[2].GetVertex().GetVertex().GetKey() != "aaa" ||
		frames[3].GetVertex().GetVertex().GetKey() != "live" {
		t.Fatalf("canonical graph order = %+v", frames[2:4])
	}
	frames[2], frames[3] = frames[3], frames[2]
	if _, err := DecodeReceiptSnapshotFrames(
		frames,
		policy,
		receiptSnapshotTestCatalogConfig(capture),
	); err == nil {
		t.Fatal("noncanonical graph order was accepted")
	}
}

func TestReceiptSnapshotRejectsMalformedRetiredEvidence(t *testing.T) {
	capture, policy := receiptSnapshotTestCaptureWithRetired(t)
	valid, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]*pb.SnapshotResponse)
	}{
		{"retired policy order", func(frames []*pb.SnapshotResponse) {
			policies := frames[0].GetHeader().GetReceiptMetadata().RetiredPolicies
			policies[0], policies[1] = policies[1], policies[0]
		}},
		{"retired row order", func(frames []*pb.SnapshotResponse) {
			frames[1], frames[2] = frames[2], frames[1]
		}},
		{"retired policy fingerprint", func(frames []*pb.SnapshotResponse) {
			frames[0].GetHeader().GetReceiptMetadata().RetiredPolicies[0].Fingerprint[0] ^= 1
		}},
		{"active epoch contamination", func(frames []*pb.SnapshotResponse) {
			frames[0].GetHeader().GetReceiptMetadata().RetiredPolicies[0].DeploymentEpoch =
				append([]byte(nil), policy.Epoch[:]...)
		}},
		{"duplicate retired epoch", func(frames []*pb.SnapshotResponse) {
			frames[0].GetHeader().GetReceiptMetadata().RetiredPolicies[1] =
				proto.Clone(frames[0].GetHeader().GetReceiptMetadata().RetiredPolicies[0]).(*pb.ReceiptPolicy)
		}},
		{"retired epoch count", func(frames []*pb.SnapshotResponse) {
			frames[len(frames)-1].GetFooter().RetiredEpochCount++
		}},
		{"retired receipt count", func(frames []*pb.SnapshotResponse) {
			frames[len(frames)-1].GetFooter().RetiredReceiptCount++
		}},
		{"unknown retired policy field", func(frames []*pb.SnapshotResponse) {
			frames[0].GetHeader().GetReceiptMetadata().RetiredPolicies[0].ProtoReflect().
				SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := cloneReceiptSnapshotFrames(valid)
			tc.mutate(frames)
			if _, err := DecodeReceiptSnapshotFrames(
				frames,
				policy,
				receiptSnapshotTestCatalogConfig(capture),
			); err == nil {
				t.Fatal("malformed retired receipt evidence was accepted")
			}
		})
	}
}

func TestDecodeReceiptSnapshotFramesReturnsDetachedArchiveCapture(t *testing.T) {
	want, policy := receiptSnapshotTestCapture(t, true, true)
	frames, err := PrepareReceiptSnapshotFrames(want, policy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReceiptSnapshotFrames(
		frames,
		policy,
		receiptSnapshotTestCatalogConfig(want),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Graph) != len(want.Graph) ||
		decoded.Graph[0].GetHeader().GetReceiptMetadata() != nil ||
		decoded.Graph[len(decoded.Graph)-1].GetFooter().GetActiveReceiptCount() != 0 ||
		decoded.Graph[len(decoded.Graph)-1].GetFooter().GetOriginCount() != 0 ||
		len(decoded.Receipts.Receipts) != 1 ||
		len(decoded.Origins) != 1 ||
		decoded.Policy.Epoch != policy.Epoch ||
		decoded.Policy.ClockHighWater.UnixMilli() != decoded.Receipts.ClockHighWaterMillis {
		t.Fatalf("decoded receipt Snapshot capture = %+v", decoded)
	}
	for i := range want.Graph {
		if !proto.Equal(decoded.Graph[i], want.Graph[i]) {
			t.Fatalf("decoded graph frame %d differs from archive capture", i)
		}
	}
	frames[0].GetHeader().CutoffLocalSeq++
	frames[1].GetReceipt().OriginalResult[0] ^= 1
	frames[2].GetVertex().GetVertex().Key = "mutated"
	if decoded.Graph[0].GetHeader().GetCutoffLocalSeq() != 11 ||
		decoded.Receipts.Receipts[0].Result[0] != 0xde ||
		decoded.Graph[1].GetVertex().GetVertex().GetKey() != "live" {
		t.Fatal("decoded receipt Snapshot capture aliases input frames")
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
			frames, err := PrepareReceiptSnapshotFrames(capture, policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != tc.wantFrames || frames[0].GetHeader() == nil ||
				frames[len(frames)-1].GetFooter().GetActiveReceiptCount() != tc.wantReceipts ||
				frames[len(frames)-1].GetFooter().GetVertexCount() != 0 ||
				frames[len(frames)-1].GetFooter().GetEdgeCount() != 0 {
				t.Fatalf("receipt-only stream = %+v", frames)
			}
			if err := validateReceiptSnapshotFrames(
				frames,
				policy,
				receiptSnapshotTestCatalogConfig(capture),
			); err != nil {
				t.Fatalf("valid receipt-only stream: %v", err)
			}
		})
	}
}

func TestValidateReceiptSnapshotFramesRejectsMalformedStream(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	valid, err := PrepareReceiptSnapshotFrames(capture, policy)
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
			frames[0].GetHeader().GetReceiptMetadata().GetActivePolicy().DeploymentEpoch = []byte{1}
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
			frames[0].GetHeader().GetReceiptMetadata().GetActivePolicy().ProtoReflect().
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
			frames[len(frames)-1].GetFooter().ActiveReceiptCount++
			return frames
		}},
		{"origin count", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[len(frames)-1].GetFooter().OriginCount++
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
		{"unset live vertex value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = nil
			return frames
		}},
		{"false nil live vertex value", func(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
			frames[2].GetVertex().GetVertex().Value = &pb.Vertex_Nil{Nil: false}
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
			if err := validateReceiptSnapshotFrames(
				frames,
				policy,
				receiptSnapshotTestCatalogConfig(capture),
			); err == nil {
				t.Fatal("malformed receipt Snapshot was accepted")
			}
		})
	}
	for _, weight := range []struct {
		name  string
		value float32
	}{
		{"NaN", float32(math.NaN())},
		{"positive infinity", float32(math.Inf(1))},
		{"negative infinity", float32(math.Inf(-1))},
	} {
		for _, contribution := range []string{"Put", "Add"} {
			t.Run(contribution+"/"+weight.name, func(t *testing.T) {
				frames := cloneReceiptSnapshotFrames(valid)
				edge := receiptSnapshotPutEdgeFrame("live", "live")
				row := edge.GetEdge().Contributions[0]
				row.Weight = weight.value
				if contribution == "Add" {
					row.ContribId = append([]byte{1}, make([]byte, 23)...)
					row.Hlc = proto.Clone(frames[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
				}
				frames = insertReceiptSnapshotFrames(frames, len(frames)-1, edge)
				frames[len(frames)-1].GetFooter().EdgeCount++
				decoded, err := DecodeReceiptSnapshotFrames(
					frames, policy, receiptSnapshotTestCatalogConfig(capture),
				)
				if err == nil || !strings.Contains(err.Error(), "non-finite live edge contribution weight") ||
					len(decoded.Graph) != 0 {
					t.Fatalf("invalid receipt Snapshot returned graph=%v, err=%v", decoded.Graph, err)
				}
			})
		}
	}
}

func TestReceiptSnapshotPreservesFiniteOverflowSourcesAndNaNResultBits(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	result := []byte{0x7f, 0xc0, 0, 1}
	capture.Receipts.Receipts[0].Result = append([]byte(nil), result...)
	stamp := capture.Graph[0].GetHeader().GetCutoffHlc()
	edge := receiptSnapshotPutEdgeFrame("live", "live")
	edge.GetEdge().Contributions = []*pb.SnapshotEdgeContribution{
		{Weight: math.MaxFloat32, ContribId: append([]byte{1}, make([]byte, 23)...), Hlc: stamp},
		{Weight: math.MaxFloat32, ContribId: append([]byte{2}, make([]byte, 23)...), Hlc: stamp},
	}
	capture.Graph = insertReceiptSnapshotFrames(capture.Graph, len(capture.Graph)-1, edge)
	capture.Graph[len(capture.Graph)-1].GetFooter().EdgeCount++
	frames, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatalf("prepare finite-source receipt Snapshot: %v", err)
	}
	decoded, err := DecodeReceiptSnapshotFrames(frames, policy, receiptSnapshotTestCatalogConfig(capture))
	if err != nil {
		t.Fatalf("decode finite-source receipt Snapshot: %v", err)
	}
	if len(decoded.Receipts.Receipts) != 1 || !bytes.Equal(decoded.Receipts.Receipts[0].Result, result) {
		t.Fatalf("authoritative original result bits changed: %+v", decoded.Receipts.Receipts)
	}
	if len(decoded.Graph) != 4 || len(decoded.Graph[2].GetEdge().GetContributions()) != 2 {
		t.Fatalf("source contributions changed: %+v", decoded.Graph)
	}
	contributions := decoded.Graph[2].GetEdge().GetContributions()
	if contributions[0].GetWeight() != math.MaxFloat32 ||
		contributions[1].GetWeight() != math.MaxFloat32 ||
		!math.IsInf(float64(contributions[0].GetWeight()+contributions[1].GetWeight()), 1) {
		t.Fatalf("finite contributions no longer permit derived +Inf: %+v", contributions)
	}
}

func TestReceiptSnapshotRejectsDerivedAggregate(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	valid, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	edge := receiptSnapshotPutEdgeFrame("live", "live")
	edge.GetEdge().Contributions = nil
	edge.GetEdge().DerivedAggregate = &pb.SnapshotEdgeDerivedAggregate{Weight: float32(math.Inf(1))}
	capture.Graph = insertReceiptSnapshotFrames(capture.Graph, len(capture.Graph)-1, proto.Clone(edge).(*pb.SnapshotResponse))
	capture.Graph[len(capture.Graph)-1].GetFooter().EdgeCount++
	if frames, err := PrepareReceiptSnapshotFrames(capture, policy); err == nil ||
		!strings.Contains(err.Error(), "receipt Snapshot cannot contain a derived edge aggregate") || frames != nil {
		t.Fatalf("prepared receipt Snapshot with derived marker: frames=%v, err=%v", frames, err)
	}
	malformed := insertReceiptSnapshotFrames(valid, len(valid)-1, edge)
	malformed[len(malformed)-1].GetFooter().EdgeCount++
	if decoded, err := DecodeReceiptSnapshotFrames(
		malformed, policy, receiptSnapshotTestCatalogConfig(capture),
	); err == nil || !strings.Contains(err.Error(), "receipt Snapshot cannot contain a derived edge aggregate") ||
		len(decoded.Graph) != 0 {
		t.Fatalf("decoded receipt Snapshot with derived marker: graph=%v, err=%v", decoded.Graph, err)
	}
}

func TestPrepareReceiptSnapshotFramesRejectsMalformedCapture(t *testing.T) {
	capture, policy := receiptSnapshotTestCapture(t, true, true)
	lowerRetired, err := newEmptyRetiredCatalogSnapshot(
		capture.Policy,
		capture.Receipts.ClockHighWaterMillis-1,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ReceiptWholeStateCapture)
	}{
		{"zero retired catalog", func(c *ReceiptWholeStateCapture) {
			c.Retired = mutationreceipt.RetiredCatalogSnapshot{}
		}},
		{"lower retired high-water", func(c *ReceiptWholeStateCapture) {
			c.Retired = lowerRetired
		}},
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
		{"unset live vertex value", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetVertex().Value = nil
		}},
		{"false nil live vertex value", func(c *ReceiptWholeStateCapture) {
			c.Graph[1].GetVertex().GetVertex().Value = &pb.Vertex_Nil{Nil: false}
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
				Retired: capture.Retired, Policy: capture.Policy,
				Origins: append([]OriginState(nil), capture.Origins...),
			}
			tc.mutate(&bad)
			if frames, err := PrepareReceiptSnapshotFrames(bad, policy); err == nil || frames != nil {
				t.Fatalf("malformed capture produced %d frames: %v", len(frames), err)
			}
		})
	}
}

func TestPrepareReceiptSnapshotFramesRejectsMalformedRetiredCapture(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ReceiptWholeStateCapture)
	}{
		{"high-water mismatch", func(c *ReceiptWholeStateCapture) {
			c.Retired.ClockHighWaterMillis++
		}},
		{"policy fingerprint mismatch", func(c *ReceiptWholeStateCapture) {
			c.Retired.Epochs[0].Policy.MaxBytes++
		}},
		{"active epoch contamination", func(c *ReceiptWholeStateCapture) {
			c.Retired.Epochs[0].Policy.Epoch = c.Policy.Epoch
			c.Retired.Epochs[0].State.Epoch = c.Policy.Epoch
		}},
		{"epoch order", func(c *ReceiptWholeStateCapture) {
			c.Retired.Epochs[0], c.Retired.Epochs[1] =
				c.Retired.Epochs[1], c.Retired.Epochs[0]
		}},
		{"duplicate receipt", func(c *ReceiptWholeStateCapture) {
			row := c.Retired.Epochs[0].State.Receipts[0]
			c.Retired.Epochs[0].State.Receipts =
				append(c.Retired.Epochs[0].State.Receipts, row)
		}},
		{"row epoch mismatch", func(c *ReceiptWholeStateCapture) {
			c.Retired.Epochs[0].State.Receipts[0].ID =
				c.Retired.Epochs[1].State.Receipts[0].ID
		}},
		{"empty retired epoch", func(c *ReceiptWholeStateCapture) {
			c.Retired.Epochs[0].State.Receipts = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture, policy := receiptSnapshotTestCaptureWithRetired(t)
			tc.mutate(&capture)
			if frames, err := PrepareReceiptSnapshotFrames(capture, policy); err == nil || frames != nil {
				t.Fatalf("malformed retired capture produced %d frames: %v", len(frames), err)
			}
		})
	}
}
