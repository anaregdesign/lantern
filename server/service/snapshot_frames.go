package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

const receiptSnapshotMaxFrameBytes = 8 << 20

// replicationSnapshotCut groups data copied under a publication cut. The
// receipt capture clones mutable Vertex payloads before releasing its cut;
// the existing graph-only RPC retains its current copy behavior.
type replicationSnapshotCut struct {
	cutoffPerOrigin map[string]uint64
	cutoffHLC       hlc.Timestamp
	cutoffLocalSeq  uint64
	barriers        graphcache.CausalBarrierSnapshot[string]
	tombstones      graphcache.TombstoneSnapshot[string]
	graph           graphcache.GraphSnapshot[string, *pb.Vertex]
}

// sendSnapshotFrames projects a detached graph cut into graph frames. A
// RECEIPT_V1 capture uses these frames as an intermediate representation;
// prepareReceiptSnapshotFrames adds the receipt metadata and rows before the
// public producer sends anything.
func sendSnapshotFrames(ctx context.Context, cut replicationSnapshotCut, format pb.SnapshotFormat, stream Sender[pb.SnapshotResponse]) error {
	cutoffPerOrigin, cutoffHLC, cutoffLocalSeq := cut.cutoffPerOrigin, cut.cutoffHLC, cut.cutoffLocalSeq
	barriers, tombstones, graph := cut.barriers, cut.tombstones, cut.graph
	header := &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Header{
			Header: &pb.SnapshotHeader{
				CutoffSeqPerOrigin: cutoffPerOrigin,
				CutoffHlc:          hlcToProto(cutoffHLC),
				CutoffLocalSeq:     cutoffLocalSeq,
				Format:             format,
			},
		},
	}
	if err := stream.Send(header); err != nil {
		return err
	}
	var vertexBarrierCount uint64
	for _, barrier := range barriers.Vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_VertexCausalBarrier{
				VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{
					Key: barrier.Key,
					Hlc: hlcToProto(barrier.HLC),
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		vertexBarrierCount++
	}

	var edgeBarrierCount uint64
	for _, barrier := range barriers.Edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_EdgeCausalBarrier{
				EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{
					Tail: barrier.Tail,
					Head: barrier.Head,
					Hlc:  hlcToProto(barrier.HLC),
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		edgeBarrierCount++
	}

	var vertexTombstoneCount uint64
	for _, tombstone := range tombstones.Vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if err := stream.Send(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexTombstone{
			VertexTombstone: &pb.SnapshotVertexTombstone{
				Key: tombstone.Key, Hlc: hlcToProto(tombstone.HLC),
				Expiration: timestamppb.New(tombstone.Expiration),
			},
		}}); err != nil {
			return err
		}
		vertexTombstoneCount++
	}
	var edgeTombstoneCount uint64
	for _, tombstone := range tombstones.Edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if err := stream.Send(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeTombstone{
			EdgeTombstone: &pb.SnapshotEdgeTombstone{
				Tail: tombstone.Tail, Head: tombstone.Head,
				Hlc: hlcToProto(tombstone.HLC), Expiration: timestamppb.New(tombstone.Expiration),
			},
		}}); err != nil {
			return err
		}
		edgeTombstoneCount++
	}

	var vertexCount uint64
	vertices := graph.Vertices
	for _, v := range vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_Vertex{
				Vertex: &pb.SnapshotVertex{
					Vertex: replicationSnapshotVertex(v),
					Hlc:    hlcToProto(v.HLC),
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		vertexCount++
	}

	var edgeCount uint64
	edges := graph.Edges
	for _, e := range edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		contribs := make([]*pb.SnapshotEdgeContribution, 0, len(e.Contributions))
		for _, c := range e.Contributions {
			contribs = append(contribs, &pb.SnapshotEdgeContribution{
				Weight:     c.Weight,
				Expiration: timestamppb.New(c.Expiration),
				ContribId:  contribIDBytes(c.ContribID),
				Hlc:        hlcToProto(c.HLC),
			})
		}
		entry := &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_Edge{
				Edge: &pb.SnapshotEdge{
					Tail:          e.Tail,
					Head:          e.Head,
					Hlc:           hlcToProto(e.HLC),
					Contributions: contribs,
				},
			},
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
		edgeCount++
	}

	footer := &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Footer{
			Footer: &pb.SnapshotFooter{
				VertexCount:              vertexCount,
				EdgeCount:                edgeCount,
				VertexCausalBarrierCount: vertexBarrierCount,
				EdgeCausalBarrierCount:   edgeBarrierCount,
				VertexTombstoneCount:     vertexTombstoneCount,
				EdgeTombstoneCount:       edgeTombstoneCount,
			},
		},
	}
	return stream.Send(footer)
}

