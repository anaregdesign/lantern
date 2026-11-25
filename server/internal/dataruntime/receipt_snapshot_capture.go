package dataruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ReceiptWholeStateCapture is only a detached, healthy in-process publication
// cut. Its graph frames and active Store state feed both the private archive
// codec and the opt-in RECEIPT Snapshot producer; Retired is transported
// only by RECEIPT. It is not proof that a WAL has the current frontier and
// does not enable receipt writes or installation.
type ReceiptWholeStateCapture struct {
	Graph    []*pb.SnapshotResponse
	Receipts mutationreceipt.Snapshot
	Retired  mutationreceipt.RetiredCatalogSnapshot
	Policy   mutationreceipt.Config
	Origins  []OriginState
}

// ReceiptWholeStateSource binds a logical replication image to a single
// executor, graph and receipt policy.
type ReceiptWholeStateSource struct {
	owner   *Executor
	store   *mutationreceipt.Store
	retired *retiredReceiptCatalogSlot
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	capture func(context.Context, mutationreceipt.Config) (ReceiptWholeStateCapture, error)
}

// Capture returns one detached publication cut.
func (c *ReceiptWholeStateSource) Capture(
	ctx context.Context,
	policy mutationreceipt.Config,
) (ReceiptWholeStateCapture, error) {
	if c == nil || c.capture == nil {
		return ReceiptWholeStateCapture{}, errors.New("receipt whole-state source is nil")
	}
	return c.capture(ctx, policy)
}

func (c *ReceiptWholeStateSource) belongsTo(replication *Replication) bool {
	if c == nil || c.owner == nil || c.store == nil || c.retired == nil || c.cache == nil ||
		c.capture == nil || replication == nil {
		return false
	}
	backend, backendOK := replication.backend.(*graphcache.GraphCache[string, *pb.Vertex])
	ownerBackend, ownerBackendOK := c.owner.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	originOwner, originOwnerOK := replication.origins.(*Executor)
	runtimeSlotMatches := replication.runtime == nil ||
		(replication.runtime.receipt != nil && replication.runtime.receipt.retired == c.retired)
	return backendOK && ownerBackendOK && originOwnerOK &&
		c.cache == backend && c.cache == ownerBackend &&
		c.owner == originOwner &&
		c.owner.log == replication.log &&
		c.owner.clock == replication.clock &&
		c.owner.runtime == replication.runtime &&
		runtimeSlotMatches &&
		c.owner.receiptStore == c.store &&
		c.owner.receiptRetiredCatalog == c.retired &&
		c.owner.receiptEdgeContributionDeleteCoordinator != nil &&
		c.owner.receiptEdgeContributionDeleteCoordinator.service == c.owner &&
		c.owner.receiptEdgeContributionDeleteCoordinator.cache == c.cache &&
		c.owner.receiptEdgeContributionDeleteCoordinator.store == c.store
}

