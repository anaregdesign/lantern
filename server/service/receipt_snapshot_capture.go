package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// receiptWholeStateCapture is only a detached, healthy in-process publication
// cut. Its RECEIPT_V1 graph frames are an input to the private archive codec,
// not a supported Snapshot RPC or proof that a WAL has the current frontier.
type receiptWholeStateCapture struct {
	Graph    []*pb.SnapshotResponse
	Receipts mutationreceipt.Snapshot
	Policy   mutationreceipt.Config
	Origins  []OriginState
}

// captureReceiptWholeState copies the graph, receipts, origin vector, local
// log position, and HLC frontier while receipt commits and server-owned
// high-water-changing lookups are excluded by the same publication gate.
// It returns no partial image on a fault, old-epoch Store export, or policy
// mismatch. The caller owns the resulting protobuf values and receipt bytes.
//
// Clock.Now advances an in-memory HLC; this capture does not make that floor
// durable. Store.Begin and Store.Lookup also advance high-water without a WAL
// envelope. Restore must persist those advances or rotate the active epoch
// before serving receipt-capable traffic; this private seam enables neither.
func (c *edgeDeleteReceiptCoordinator) captureReceiptWholeState(ctx context.Context, policy mutationreceipt.Config) (receiptWholeStateCapture, error) {
	if err := ctx.Err(); err != nil {
		return receiptWholeStateCapture{}, ctxToConnect(err)
	}
	if c == nil || c.service == nil || c.cache == nil || c.store == nil {
		return receiptWholeStateCapture{}, errors.New("receipt whole-state capture requires a staged service and Store")
	}
	s := c.service
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if s.log == nil || s.clock == nil || s.origins == nil || !ok || cache != c.cache {
		return receiptWholeStateCapture{}, errors.New("receipt whole-state capture requires one wired graph, log, clock, and origin tracker")
	}
	configured, err := mutationreceipt.New(policy)
	if err != nil {
		return receiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: invalid policy: %w", err)
	}
	if configured.Epoch() != c.store.Epoch() || configured.PolicyFingerprint() != c.store.PolicyFingerprint() {
		return receiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: Store policy mismatch: %w", mutationreceipt.ErrInvalidSnapshot)
	}

	var image replicationSnapshotCut
	var receipts mutationreceipt.Snapshot
	var origins []OriginState
	err = s.withExclusiveCommittedView(func() error {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		var captureErr error
		receipts, captureErr = c.store.Snapshot()
		if captureErr != nil {
			return captureErr
		}
		graph := c.cache.SnapshotReplication()
		// GraphCache owns the snapshot slices, but their generic Vertex values
		// still point at protobuf messages. Clone while the publication cut is
		// held so a later service write cannot change the detached image.
		for i := range graph.Graph.Vertices {
			if value := graph.Graph.Vertices[i].Value; value != nil {
				graph.Graph.Vertices[i].Value = proto.Clone(value).(*pb.Vertex)
			}
		}
		origins = s.OriginStates()
		image.cutoffPerOrigin = make(map[string]uint64, len(origins))
		var previous hlc.NodeID
		for i, origin := range origins {
			if origin.Origin == (hlc.NodeID{}) || origin.LastSeq == 0 ||
				origin.LastHLC.WallNs <= 0 || origin.LastHLC.NodeID != origin.Origin ||
				(i != 0 && bytes.Compare(previous[:], origin.Origin[:]) >= 0) {
				return errors.New("receipt whole-state capture has an invalid origin cutoff")
			}
			previous = origin.Origin
			image.cutoffPerOrigin[hex.EncodeToString(origin.Origin[:])] = origin.LastSeq
		}
		image.cutoffLocalSeq, _ = s.log.LastSeq()
		image.cutoffHLC = s.clock.Now()
		if image.cutoffHLC.WallNs <= 0 || image.cutoffHLC.NodeID == (hlc.NodeID{}) {
			return errors.New("receipt whole-state capture has an invalid HLC frontier")
		}
		image.barriers, image.tombstones, image.graph = graph.Barriers, graph.Tombstones, graph.Graph
		return nil
	})
	if err != nil {
		return receiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: %w", err)
	}
	if !policy.ClockHighWater.IsZero() && policy.ClockHighWater.UnixMilli() > receipts.ClockHighWaterMillis {
		return receiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: policy high-water exceeds Store: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	policy.ClockHighWater = receipts.ClockHighWater()
	if _, err := mutationreceipt.NewFromSnapshot(policy, receipts); err != nil {
		return receiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: invalid Store policy or snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return receiptWholeStateCapture{}, ctxToConnect(err)
	}

	collector := &receiptSnapshotFrameCollector{}
	if err := sendSnapshotFrames(ctx, image, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1, collector); err != nil {
		return receiptWholeStateCapture{}, err
	}
	return receiptWholeStateCapture{Graph: collector.frames, Receipts: receipts, Policy: policy, Origins: origins}, nil
}

type receiptSnapshotFrameCollector struct{ frames []*pb.SnapshotResponse }

func (c *receiptSnapshotFrameCollector) Send(frame *pb.SnapshotResponse) error {
	c.frames = append(c.frames, frame)
	return nil
}
