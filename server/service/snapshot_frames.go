package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
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
			contribution := &pb.SnapshotEdgeContribution{
				Weight:    c.Weight,
				ContribId: contribIDBytes(c.ContribID),
				Hlc:       hlcToProto(c.HLC),
			}
			if !c.Expiration.IsZero() {
				contribution.Expiration = timestamppb.New(c.Expiration)
			}
			contribs = append(contribs, contribution)
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
	if err := ValidateReceiptSnapshotGraphCapture(capture.Graph, capture.Origins); err != nil {
		return err
	}
	cutoff, _ := receiptSnapshotHLC(capture.Graph[0].GetHeader().GetCutoffHlc())
	if capture.Receipts.ClockHighWaterMillis > cutoff.WallNs/int64(time.Millisecond) {
		return fmt.Errorf("receipt Snapshot clock high-water exceeds graph cutoff: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	return nil
}

// ValidateReceiptSnapshotGraphCapture validates the detached graph component
// shared by the receipt Snapshot and private archive producers. It rejects any
// frame sequence that a strict receiver could not install.
func ValidateReceiptSnapshotGraphCapture(frames []*pb.SnapshotResponse, origins []OriginState) error {
	if len(frames) < 2 {
		return fmt.Errorf("receipt Snapshot graph capture lacks header or footer")
	}
	for _, frame := range frames {
		if !validReceiptSnapshotFrameOneofs(frame) {
			return fmt.Errorf("receipt Snapshot graph frame has a nil or unknown oneof")
		}
		if proto.Size(frame) > receiptSnapshotMaxFrameBytes {
			return fmt.Errorf("receipt Snapshot graph frame is nil or exceeds %d bytes", receiptSnapshotMaxFrameBytes)
		}
		if err := rejectProtoUnknownFields(frame.ProtoReflect()); err != nil {
			return fmt.Errorf("receipt Snapshot graph frame %v", err)
		}
	}
	if frames[0].GetHeader() == nil || frames[len(frames)-1].GetFooter() == nil {
		return fmt.Errorf("receipt Snapshot graph capture lacks header or footer")
	}
	header := frames[0].GetHeader()
	footer := frames[len(frames)-1].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		header.GetReceiptMetadata() != nil ||
		footer.GetReceiptCount() != 0 || footer.GetReceiptOriginCount() != 0 {
		return fmt.Errorf("receipt Snapshot graph capture has invalid receipt framing")
	}
	cutoff, ok := receiptSnapshotHLC(header.GetCutoffHlc())
	if !ok {
		return fmt.Errorf("receipt Snapshot graph capture has invalid cutoff HLC")
	}
	originLast, err := validateReceiptSnapshotOrigins(header, origins, cutoff)
	if err != nil {
		return err
	}
	return validateReceiptSnapshotGraphBody(
		frames[1:len(frames)-1],
		footer,
		receiptSnapshotCausalBounds{cutoff: cutoff, originLast: originLast},
	)
}

type receiptSnapshotEdgeKey struct{ tail, head string }

type receiptSnapshotCausalBounds struct {
	cutoff     hlc.Timestamp
	originLast map[hlc.NodeID]hlc.Timestamp
}

func (b receiptSnapshotCausalBounds) parse(stamp *pb.HLCTimestamp) (hlc.Timestamp, bool) {
	value, ok := receiptSnapshotHLC(stamp)
	if !ok || b.cutoff.Less(value) {
		return hlc.Timestamp{}, false
	}
	last, ok := b.originLast[value.NodeID]
	if !ok || last.Less(value) {
		return hlc.Timestamp{}, false
	}
	return value, true
}

func validateReceiptSnapshotGraphBody(
	frames []*pb.SnapshotResponse,
	footer *pb.SnapshotFooter,
	bounds receiptSnapshotCausalBounds,
) error {
	var counts [6]uint64
	phase := 0
	vertexBarriers := make(map[string]hlc.Timestamp)
	edgeBarriers := make(map[receiptSnapshotEdgeKey]hlc.Timestamp)
	vertexTombstones := make(map[string]struct{})
	edgeTombstones := make(map[receiptSnapshotEdgeKey]hlc.Timestamp)
	vertices := make(map[string]struct{})
	edges := make(map[receiptSnapshotEdgeKey]struct{})
	for _, frame := range frames {
		if frame == nil {
			return fmt.Errorf("nil graph frame")
		}
		rank := snapshotGraphFrameRank(frame)
		if rank == 0 || rank < phase {
			return fmt.Errorf("receipt Snapshot graph frames are malformed or reordered")
		}
		phase = rank
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_VertexCausalBarrier:
			barrier := entry.VertexCausalBarrier
			stamp, ok := bounds.parse(barrier.GetHlc())
			if barrier == nil || barrier.GetKey() == "" || !ok {
				return fmt.Errorf("invalid vertex causal barrier")
			}
			if _, exists := vertexBarriers[barrier.GetKey()]; exists {
				return fmt.Errorf("duplicate vertex causal barrier")
			}
			vertexBarriers[barrier.GetKey()] = stamp
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			barrier := entry.EdgeCausalBarrier
			stamp, ok := bounds.parse(barrier.GetHlc())
			if barrier == nil || barrier.GetTail() == "" || barrier.GetHead() == "" ||
				!ok {
				return fmt.Errorf("invalid edge causal barrier")
			}
			key := receiptSnapshotEdgeKey{barrier.GetTail(), barrier.GetHead()}
			if _, exists := edgeBarriers[key]; exists {
				return fmt.Errorf("duplicate edge causal barrier")
			}
			edgeBarriers[key] = stamp
		case *pb.SnapshotResponse_VertexTombstone:
			marker := entry.VertexTombstone
			_, ok := bounds.parse(marker.GetHlc())
			if marker == nil || marker.GetKey() == "" || !ok ||
				!validReceiptSnapshotTimestamp(marker.GetExpiration()) {
				return fmt.Errorf("invalid vertex tombstone")
			}
			if _, exists := vertexTombstones[marker.GetKey()]; exists {
				return fmt.Errorf("duplicate vertex tombstone")
			}
			if _, exists := vertexBarriers[marker.GetKey()]; exists {
				return fmt.Errorf("vertex causal barrier and tombstone overlap")
			}
			vertexTombstones[marker.GetKey()] = struct{}{}
		case *pb.SnapshotResponse_EdgeTombstone:
			marker := entry.EdgeTombstone
			stamp, ok := bounds.parse(marker.GetHlc())
			if marker == nil || marker.GetTail() == "" || marker.GetHead() == "" ||
				!ok ||
				!validReceiptSnapshotTimestamp(marker.GetExpiration()) {
				return fmt.Errorf("invalid edge tombstone")
			}
			key := receiptSnapshotEdgeKey{marker.GetTail(), marker.GetHead()}
			if _, exists := edgeTombstones[key]; exists {
				return fmt.Errorf("duplicate edge tombstone")
			}
			if _, exists := edgeBarriers[key]; exists {
				return fmt.Errorf("edge causal barrier and tombstone overlap")
			}
			edgeTombstones[key] = stamp
		case *pb.SnapshotResponse_Vertex:
			item := entry.Vertex
			if item == nil || item.GetVertex() == nil || item.GetVertex().GetKey() == "" ||
				!validOptionalReceiptSnapshotTimestamp(item.GetVertex().GetExpiration()) ||
				!validReceiptSnapshotVertexValue(item.GetVertex()) {
				return fmt.Errorf("invalid live vertex")
			}
			var liveHLC hlc.Timestamp
			if item.GetHlc() != nil {
				var ok bool
				liveHLC, ok = bounds.parse(item.GetHlc())
				if !ok {
					return fmt.Errorf("invalid live vertex")
				}
			}
			key := item.GetVertex().GetKey()
			if _, exists := vertices[key]; exists {
				return fmt.Errorf("duplicate live vertex")
			}
			if _, exists := vertexTombstones[key]; exists {
				// AddEdge may recreate a structural endpoint after a Vertex
				// Delete. Its canonical nil marker has no Vertex Put HLC and
				// intentionally retains the tombstone floor; every explicit
				// vertex value must be disjoint from that tombstone.
				if item.GetHlc() != nil || !item.GetVertex().GetNil() {
					return fmt.Errorf("live vertex and tombstone overlap")
				}
			}
			if barrier, exists := vertexBarriers[key]; exists {
				if liveHLC.Less(barrier) {
					return fmt.Errorf("live vertex is older than its causal barrier")
				}
			}
			vertices[key] = struct{}{}
		case *pb.SnapshotResponse_Edge:
			item := entry.Edge
			key := receiptSnapshotEdgeKey{item.GetTail(), item.GetHead()}
			putFloor, err := validateReceiptSnapshotEdge(item, edgeTombstones[key], bounds)
			if err != nil {
				return err
			}
			if _, exists := edges[key]; exists {
				return fmt.Errorf("duplicate live edge")
			}
			if barrier, exists := edgeBarriers[key]; exists {
				if putFloor != barrier {
					return fmt.Errorf("live edge Put floor differs from its causal barrier")
				}
			} else if putFloor != (hlc.Timestamp{}) {
				return fmt.Errorf("live edge Put floor lacks a causal barrier")
			}
			if _, exists := vertices[key.tail]; !exists {
				return fmt.Errorf("live edge tail is absent")
			}
			if _, exists := vertices[key.head]; !exists {
				return fmt.Errorf("live edge head is absent")
			}
			edges[key] = struct{}{}
		}
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

func validReceiptSnapshotTimestamp(stamp *timestamppb.Timestamp) bool {
	return stamp != nil && stamp.CheckValid() == nil
}

func validOptionalReceiptSnapshotTimestamp(stamp *timestamppb.Timestamp) bool {
	return stamp == nil || stamp.CheckValid() == nil
}

func validReceiptSnapshotVertexValue(vertex *pb.Vertex) bool {
	switch value := vertex.GetValue().(type) {
	case *pb.Vertex_Timestamp:
		return !nilOneofWrapper(value) && validReceiptSnapshotTimestamp(value.Timestamp)
	case *pb.Vertex_Duration:
		return !nilOneofWrapper(value) && value.Duration != nil && value.Duration.CheckValid() == nil
	case *pb.Vertex_Nil:
		return !nilOneofWrapper(value) && value.Nil
	default:
		return !nilOneofWrapper(value)
	}
}

func nilOneofWrapper(value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

func validReceiptSnapshotFrameOneofs(frame *pb.SnapshotResponse) bool {
	if frame == nil || frame.GetEntry() == nil || nilOneofWrapper(frame.GetEntry()) {
		return false
	}
	switch entry := frame.GetEntry().(type) {
	case *pb.SnapshotResponse_Header:
		return entry.Header != nil
	case *pb.SnapshotResponse_Receipt:
		return entry.Receipt != nil
	case *pb.SnapshotResponse_VertexCausalBarrier:
		return entry.VertexCausalBarrier != nil
	case *pb.SnapshotResponse_EdgeCausalBarrier:
		return entry.EdgeCausalBarrier != nil
	case *pb.SnapshotResponse_VertexTombstone:
		return entry.VertexTombstone != nil
	case *pb.SnapshotResponse_EdgeTombstone:
		return entry.EdgeTombstone != nil
	case *pb.SnapshotResponse_Vertex:
		return entry.Vertex != nil &&
			(entry.Vertex.Vertex == nil || !nilOneofWrapper(entry.Vertex.Vertex.GetValue()))
	case *pb.SnapshotResponse_Edge:
		return entry.Edge != nil
	case *pb.SnapshotResponse_Footer:
		return entry.Footer != nil
	default:
		return false
	}
}

func validateReceiptSnapshotEdge(
	edge *pb.SnapshotEdge,
	tombstone hlc.Timestamp,
	bounds receiptSnapshotCausalBounds,
) (hlc.Timestamp, error) {
	if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" || len(edge.GetContributions()) == 0 {
		return hlc.Timestamp{}, fmt.Errorf("invalid live edge")
	}
	var putFloor hlc.Timestamp
	if edge.GetHlc() != nil {
		var ok bool
		putFloor, ok = bounds.parse(edge.GetHlc())
		if !ok {
			return hlc.Timestamp{}, fmt.Errorf("invalid live edge Put floor")
		}
	}
	var seenPut bool
	seenAdds := make(map[[24]byte]struct{})
	for _, contribution := range edge.GetContributions() {
		if contribution == nil || !validOptionalReceiptSnapshotTimestamp(contribution.GetExpiration()) {
			return hlc.Timestamp{}, fmt.Errorf("invalid live edge contribution")
		}
		idBytes := contribution.GetContribId()
		switch len(idBytes) {
		case 0:
			if tombstone != (hlc.Timestamp{}) {
				return hlc.Timestamp{}, fmt.Errorf("live edge Put contribution conflicts with tombstone")
			}
			if seenPut {
				return hlc.Timestamp{}, fmt.Errorf("duplicate live edge Put contribution")
			}
			seenPut = true
			if contribution.GetHlc() == nil {
				if putFloor != (hlc.Timestamp{}) {
					return hlc.Timestamp{}, fmt.Errorf("live edge Put contribution lacks its floor HLC")
				}
			} else {
				stamp, ok := bounds.parse(contribution.GetHlc())
				if !ok || stamp != putFloor {
					return hlc.Timestamp{}, fmt.Errorf("live edge Put contribution HLC mismatch")
				}
			}
		case 24:
			var id [24]byte
			copy(id[:], idBytes)
			if id == ([24]byte{}) {
				return hlc.Timestamp{}, fmt.Errorf("zero live edge Add ContribID")
			}
			if _, exists := seenAdds[id]; exists {
				return hlc.Timestamp{}, fmt.Errorf("duplicate live edge Add ContribID")
			}
			seenAdds[id] = struct{}{}
			addHLC, ok := bounds.parse(contribution.GetHlc())
			if !ok || (putFloor != (hlc.Timestamp{}) && !putFloor.Less(addHLC)) {
				return hlc.Timestamp{}, fmt.Errorf("invalid live edge Add HLC")
			}
			if tombstone != (hlc.Timestamp{}) && !tombstone.Less(addHLC) {
				return hlc.Timestamp{}, fmt.Errorf("live edge Add does not follow tombstone")
			}
		default:
			return hlc.Timestamp{}, fmt.Errorf("invalid live edge ContribID length")
		}
	}
	return putFloor, nil
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

// ValidateReceiptSnapshotFrame checks one decoded frame before a receiver
// serializes or stages it. It preserves typed-nil detection that protobuf
// marshaling would otherwise collapse into an empty message.
func ValidateReceiptSnapshotFrame(frame *pb.SnapshotResponse) error {
	if !validReceiptSnapshotFrameOneofs(frame) {
		return fmt.Errorf("receipt Snapshot frame has a nil or unknown oneof")
	}
	if proto.Size(frame) > receiptSnapshotMaxFrameBytes {
		return fmt.Errorf("receipt Snapshot frame is nil or exceeds %d bytes", receiptSnapshotMaxFrameBytes)
	}
	if err := rejectProtoUnknownFields(frame.ProtoReflect()); err != nil {
		return fmt.Errorf("receipt Snapshot frame %v", err)
	}
	return nil
}

// validateReceiptSnapshotFrames is the producer's final preflight.
func validateReceiptSnapshotFrames(frames []*pb.SnapshotResponse) error {
	_, err := DecodeReceiptSnapshotFrames(frames)
	return err
}

// DecodeReceiptSnapshotFrames validates one complete RECEIPT_V1 stream and
// converts it into the detached capture shape consumed by the private
// whole-state archive/staging layer. The returned graph stream owns cloned
// frames and has receipt metadata/counts split back out of its header/footer.
// It installs nothing and returns no partial capture.
func DecodeReceiptSnapshotFrames(frames []*pb.SnapshotResponse) (ReceiptWholeStateCapture, error) {
	if len(frames) < 2 {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot stream lacks header or footer")
	}
	for _, frame := range frames {
		if err := ValidateReceiptSnapshotFrame(frame); err != nil {
			return ReceiptWholeStateCapture{}, err
		}
	}
	if frames[0].GetHeader() == nil || frames[len(frames)-1].GetFooter() == nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot stream lacks header or footer")
	}
	header := frames[0].GetHeader()
	footer := frames[len(frames)-1].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		header.GetReceiptMetadata() == nil || header.GetReceiptMetadata().GetPolicy() == nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot header metadata is missing")
	}
	cutoff, ok := receiptSnapshotHLC(header.GetCutoffHlc())
	if !ok {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot cutoff HLC is invalid")
	}

	config, state, err := receiptSnapshotStoreState(header.GetReceiptMetadata())
	if err != nil {
		return ReceiptWholeStateCapture{}, err
	}
	if state.ClockHighWaterMillis > cutoff.WallNs/int64(time.Millisecond) {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot clock high-water exceeds cutoff HLC")
	}
	originLast, origins, err := validateReceiptSnapshotWireOrigins(header, cutoff)
	if err != nil {
		return ReceiptWholeStateCapture{}, err
	}

	var receiptCount uint64
	graphFrames := make([]*pb.SnapshotResponse, 0, len(frames))
	headerFrame := proto.Clone(frames[0]).(*pb.SnapshotResponse)
	headerFrame.GetHeader().ReceiptMetadata = nil
	graphFrames = append(graphFrames, headerFrame)
	graphStarted := false
	for _, frame := range frames[1 : len(frames)-1] {
		if row := frame.GetReceipt(); row != nil {
			if graphStarted {
				return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot receipt row follows graph data")
			}
			receipt, err := receiptFromSnapshotRow(row)
			if err != nil {
				return ReceiptWholeStateCapture{}, err
			}
			state.Receipts = append(state.Receipts, receipt)
			receiptCount++
			continue
		}
		graphStarted = true
		graphFrames = append(graphFrames, proto.Clone(frame).(*pb.SnapshotResponse))
	}
	if receiptCount != footer.GetReceiptCount() ||
		uint64(len(header.GetReceiptMetadata().GetOriginCutoffs())) != footer.GetReceiptOriginCount() {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot footer count mismatch")
	}
	if err := validateReceiptSnapshotGraphBody(
		graphFrames[1:],
		footer,
		receiptSnapshotCausalBounds{cutoff: cutoff, originLast: originLast},
	); err != nil {
		return ReceiptWholeStateCapture{}, err
	}
	if _, err := mutationreceipt.NewFromSnapshot(config, state); err != nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt Snapshot rows are invalid: %w", err)
	}
	footerFrame := proto.Clone(frames[len(frames)-1]).(*pb.SnapshotResponse)
	footerFrame.GetFooter().ReceiptCount = 0
	footerFrame.GetFooter().ReceiptOriginCount = 0
	graphFrames = append(graphFrames, footerFrame)
	capture := ReceiptWholeStateCapture{
		Graph: graphFrames, Receipts: state, Policy: config, Origins: origins,
	}
	if err := validateReceiptSnapshotCapture(capture, config); err != nil {
		return ReceiptWholeStateCapture{}, err
	}
	return capture, nil
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

func validateReceiptSnapshotOrigins(
	header *pb.SnapshotHeader,
	origins []OriginState,
	cutoff hlc.Timestamp,
) (map[hlc.NodeID]hlc.Timestamp, error) {
	if len(header.GetCutoffSeqPerOrigin()) != len(origins) {
		return nil, fmt.Errorf("receipt Snapshot origin cutoffs do not match graph header")
	}
	var previous hlc.NodeID
	lastByOrigin := make(map[hlc.NodeID]hlc.Timestamp, len(origins))
	for i, origin := range origins {
		if origin.Origin == (hlc.NodeID{}) || origin.LastSeq == 0 ||
			origin.LastHLC.NodeID != origin.Origin || origin.LastHLC.WallNs <= 0 ||
			(i != 0 && bytes.Compare(previous[:], origin.Origin[:]) >= 0) ||
			cutoff.Less(origin.LastHLC) ||
			header.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin.Origin[:])] != origin.LastSeq {
			return nil, fmt.Errorf("receipt Snapshot origin cutoff is invalid")
		}
		lastByOrigin[origin.Origin] = origin.LastHLC
		previous = origin.Origin
	}
	return lastByOrigin, nil
}

