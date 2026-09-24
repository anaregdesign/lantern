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

// ReceiptWholeStateCapture is only a detached, healthy in-process publication
// cut. Its RECEIPT_V1 graph frames are an input to the private archive codec,
// not a supported Snapshot RPC or proof that a WAL has the current frontier.
type ReceiptWholeStateCapture struct {
	Graph    []*pb.SnapshotResponse
	Receipts mutationreceipt.Snapshot
	Policy   mutationreceipt.Config
	Origins  []OriginState
}

// ReceiptWholeStateSource is a read-only source for the private archive
// producer. One call returns all sections from one publication cut; callers
// must not assemble an archive by sampling the service again.
type ReceiptWholeStateSource func(context.Context, mutationreceipt.Config) (ReceiptWholeStateCapture, error)

// NewReceiptWholeStateSource exposes only the coordinator's detached capture,
// never its commit or status operations. It is deliberately absent from the
// production DI graph and does not enable receipt admission or restore. The
// service binds its first Store pointer and rejects a different one, even
// when the replacement has the same epoch and policy.
func NewReceiptWholeStateSource(s *LanternService, store *mutationreceipt.Store) (ReceiptWholeStateSource, error) {
	coordinator, err := newEdgeDeleteReceiptCoordinator(s, store)
	if err != nil {
		return nil, err
	}
	return coordinator.captureReceiptWholeState, nil
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
func (c *edgeDeleteReceiptCoordinator) captureReceiptWholeState(ctx context.Context, policy mutationreceipt.Config) (ReceiptWholeStateCapture, error) {
	if err := ctx.Err(); err != nil {
		return ReceiptWholeStateCapture{}, ctxToConnect(err)
	}
	if c == nil || c.service == nil || c.cache == nil || c.store == nil {
		return ReceiptWholeStateCapture{}, errors.New("receipt whole-state capture requires a staged service and Store")
	}
	s := c.service
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if s.log == nil || s.clock == nil || s.origins == nil || !ok || cache != c.cache {
		return ReceiptWholeStateCapture{}, errors.New("receipt whole-state capture requires one wired graph, log, clock, and origin tracker")
	}
	configured, err := mutationreceipt.New(policy)
	if err != nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: invalid policy: %w", err)
	}
	if configured.Epoch() != c.store.Epoch() || configured.PolicyFingerprint() != c.store.PolicyFingerprint() {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: Store policy mismatch: %w", mutationreceipt.ErrInvalidSnapshot)
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
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: %w", err)
	}
	if !policy.ClockHighWater.IsZero() && policy.ClockHighWater.UnixMilli() > receipts.ClockHighWaterMillis {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: policy high-water exceeds Store: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	policy.ClockHighWater = receipts.ClockHighWater()
	if _, err := mutationreceipt.NewFromSnapshot(policy, receipts); err != nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: invalid Store policy or snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ReceiptWholeStateCapture{}, ctxToConnect(err)
	}

	collector := &receiptSnapshotFrameCollector{}
	if err := sendSnapshotFrames(ctx, image, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1, collector); err != nil {
		return ReceiptWholeStateCapture{}, err
	}
	return ReceiptWholeStateCapture{Graph: collector.frames, Receipts: receipts, Policy: policy, Origins: origins}, nil
}

type receiptSnapshotFrameCollector struct{ frames []*pb.SnapshotResponse }

func (c *receiptSnapshotFrameCollector) Send(frame *pb.SnapshotResponse) error {
	c.frames = append(c.frames, frame)
	return nil
}
