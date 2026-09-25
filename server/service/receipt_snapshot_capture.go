package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ReceiptWholeStateCapture is only a detached, healthy in-process publication
// cut. Its graph frames and Store state feed both the private archive codec and
// the opt-in RECEIPT_V1 Snapshot producer. It is not proof that a WAL has the
// current frontier and does not enable receipt writes or installation.
type ReceiptWholeStateCapture struct {
	Graph    []*pb.SnapshotResponse
	Receipts mutationreceipt.Snapshot
	Policy   mutationreceipt.Config
	Origins  []OriginState
}

// ReceiptWholeStateBackupCapture binds a detached whole-state image to the
// exact durable FileWAL tip captured under the same exclusive publication
// cut. It is a private backup-composition prerequisite, not a backup schedule,
// persistence format, restore authority, or public receipt capability.
type ReceiptWholeStateBackupCapture struct {
	WholeState ReceiptWholeStateCapture
	WALTip     mutationlog.FileWALTipWitness
	NodeID     hlc.NodeID
	Generation [16]byte
}

// ReceiptWholeStateSource is the read-only source shared by the private archive
// and opt-in replication Snapshot producers. Its private owner identity keeps a
// replication service from accepting a cut from a different primary or serving
// runtime. Capture returns all in-memory sections from one publication cut.
// CaptureForBackup additionally binds that image to the exact durable WAL tip
// without allowing callers to sample the runtime separately.
type ReceiptWholeStateSource struct {
	owner         *LanternService
	store         *mutationreceipt.Store
	cache         *graphcache.GraphCache[string, *pb.Vertex]
	capture       func(context.Context, mutationreceipt.Config) (ReceiptWholeStateCapture, error)
	captureBackup func(context.Context, mutationreceipt.Config) (ReceiptWholeStateBackupCapture, error)
}

// Capture returns one detached publication cut.
func (s *ReceiptWholeStateSource) Capture(
	ctx context.Context,
	policy mutationreceipt.Config,
) (ReceiptWholeStateCapture, error) {
	if s == nil || s.capture == nil {
		return ReceiptWholeStateCapture{}, errors.New("receipt whole-state source is nil")
	}
	return s.capture(ctx, policy)
}

// CaptureForBackup returns one detached publication cut and its exact durable
// FileWAL tip. Graph-only, faulted, mismatched, or closed runtimes fail closed
// and return no partial image or witness.
func (s *ReceiptWholeStateSource) CaptureForBackup(
	ctx context.Context,
	policy mutationreceipt.Config,
) (ReceiptWholeStateBackupCapture, error) {
	if s == nil || s.captureBackup == nil {
		return ReceiptWholeStateBackupCapture{}, errors.New("receipt whole-state backup source is nil")
	}
	return s.captureBackup(ctx, policy)
}

func (s *ReceiptWholeStateSource) belongsTo(replication *LanternReplicationService) bool {
	if s == nil || s.owner == nil || s.store == nil || s.cache == nil ||
		s.capture == nil || s.captureBackup == nil || replication == nil {
		return false
	}
	backend, backendOK := replication.backend.(*graphcache.GraphCache[string, *pb.Vertex])
	ownerBackend, ownerBackendOK := s.owner.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	originOwner, originOwnerOK := replication.origins.(*LanternService)
	return backendOK && ownerBackendOK && originOwnerOK &&
		s.cache == backend && s.cache == ownerBackend &&
		s.owner == originOwner &&
		s.owner.log == replication.log &&
		s.owner.clock == replication.clock &&
		s.owner.runtime == replication.runtime &&
		s.owner.receiptStore == s.store
}

// NewReceiptWholeStateSource exposes only the coordinator's detached capture,
// never its commit or status operations. Runtime certification constructs the
// production instance shared by replication Snapshot and backup; direct
// callers do not gain receipt admission or restore authority. The service
// binds its first Store pointer and rejects a different one, even when the
// replacement has the same epoch and policy.
func NewReceiptWholeStateSource(s *LanternService, store *mutationreceipt.Store) (*ReceiptWholeStateSource, error) {
	coordinator, err := newEdgeDeleteReceiptCoordinator(s, store)
	if err != nil {
		return nil, err
	}
	return &ReceiptWholeStateSource{
		owner:         s,
		store:         store,
		cache:         coordinator.cache,
		capture:       coordinator.captureReceiptWholeState,
		captureBackup: coordinator.captureReceiptWholeStateForBackup,
	}, nil
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
	capture, err := c.captureReceiptWholeStateCut(ctx, policy, false)
	if err != nil {
		return ReceiptWholeStateCapture{}, err
	}
	return capture.WholeState, nil
}

func (c *edgeDeleteReceiptCoordinator) captureReceiptWholeStateForBackup(
	ctx context.Context,
	policy mutationreceipt.Config,
) (ReceiptWholeStateBackupCapture, error) {
	return c.captureReceiptWholeStateCut(ctx, policy, true)
}