// prepareReceiptSnapshotFrames converts one detached publication cut into a
// complete RECEIPT_V1 stream. It validates and owns the full frame sequence
// before the caller sends the header, so malformed source data cannot expose a
// success-shaped partial image.
func prepareReceiptSnapshotFrames(capture ReceiptWholeStateCapture, requested mutationreceipt.Config) ([]*pb.SnapshotResponse, error) {
	if err := validateReceiptSnapshotCapture(capture, requested); err != nil {
		return nil, err
	}

	headerFrame := proto.Clone(capture.Graph[0]).(*pb.SnapshotResponse)
	header := headerFrame.GetHeader()
	header.ReceiptMetadata = &pb.SnapshotReceiptMetadata{
		Policy: &pb.ReceiptPolicy{
			DeploymentEpoch: append([]byte(nil), capture.Receipts.Epoch[:]...),
			Fingerprint:     append([]byte(nil), capture.Receipts.PolicyFingerprint[:]...),
			RetentionMs:     uint64(capture.Policy.Retention / time.Millisecond),
			MaxEntries:      uint64(capture.Policy.MaxEntries),
			MaxBytes:        capture.Policy.MaxBytes,
		},
		ClockHighWaterUnixMs: uint64(capture.Receipts.ClockHighWaterMillis),
		OriginCutoffs:        make([]*pb.OriginState, len(capture.Origins)),
	}
	for i, origin := range capture.Origins {
		header.ReceiptMetadata.OriginCutoffs[i] = &pb.OriginState{
			Origin:  append([]byte(nil), origin.Origin[:]...),
			LastSeq: origin.LastSeq,
			LastHlc: hlcToProto(origin.LastHLC),
		}
	}

	frames := make([]*pb.SnapshotResponse, 0, len(capture.Graph)+len(capture.Receipts.Receipts))
	frames = append(frames, headerFrame)
	for _, receipt := range capture.Receipts.Receipts {
		row, err := receiptSnapshotRow(receipt)
		if err != nil {
			return nil, err
		}
		frames = append(frames, &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_Receipt{Receipt: row},
		})
	}
	for _, frame := range capture.Graph[1 : len(capture.Graph)-1] {
		frames = append(frames, proto.Clone(frame).(*pb.SnapshotResponse))
	}
	footerFrame := proto.Clone(capture.Graph[len(capture.Graph)-1]).(*pb.SnapshotResponse)
	footerFrame.GetFooter().ReceiptCount = uint64(len(capture.Receipts.Receipts))
	footerFrame.GetFooter().ReceiptOriginCount = uint64(len(capture.Origins))
	frames = append(frames, footerFrame)

	if err := validateReceiptSnapshotFrames(frames); err != nil {
		return nil, err
	}
	return frames, nil
}