func validateReceiptSnapshotWireOrigins(
	header *pb.SnapshotHeader,
	cutoff hlc.Timestamp,
) (map[hlc.NodeID]hlc.Timestamp, []OriginState, error) {
	metadata := header.GetReceiptMetadata()
	if len(header.GetCutoffSeqPerOrigin()) != len(metadata.GetOriginCutoffs()) {
		return nil, nil, fmt.Errorf("receipt Snapshot origin metadata count mismatch")
	}
	var previous hlc.NodeID
	lastByOrigin := make(map[hlc.NodeID]hlc.Timestamp, len(metadata.GetOriginCutoffs()))
	origins := make([]OriginState, 0, len(metadata.GetOriginCutoffs()))
	for i, row := range metadata.GetOriginCutoffs() {
		if row == nil || len(row.GetOrigin()) != len(hlc.NodeID{}) || row.GetLastSeq() == 0 {
			return nil, nil, fmt.Errorf("receipt Snapshot origin metadata is invalid")
		}
		var origin hlc.NodeID
		copy(origin[:], row.GetOrigin())
		last, ok := receiptSnapshotHLC(row.GetLastHlc())
		if !ok || origin == (hlc.NodeID{}) || last.NodeID != origin ||
			(i != 0 && bytes.Compare(previous[:], origin[:]) >= 0) ||
			cutoff.Less(last) ||
			header.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin[:])] != row.GetLastSeq() {
			return nil, nil, fmt.Errorf("receipt Snapshot origin metadata is invalid")
		}
		lastByOrigin[origin] = last
		origins = append(origins, OriginState{
			Origin: origin, LastSeq: row.GetLastSeq(), LastHLC: last,
		})
		previous = origin
	}
	return lastByOrigin, origins, nil
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