func (c *edgeDeleteReceiptCoordinator) captureReceiptWholeStateCut(
	ctx context.Context,
	policy mutationreceipt.Config,
	includeWALTip bool,
) (ReceiptWholeStateBackupCapture, error) {
	if err := ctx.Err(); err != nil {
		return ReceiptWholeStateBackupCapture{}, ctxToConnect(err)
	}
	if c == nil || c.service == nil || c.cache == nil || c.store == nil {
		return ReceiptWholeStateBackupCapture{}, errors.New("receipt whole-state capture requires a staged service and Store")
	}
	s := c.service
	cache, ok := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if s.log == nil || s.clock == nil || s.origins == nil || !ok || cache != c.cache {
		return ReceiptWholeStateBackupCapture{}, errors.New("receipt whole-state capture requires one wired graph, log, clock, and origin tracker")
	}
	configured, err := mutationreceipt.New(policy)
	if err != nil {
		return ReceiptWholeStateBackupCapture{}, fmt.Errorf("receipt whole-state capture: invalid policy: %w", err)
	}
	if configured.Epoch() != c.store.Epoch() || configured.PolicyFingerprint() != c.store.PolicyFingerprint() {
		return ReceiptWholeStateBackupCapture{}, fmt.Errorf("receipt whole-state capture: Store policy mismatch: %w", mutationreceipt.ErrInvalidSnapshot)
	}

	var image replicationSnapshotCut
	var receipts mutationreceipt.Snapshot
	var origins []OriginState
	var walTip mutationlog.FileWALTipWitness
	var nodeID hlc.NodeID
	var generation [16]byte
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
		if includeWALTip {
			if s.runtime == nil || s.runtime.receipt == nil {
				return errors.New("receipt whole-state backup capture requires a durable serving runtime")
			}
			walTip, captureErr = s.runtime.receiptWALTipWitness(s, image.cutoffLocalSeq)
			if captureErr != nil {
				return captureErr
			}
			nodeID = s.clock.NodeID()
			generation = s.runtime.receipt.generation
			if nodeID == (hlc.NodeID{}) || generation == ([16]byte{}) {
				return errors.New("receipt whole-state backup capture has an invalid runtime identity")
			}
		}
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		return nil
	})
	if err != nil {
		return ReceiptWholeStateBackupCapture{}, fmt.Errorf("receipt whole-state capture: %w", err)
	}
	if !policy.ClockHighWater.IsZero() && policy.ClockHighWater.UnixMilli() > receipts.ClockHighWaterMillis {
		return ReceiptWholeStateBackupCapture{}, fmt.Errorf("receipt whole-state capture: policy high-water exceeds Store: %w", mutationreceipt.ErrInvalidSnapshot)
	}
	if receipts.ClockHighWaterMillis > image.cutoffHLC.WallNs/int64(time.Millisecond) {
		return ReceiptWholeStateBackupCapture{}, fmt.Errorf(
			"receipt whole-state capture: Store clock high-water exceeds graph cutoff: %w",
			mutationreceipt.ErrInvalidSnapshot,
		)
	}
	policy.ClockHighWater = receipts.ClockHighWater()
	if _, err := mutationreceipt.NewFromSnapshot(policy, receipts); err != nil {
		return ReceiptWholeStateBackupCapture{}, fmt.Errorf("receipt whole-state capture: invalid Store policy or snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ReceiptWholeStateBackupCapture{}, ctxToConnect(err)
	}

	collector := &receiptSnapshotFrameCollector{}
	if err := sendSnapshotFrames(ctx, image, pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1, collector); err != nil {
		return ReceiptWholeStateBackupCapture{}, err
	}
	wholeState := ReceiptWholeStateCapture{
		Graph: collector.frames, Receipts: receipts, Policy: policy, Origins: origins,
	}
	if includeWALTip {
		if len(wholeState.Graph) == 0 {
			return ReceiptWholeStateBackupCapture{}, errors.New("receipt whole-state capture: graph frame stream is empty")
		}
		header := wholeState.Graph[0].GetHeader()
		if header == nil || header.GetCutoffLocalSeq() != walTip.Seq {
			return ReceiptWholeStateBackupCapture{}, fmt.Errorf(
				"receipt whole-state capture: %w: graph seq differs from WAL witness",
				mutationlog.ErrFileWALSequence,
			)
		}
	}
	return ReceiptWholeStateBackupCapture{
		WholeState: wholeState,
		WALTip:     walTip,
		NodeID:     nodeID,
		Generation: generation,
	}, nil
}

type receiptSnapshotFrameCollector struct{ frames []*pb.SnapshotResponse }

func (c *receiptSnapshotFrameCollector) Send(frame *pb.SnapshotResponse) error {
	c.frames = append(c.frames, frame)
	return nil
}