// NewReceiptWholeStateSource exposes only the executor's detached capture,
// never its commit or status operations. Runtime certification constructs the
// production instance shared by replication Snapshot and backup; direct
// callers do not gain receipt admission or restore authority. The service
// binds its first Store pointer and rejects a different one, even when the
// replacement has the same epoch and policy.
func NewReceiptWholeStateSource(s *Executor, store *mutationreceipt.Store) (*ReceiptWholeStateSource, error) {
	coordinator, err := newEdgeDeleteReceiptCoordinator(s, store)
	if err != nil {
		return nil, err
	}
	s.replicationCutMu.Lock()
	if s.receiptRetiredCatalog == nil {
		s.receiptRetiredCatalog = &retiredReceiptCatalogSlot{}
	}
	retired := s.receiptRetiredCatalog
	coordinator.retired = retired
	s.replicationCutMu.Unlock()
	if _, err := newVertexPutReceiptCoordinator(s, store); err != nil {
		return nil, err
	}
	if _, err := newVertexDeleteReceiptCoordinator(s, store); err != nil {
		return nil, err
	}
	if _, err := newEdgeAddReceiptCoordinator(s, store); err != nil {
		return nil, err
	}
	if _, err := newEdgeContributionDeleteReceiptCoordinator(s, store); err != nil {
		return nil, err
	}
	source := &ReceiptWholeStateSource{
		owner: s, store: store, retired: retired, cache: coordinator.cache,
	}
	source.capture = source.captureReceiptWholeState
	return source, nil
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
func (c *ReceiptWholeStateSource) captureReceiptWholeState(
	ctx context.Context,
	policy mutationreceipt.Config,
) (ReceiptWholeStateCapture, error) {
	return c.captureWithTail(ctx, policy, nil)
}

// captureWithTail lets the private replication owner reserve the exact tail
// before releasing the whole-state publication cut. The callback must not call
// back into the service or retain mutable state from this cut. The caller owns
// its reservation and must close it on every subsequent capture/send failure.
func (c *ReceiptWholeStateSource) captureWithTail(
	ctx context.Context,
	policy mutationreceipt.Config,
	atCut func(uint64, map[string]uint64, <-chan struct{}) error,
) (ReceiptWholeStateCapture, error) {
	if err := ctx.Err(); err != nil {
		return ReceiptWholeStateCapture{}, ctxToConnect(err)
	}
	if c == nil || c.owner == nil || c.cache == nil || c.store == nil || c.retired == nil {
		return ReceiptWholeStateCapture{}, errors.New("receipt whole-state capture requires a staged service, Store, and retired catalog")
	}
	s := c.owner
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

	image := replicationSnapshotCut{namespaceFormat: s.namespaceFormat}
	var receipts mutationreceipt.Snapshot
	var retired mutationreceipt.RetiredCatalogSnapshot
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
		if captureErr = c.retired.initializeIfNeeded(policy, receipts.ClockHighWaterMillis); captureErr != nil {
			return captureErr
		}
		retired, _, captureErr = c.retired.snapshot(policy, receipts.ClockHighWaterMillis)
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
		if receipts.ClockHighWaterMillis < 0 ||
			receipts.ClockHighWaterMillis > math.MaxInt64/int64(time.Millisecond) {
			return errors.New("receipt whole-state capture clock high-water exceeds the HLC wall range")
		}
		if err := s.clock.RestoreFloor(hlc.Timestamp{
			WallNs: receipts.ClockHighWaterMillis * int64(time.Millisecond),
			NodeID: s.clock.NodeID(),
		}); err != nil {
			return fmt.Errorf("restore receipt clock high-water into HLC: %w", err)
		}
		image.cutoffHLC = s.clock.Now()
		if image.cutoffHLC.WallNs <= 0 || image.cutoffHLC.NodeID == (hlc.NodeID{}) {
			return errors.New("receipt whole-state capture has an invalid HLC frontier")
		}
		image.barriers, image.tombstones, image.graph = graph.Barriers, graph.Tombstones, graph.Graph

		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if atCut != nil {
			return atCut(image.cutoffLocalSeq, maps.Clone(image.cutoffPerOrigin), s.publicationFaultCh)
		}
		return nil
	})
	if err != nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: %w", err)
	}
	if !policy.ClockHighWater.IsZero() && policy.ClockHighWater.UnixMilli() > receipts.ClockHighWaterMillis {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: policy high-water exceeds Store: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	if receipts.ClockHighWaterMillis > image.cutoffHLC.WallNs/int64(time.Millisecond) {
		return ReceiptWholeStateCapture{}, fmt.Errorf(
			"receipt whole-state capture: Store clock high-water exceeds graph cutoff: %w",
			mutationreceipt.ErrInvalidSnapshot,
		)
	}
	policy.ClockHighWater = receipts.ClockHighWater()
	if _, err := mutationreceipt.NewFromSnapshot(policy, receipts); err != nil {
		return ReceiptWholeStateCapture{}, fmt.Errorf("receipt whole-state capture: invalid Store policy or snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ReceiptWholeStateCapture{}, ctxToConnect(err)
	}

	collector := &receiptSnapshotFrameCollector{}
	if err := sendSnapshotFrames(ctx, image, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, collector); err != nil {
		return ReceiptWholeStateCapture{}, err
	}
	wholeState := ReceiptWholeStateCapture{
		Graph: collector.frames, Receipts: receipts, Retired: retired, Policy: policy, Origins: origins,
	}
	return wholeState, nil
}

type receiptSnapshotFrameCollector struct{ frames []*pb.SnapshotResponse }

func (c *receiptSnapshotFrameCollector) Send(frame *pb.SnapshotResponse) error {
	c.frames = append(c.frames, frame)
	return nil
}