func validateReceiptSnapshotCapture(capture ReceiptWholeStateCapture, requested mutationreceipt.Config) error {
	if _, err := mutationreceipt.New(requested); err != nil {
		return fmt.Errorf("receipt Snapshot has invalid requested policy: %w", err)
	}
	if capture.Policy.Epoch != requested.Epoch ||
		capture.Policy.Retention != requested.Retention ||
		capture.Policy.MaxEntries != requested.MaxEntries ||
		capture.Policy.MaxBytes != requested.MaxBytes ||
		(!requested.ClockHighWater.IsZero() &&
			requested.ClockHighWater.UnixMilli() > capture.Receipts.ClockHighWaterMillis) {
		return fmt.Errorf("receipt Snapshot capture policy differs from requested policy: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	if capture.Policy.ClockHighWater.UnixMilli() != capture.Receipts.ClockHighWaterMillis {
		return fmt.Errorf("receipt Snapshot clock high-water mismatch: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	if _, err := mutationreceipt.NewFromSnapshot(capture.Policy, capture.Receipts); err != nil {
		return fmt.Errorf("receipt Snapshot has invalid Store state: %w", err)
	}
	if err := validateReceiptSnapshotGraphCapture(capture.Graph, capture.Origins); err != nil {
		return err
	}
	return nil
}

func validateReceiptSnapshotGraphCapture(frames []*pb.SnapshotResponse, origins []OriginState) error {
	if len(frames) < 2 || frames[0] == nil || frames[0].GetHeader() == nil ||
		frames[len(frames)-1] == nil || frames[len(frames)-1].GetFooter() == nil {
		return fmt.Errorf("receipt Snapshot graph capture lacks header or footer")
	}
	header := frames[0].GetHeader()
	footer := frames[len(frames)-1].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		header.GetReceiptMetadata() != nil ||
		footer.GetReceiptCount() != 0 || footer.GetReceiptOriginCount() != 0 {
		return fmt.Errorf("receipt Snapshot graph capture has invalid receipt framing")
	}
	if _, ok := receiptSnapshotHLC(header.GetCutoffHlc()); !ok {
		return fmt.Errorf("receipt Snapshot graph capture has invalid cutoff HLC")
	}
	if err := validateReceiptSnapshotOrigins(header, origins); err != nil {
		return err
	}
	var counts [6]uint64
	phase := 0
	for _, frame := range frames[1 : len(frames)-1] {
		if frame == nil || proto.Size(frame) > receiptSnapshotMaxFrameBytes {
			return fmt.Errorf("receipt Snapshot graph frame is nil or exceeds %d bytes", receiptSnapshotMaxFrameBytes)
		}
		rank := snapshotGraphFrameRank(frame)
		if rank == 0 || rank < phase {
			return fmt.Errorf("receipt Snapshot graph frames are malformed or reordered")
		}
		phase = rank
		counts[rank-1]++
	}
	if counts != [6]uint64{
		footer.GetVertexCausalBarrierCount(),
		footer.GetEdgeCausalBarrierCount(),
		footer.GetVertexTombstoneCount(),
		footer.GetEdgeTombstoneCount(),
		footer.GetVertexCount(),
		footer.GetEdgeCount(),
	} {
		return fmt.Errorf("receipt Snapshot graph footer count mismatch")
	}
	return nil
}

func snapshotGraphFrameRank(frame *pb.SnapshotResponse) int {
	switch frame.GetEntry().(type) {
	case *pb.SnapshotResponse_VertexCausalBarrier:
		return 1
	case *pb.SnapshotResponse_EdgeCausalBarrier:
		return 2
	case *pb.SnapshotResponse_VertexTombstone:
		return 3
	case *pb.SnapshotResponse_EdgeTombstone:
		return 4
	case *pb.SnapshotResponse_Vertex:
		return 5
	case *pb.SnapshotResponse_Edge:
		return 6
	default:
		return 0
	}
}

func receiptSnapshotRow(receipt mutationreceipt.Receipt) (*pb.SnapshotReceipt, error) {
	kind, err := receiptSnapshotKind(receipt.Kind)
	if err != nil {
		return nil, err
	}
	row := &pb.SnapshotReceipt{
		OperationId:    receipt.ID.Bytes(),
		LogicalCallId:  append([]byte(nil), receipt.Group[:]...),
		ItemIndex:      receipt.Index,
		ItemCount:      receipt.Count,
		Kind:           kind,
		IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
		DeadlineUnixMs: uint64(receipt.DeadlineMillis),
		OriginalResult: append([]byte(nil), receipt.Result...),
	}
	if receipt.HasContrib {
		row.Contribution = &pb.SnapshotReceiptContribution{
			ContributionId: append([]byte(nil), receipt.ContribID[:]...),
		}
	}
	return row, nil
}

func receiptSnapshotKind(kind mutationreceipt.Kind) (pb.SnapshotReceiptKind, error) {
	switch kind {
	case mutationreceipt.PutVertex:
		return pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_PUT_VERTEX, nil
	case mutationreceipt.PutEdge:
		return pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_PUT_EDGE, nil
	case mutationreceipt.AddEdge:
		return pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_ADD_EDGE, nil
	case mutationreceipt.DeleteVertex:
		return pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_VERTEX, nil
	case mutationreceipt.DeleteEdge:
		return pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_EDGE, nil
	default:
		return pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_UNSPECIFIED,
			fmt.Errorf("receipt Snapshot has unknown receipt kind %d", kind)
	}
}

func receiptKindFromSnapshot(kind pb.SnapshotReceiptKind) (mutationreceipt.Kind, error) {
	switch kind {
	case pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_PUT_VERTEX:
		return mutationreceipt.PutVertex, nil
	case pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_PUT_EDGE:
		return mutationreceipt.PutEdge, nil
	case pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_ADD_EDGE:
		return mutationreceipt.AddEdge, nil
	case pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_VERTEX:
		return mutationreceipt.DeleteVertex, nil
	case pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_EDGE:
		return mutationreceipt.DeleteEdge, nil
	default:
		return 0, fmt.Errorf("receipt Snapshot has unknown receipt kind %d", kind)
	}
}

// validateReceiptSnapshotFrames is the producer's final preflight. It mirrors
// the future receiver's structural obligations without installing any state.
func validateReceiptSnapshotFrames(frames []*pb.SnapshotResponse) error {
	if len(frames) < 2 || frames[0] == nil || frames[0].GetHeader() == nil ||
		frames[len(frames)-1] == nil || frames[len(frames)-1].GetFooter() == nil {
		return fmt.Errorf("receipt Snapshot stream lacks header or footer")
	}
	for _, frame := range frames {
		if frame == nil || proto.Size(frame) > receiptSnapshotMaxFrameBytes {
			return fmt.Errorf("receipt Snapshot frame is nil or exceeds %d bytes", receiptSnapshotMaxFrameBytes)
		}
	}
	header := frames[0].GetHeader()
	footer := frames[len(frames)-1].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		header.GetReceiptMetadata() == nil || header.GetReceiptMetadata().GetPolicy() == nil {
		return fmt.Errorf("receipt Snapshot header metadata is missing")
	}
	if _, ok := receiptSnapshotHLC(header.GetCutoffHlc()); !ok {
		return fmt.Errorf("receipt Snapshot cutoff HLC is invalid")
	}

	config, state, err := receiptSnapshotStoreState(header.GetReceiptMetadata())
	if err != nil {
		return err
	}
	if err := validateReceiptSnapshotWireOrigins(header); err != nil {
		return err
	}

	var counts [6]uint64
	var receiptCount uint64
	phase := 0
	for _, frame := range frames[1 : len(frames)-1] {
		if row := frame.GetReceipt(); row != nil {
			if phase != 0 {
				return fmt.Errorf("receipt Snapshot receipt row follows graph data")
			}
			receipt, err := receiptFromSnapshotRow(row)
			if err != nil {
				return err
			}
			state.Receipts = append(state.Receipts, receipt)
			receiptCount++
			continue
		}
		rank := snapshotGraphFrameRank(frame)
		if rank == 0 || rank < phase {
			return fmt.Errorf("receipt Snapshot body frame is malformed or reordered")
		}
		phase = rank
		counts[rank-1]++
	}
	if counts != [6]uint64{
		footer.GetVertexCausalBarrierCount(),
		footer.GetEdgeCausalBarrierCount(),
		footer.GetVertexTombstoneCount(),
		footer.GetEdgeTombstoneCount(),
		footer.GetVertexCount(),
		footer.GetEdgeCount(),
	} || receiptCount != footer.GetReceiptCount() ||
		uint64(len(header.GetReceiptMetadata().GetOriginCutoffs())) != footer.GetReceiptOriginCount() {
		return fmt.Errorf("receipt Snapshot footer count mismatch")
	}
	if _, err := mutationreceipt.NewFromSnapshot(config, state); err != nil {
		return fmt.Errorf("receipt Snapshot rows are invalid: %w", err)
	}
	return nil
}

func receiptSnapshotStoreState(metadata *pb.SnapshotReceiptMetadata) (mutationreceipt.Config, mutationreceipt.Snapshot, error) {
	policy := metadata.GetPolicy()
	maxInt := uint64(^uint(0) >> 1)
	if len(policy.GetDeploymentEpoch()) != len(mutationreceipt.Epoch{}) ||
		len(policy.GetFingerprint()) != 32 ||
		policy.GetRetentionMs() > uint64(math.MaxInt64/int64(time.Millisecond)) ||
		policy.GetMaxEntries() == 0 || policy.GetMaxEntries() > maxInt ||
		policy.GetMaxBytes() == 0 ||
		metadata.GetClockHighWaterUnixMs() > math.MaxInt64 {
		return mutationreceipt.Config{}, mutationreceipt.Snapshot{}, fmt.Errorf("receipt Snapshot policy metadata is invalid")
	}
	var epoch mutationreceipt.Epoch
	var fingerprint [32]byte
	copy(epoch[:], policy.GetDeploymentEpoch())
	copy(fingerprint[:], policy.GetFingerprint())
	highWater := int64(metadata.GetClockHighWaterUnixMs())
	config := mutationreceipt.Config{
		Epoch:          epoch,
		Retention:      time.Duration(policy.GetRetentionMs()) * time.Millisecond,
		MaxEntries:     int(policy.GetMaxEntries()),
		MaxBytes:       policy.GetMaxBytes(),
		ClockHighWater: time.UnixMilli(highWater),
	}
	state := mutationreceipt.Snapshot{
		Version:              1,
		Epoch:                epoch,
		PolicyFingerprint:    fingerprint,
		ClockHighWaterMillis: highWater,
	}
	return config, state, nil
}

func receiptFromSnapshotRow(row *pb.SnapshotReceipt) (mutationreceipt.Receipt, error) {
	if row == nil || len(row.GetOperationId()) != len(mutationreceipt.ID{}) ||
		len(row.GetLogicalCallId()) != len(mutationreceipt.GroupID{}) ||
		len(row.GetIntentSha256()) != 32 || row.GetDeadlineUnixMs() > math.MaxInt64 {
		return mutationreceipt.Receipt{}, fmt.Errorf("receipt Snapshot row metadata is invalid")
	}
	kind, err := receiptKindFromSnapshot(row.GetKind())
	if err != nil {
		return mutationreceipt.Receipt{}, err
	}
	receipt := mutationreceipt.Receipt{
		Intent: mutationreceipt.Intent{
			Index: row.GetItemIndex(),
			Count: row.GetItemCount(),
			Kind:  kind,
		},
		Result:         append([]byte(nil), row.GetOriginalResult()...),
		DeadlineMillis: int64(row.GetDeadlineUnixMs()),
	}
	copy(receipt.ID[:], row.GetOperationId())
	copy(receipt.Group[:], row.GetLogicalCallId())
	copy(receipt.Digest[:], row.GetIntentSha256())
	if row.GetContribution() != nil {
		if len(row.GetContribution().GetContributionId()) != len(mutationreceipt.ContribID{}) {
			return mutationreceipt.Receipt{}, fmt.Errorf("receipt Snapshot contribution metadata is invalid")
		}
		receipt.HasContrib = true
		copy(receipt.ContribID[:], row.GetContribution().GetContributionId())
	}
	return receipt, nil
}

func validateReceiptSnapshotOrigins(header *pb.SnapshotHeader, origins []OriginState) error {
	if len(header.GetCutoffSeqPerOrigin()) != len(origins) {
		return fmt.Errorf("receipt Snapshot origin cutoffs do not match graph header")
	}
	var previous hlc.NodeID
	cutoff, _ := receiptSnapshotHLC(header.GetCutoffHlc())
	for i, origin := range origins {
		if origin.Origin == (hlc.NodeID{}) || origin.LastSeq == 0 ||
			origin.LastHLC.NodeID != origin.Origin || origin.LastHLC.WallNs <= 0 ||
			(i != 0 && bytes.Compare(previous[:], origin.Origin[:]) >= 0) ||
			cutoff.Less(origin.LastHLC) ||
			header.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin.Origin[:])] != origin.LastSeq {
			return fmt.Errorf("receipt Snapshot origin cutoff is invalid")
		}
		previous = origin.Origin
	}
	return nil
}

func validateReceiptSnapshotWireOrigins(header *pb.SnapshotHeader) error {
	metadata := header.GetReceiptMetadata()
	if len(header.GetCutoffSeqPerOrigin()) != len(metadata.GetOriginCutoffs()) {
		return fmt.Errorf("receipt Snapshot origin metadata count mismatch")
	}
	cutoff, _ := receiptSnapshotHLC(header.GetCutoffHlc())
	var previous hlc.NodeID
	for i, row := range metadata.GetOriginCutoffs() {
		if row == nil || len(row.GetOrigin()) != len(hlc.NodeID{}) || row.GetLastSeq() == 0 {
			return fmt.Errorf("receipt Snapshot origin metadata is invalid")
		}
		var origin hlc.NodeID
		copy(origin[:], row.GetOrigin())
		last, ok := receiptSnapshotHLC(row.GetLastHlc())
		if !ok || origin == (hlc.NodeID{}) || last.NodeID != origin ||
			(i != 0 && bytes.Compare(previous[:], origin[:]) >= 0) ||
			cutoff.Less(last) ||
			header.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin[:])] != row.GetLastSeq() {
			return fmt.Errorf("receipt Snapshot origin metadata is invalid")
		}
		previous = origin
	}
	return nil
}

func receiptSnapshotHLC(stamp *pb.HLCTimestamp) (hlc.Timestamp, bool) {
	if stamp == nil || stamp.GetWallNs() <= 0 || len(stamp.GetNodeId()) != len(hlc.NodeID{}) {
		return hlc.Timestamp{}, false
	}
	var nodeID hlc.NodeID
	copy(nodeID[:], stamp.GetNodeId())
	if nodeID == (hlc.NodeID{}) {
		return hlc.Timestamp{}, false
	}
	return hlc.Timestamp{
		WallNs: stamp.GetWallNs(), Logical: stamp.GetLogical(), NodeID: nodeID,
	}, true
}

// replicationSnapshotVertex converts GraphCache's nil value sentinel into the
// public Vertex nil-value arm. Edge writes auto-create endpoint vertices with a
// nil *pb.Vertex payload, but a SnapshotVertex wire frame must remain
// self-describing: the receiver needs both the key and expiration and rejects a
// nil payload as a truncated/corrupt frame. Non-nil values already carry their
// exact public representation and can be streamed unchanged.
func replicationSnapshotVertex(v graphcache.SnapshotVertex[string, *pb.Vertex]) *pb.Vertex {
	if v.Value != nil {
		return v.Value
	}
	vertex := &pb.Vertex{
		Key:   v.Key,
		Value: &pb.Vertex_Nil{Nil: true},
	}
	if !v.Expiration.IsZero() {
		vertex.Expiration = timestamppb.New(v.Expiration)
	}
	return vertex
}
