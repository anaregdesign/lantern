package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

type receiptBaselineInstallFaultPoint string

const (
	receiptBaselineAfterSidecarBeforeFinalCut receiptBaselineInstallFaultPoint = "after-sidecar-before-final-cut"
	receiptBaselineAfterMarkerBeforePublish   receiptBaselineInstallFaultPoint = "after-marker-before-publish"
	receiptBaselineAfterPublish               receiptBaselineInstallFaultPoint = "after-baseline-publish"
	receiptBaselineInstallMaxAttempts                                          = 3
)

var errReceiptBaselineInstallDrift = errors.New("service: receipt baseline install state drifted")

// InstallReceiptBaseline validates a detached combined receipt capture and installs
// it into this service's identity-stable runtime objects. It is a private
// in-process composition primitive; no public receipt RPC calls it.
func (s *LanternService) InstallReceiptBaseline(ctx context.Context, capture ReceiptWholeStateCapture) error {
	return s.installReceiptBaseline(ctx, capture)
}

func (s *LanternService) installReceiptBaseline(
	ctx context.Context,
	capture ReceiptWholeStateCapture,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.runtime == nil || s.runtime.receipt == nil ||
		s.runtime.receipt.owner == nil || s.runtime.receipt.baselineCodec == nil ||
		s.runtime.receipt.baselineInstallGate == nil ||
		s.cache != s.runtime.graph || s.log != s.runtime.log ||
		s.clock != s.runtime.clock || s.origins != s.runtime.origins ||
		s.receiptStore != s.runtime.receipt.store ||
		s.receiptRetiredCatalog != s.runtime.receipt.retired {
		return errors.New("service: durable baseline install requires the exact certified runtime")
	}
	receiptRuntime := s.runtime.receipt
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-receiptRuntime.baselineInstallGate:
	}
	defer func() {
		receiptRuntime.baselineInstallGate <- struct{}{}
	}()
	if err := s.withExclusiveCommittedView(func() error { return nil }); err != nil {
		return err
	}
	if capture.Policy.Epoch != receiptRuntime.epoch {
		return fmt.Errorf("%w: baseline epoch mismatch", errReceiptBaselineMarker)
	}
	policyStore, err := mutationreceipt.New(receiptRuntime.policy)
	if err != nil {
		return fmt.Errorf("service: baseline runtime policy: %w", err)
	}
	if capture.Receipts.Epoch != receiptRuntime.epoch ||
		capture.Receipts.PolicyFingerprint != policyStore.PolicyFingerprint() {
		return fmt.Errorf("%w: baseline policy mismatch", errReceiptBaselineMarker)
	}

	incomingRaw, err := receiptRuntime.baselineCodec.EncodeCombinedReceiptBaseline(ctx, capture)
	if err != nil {
		return fmt.Errorf("service: encode receipt baseline: %w", err)
	}
	incoming, err := receiptRuntime.baselineCodec.StageCombinedReceiptBaseline(
		ctx,
		incomingRaw,
		receiptRuntime.policy,
		receiptRuntime.defaultTTL,
		receiptRuntime.configureGraph,
	)
	if err != nil {
		return fmt.Errorf("service: validate receipt baseline: %w", err)
	}
	if incoming == nil || incoming.Graph == nil || incoming.Receipts == nil ||
		incoming.Policy.Epoch != receiptRuntime.epoch ||
		incoming.Receipts.PolicyFingerprint() != policyStore.PolicyFingerprint() {
		return fmt.Errorf("%w: incomplete or mismatched baseline candidate", errReceiptBaselineMarker)
	}
	incomingReceipts, err := incoming.Receipts.Snapshot()
	if err != nil {
		return fmt.Errorf("service: snapshot baseline receipt candidate: %w", err)
	}
	if incoming.Retired.ClockHighWaterMillis != incomingReceipts.ClockHighWaterMillis {
		return fmt.Errorf("%w: active and retired baseline high-water differs", errReceiptBaselineMarker)
	}
	graphFrames := cloneReceiptBaselineGraphFrames(capture.Graph)
	sidecars := receiptBaselineSidecarStore{
		walPath: receiptRuntime.owner.lease.Path(),
		fault:   receiptRuntime.sidecarFault,
	}
	for attempt := 0; attempt < receiptBaselineInstallMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		observed, prepared, err := s.prepareReceiptBaselineAttempt(
			ctx,
			receiptRuntime,
			incoming,
			incomingReceipts,
			graphFrames,
		)
		if err != nil {
			return err
		}
		raw, err := receiptRuntime.baselineCodec.EncodeCombinedReceiptBaseline(ctx, prepared.capture)
		if err != nil {
			return fmt.Errorf("service: encode combined receipt baseline: %w", err)
		}
		committed := receiptRuntime.committedBaseline
		digest, _, err := sidecars.persist(ReceiptBaselineFormatCombined, raw)
		if err != nil {
			return errors.Join(err, sidecars.cleanup(committed))
		}
		if fault := receiptRuntime.injectInstallFault(receiptBaselineAfterSidecarBeforeFinalCut); fault != nil {
			return errors.Join(fault, sidecars.cleanup(committed))
		}
		markerMayBeDurable := false
		err = s.commitReceiptBaselineAttempt(
			ctx,
			receiptRuntime,
			policyStore.PolicyFingerprint(),
			observed,
			prepared.candidate,
			raw,
			digest,
			&markerMayBeDurable,
		)
		if err == nil {
			reference := receiptBaselineReference{
				Format: ReceiptBaselineFormatCombined,
				Digest: digest,
			}
			receiptRuntime.committedBaseline = reference
			if cleanupErr := sidecars.cleanup(reference); cleanupErr != nil {
				logger := s.logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error(
					"receipt baseline committed but stale sidecar cleanup failed",
					"baseline_digest", fmt.Sprintf("%x", digest),
					"error", cleanupErr,
				)
			}
			return nil
		}
		if markerMayBeDurable && !receiptBaselineMarkerDefinitelyAbsent(err) {
			return err
		}
		if cleanupErr := sidecars.cleanup(committed); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		if !errors.Is(err, errReceiptBaselineInstallDrift) {
			return err
		}
	}
	return fmt.Errorf("%w after %d attempts", errReceiptBaselineInstallDrift, receiptBaselineInstallMaxAttempts)
}

type receiptBaselineInstallObservation struct {
	active   mutationreceipt.Snapshot
	retired  mutationreceipt.RetiredCatalogSnapshot
	revision uint64
	origins  []OriginState
}

type preparedReceiptBaselineAttempt struct {
	capture   ReceiptWholeStateCapture
	candidate *ReceiptBaselineCandidate
}

func (s *LanternService) prepareReceiptBaselineAttempt(
	ctx context.Context,
	receiptRuntime *receiptServingRuntime,
	incoming *ReceiptBaselineCandidate,
	incomingReceipts mutationreceipt.Snapshot,
	graphFrames []*pb.SnapshotResponse,
) (receiptBaselineInstallObservation, preparedReceiptBaselineAttempt, error) {
	var observed receiptBaselineInstallObservation
	err := s.withExclusiveCommittedView(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.receiptOriginCutMu.Lock()
		defer s.receiptOriginCutMu.Unlock()
		var err error
		observed.active, err = s.receiptStore.Snapshot()
		if err != nil {
			return fmt.Errorf("service: snapshot current receipt Store: %w", err)
		}
		observed.retired, observed.revision, err = s.receiptRetiredCatalog.snapshot(
			receiptRuntime.policy,
			observed.active.ClockHighWaterMillis,
		)
		if err != nil {
			return fmt.Errorf("service: snapshot current retired receipt catalog: %w", err)
		}
		observed.origins = s.origins.States()
		return validateOriginStateDominance(incoming.Origins, observed.origins)
	})
	if err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{}, err
	}
	effectiveHighWater := incomingReceipts.ClockHighWaterMillis
	if effectiveHighWater < observed.active.ClockHighWaterMillis {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{},
			fmt.Errorf("%w: active receipt high-water would move backward", mutationreceipt.ErrRetiredCatalogClockRollback)
	}
	activeConfig := receiptRuntime.policy
	activeConfig.ClockHighWater = time.UnixMilli(effectiveHighWater)
	activeStore, err := mutationreceipt.NewFromSnapshot(activeConfig, incomingReceipts)
	if err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{},
			fmt.Errorf("service: advance baseline receipt Store: %w", err)
	}
	active, err := activeStore.Snapshot()
	if err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{},
			fmt.Errorf("service: snapshot advanced baseline receipt Store: %w", err)
	}
	if err := validateReceiptBaselineDominance(active, observed.active); err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{}, err
	}
	retiredConfig, _, err := retiredCatalogConfig(receiptRuntime.policy, effectiveHighWater)
	if err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{}, err
	}
	retired, err := mutationreceipt.NewRetiredCatalogFromUnion(
		retiredConfig,
		incoming.Retired,
		observed.retired,
	)
	if err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{},
			fmt.Errorf("service: union retired receipt catalogs: %w", err)
	}
	retiredSnapshot, err := retired.Snapshot(time.UnixMilli(effectiveHighWater))
	if err != nil {
		return receiptBaselineInstallObservation{}, preparedReceiptBaselineAttempt{},
			fmt.Errorf("service: snapshot unioned retired receipt catalog: %w", err)
	}
	activeConfig.ClockHighWater = time.UnixMilli(active.ClockHighWaterMillis)
	return observed, preparedReceiptBaselineAttempt{
		capture: ReceiptWholeStateCapture{
			Policy:   activeConfig,
			Graph:    graphFrames,
			Receipts: active,
			Retired:  retiredSnapshot,
			Origins:  incoming.Origins,
		},
		candidate: &ReceiptBaselineCandidate{
			Graph:          incoming.Graph,
			Receipts:       activeStore,
			Retired:        retiredSnapshot,
			Policy:         activeConfig,
			Origins:        incoming.Origins,
			CutoffLocalSeq: incoming.CutoffLocalSeq,
			CutoffHLC:      incoming.CutoffHLC,
		},
	}, nil
}

func cloneReceiptBaselineGraphFrames(frames []*pb.SnapshotResponse) []*pb.SnapshotResponse {
	cloned := make([]*pb.SnapshotResponse, len(frames))
	for i, frame := range frames {
		if frame != nil {
			cloned[i] = proto.Clone(frame).(*pb.SnapshotResponse)
		}
	}
	return cloned
}

func validateReceiptBaselineDominance(
	candidate mutationreceipt.Snapshot,
	current mutationreceipt.Snapshot,
) error {
	rows := make(map[mutationreceipt.ID]mutationreceipt.Receipt, len(candidate.Receipts))
	for _, receipt := range candidate.Receipts {
		rows[receipt.ID] = receipt
	}
	for _, receipt := range current.Receipts {
		if receipt.DeadlineMillis <= candidate.ClockHighWaterMillis {
			continue
		}
		replacement, ok := rows[receipt.ID]
		if !ok || !sameReceiptWALDecision(replacement, receipt) {
			return mutationreceipt.ErrSnapshotDoesNotDominate
		}
	}
	return nil
}

func (s *LanternService) commitReceiptBaselineAttempt(
	ctx context.Context,
	receiptRuntime *receiptServingRuntime,
	policyFingerprint [32]byte,
	observed receiptBaselineInstallObservation,
	candidate *ReceiptBaselineCandidate,
	raw []byte,
	digest [32]byte,
	markerMayBeDurable *bool,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if !*markerMayBeDurable {
				if cleanupErr := (receiptBaselineSidecarStore{
					walPath: receiptRuntime.owner.lease.Path(),
				}).cleanup(receiptRuntime.committedBaseline); cleanupErr != nil {
					logger := s.logger
					if logger == nil {
						logger = slog.Default()
					}
					logger.Error(
						"receipt baseline panic cleanup failed",
						"error", cleanupErr,
					)
				}
			}
			panic(recovered)
		}
	}()
	return s.withExclusiveCommittedView(func() (installErr error) {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.receiptOriginCutMu.Lock()
		defer s.receiptOriginCutMu.Unlock()
		currentActive, err := s.receiptStore.Snapshot()
		if err != nil {
			return fmt.Errorf("service: snapshot final receipt Store: %w", err)
		}
		currentRetired, revision, err := s.receiptRetiredCatalog.snapshot(
			receiptRuntime.policy,
			currentActive.ClockHighWaterMillis,
		)
		if err != nil {
			return fmt.Errorf("service: snapshot final retired receipt catalog: %w", err)
		}
		currentOrigins := s.origins.States()
		if revision != observed.revision ||
			!reflect.DeepEqual(currentActive, observed.active) ||
			!reflect.DeepEqual(currentRetired, observed.retired) ||
			!reflect.DeepEqual(currentOrigins, observed.origins) {
			return errReceiptBaselineInstallDrift
		}
		if err := validateOriginStateDominance(candidate.Origins, currentOrigins); err != nil {
			return err
		}
		rotatedGeneration, err := nextReceiptRuntimeGeneration(receiptRuntime.generation)
		if err != nil {
			return err
		}
		candidateReceipts, err := candidate.Receipts.Snapshot()
		if err != nil {
			return fmt.Errorf("service: snapshot final baseline receipt Store: %w", err)
		}
		storeStage, err := s.receiptStore.BeginSnapshotInstall(candidateReceipts)
		if err != nil {
			return fmt.Errorf("service: stage receipt Store baseline: %w", err)
		}
		defer storeStage.Abort()
		retiredStage, err := s.receiptRetiredCatalog.beginReplace(
			receiptRuntime.policy,
			observed.revision,
			candidateReceipts.ClockHighWaterMillis,
			candidate.Retired,
		)
		if err != nil {
			if errors.Is(err, errRetiredCatalogSlotDrift) {
				return errReceiptBaselineInstallDrift
			}
			return fmt.Errorf("service: stage retired receipt catalog baseline: %w", err)
		}
		defer retiredStage.Abort()
		graphStage, err := s.runtime.graph.BeginWholeStateInstall(candidate.Graph)
		if err != nil {
			return fmt.Errorf("service: stage graph baseline: %w", err)
		}
		defer graphStage.Abort()
		originStage, err := s.origins.stageWholeState(candidate.Origins)
		if err != nil {
			return fmt.Errorf("service: stage origin baseline: %w", err)
		}
		defer originStage.Abort()
		floor, err := receiptBaselineRestoreFloor(
			candidate.CutoffHLC,
			candidateReceipts.ClockHighWaterMillis,
			s.clock.NodeID(),
		)
		if err != nil {
			return err
		}
		clockStage, err := s.clock.BeginRestoreFloor(floor)
		if err != nil {
			return fmt.Errorf("service: stage baseline HLC floor: %w", err)
		}
		defer clockStage.Abort()
		marker := receiptBaselineMarker{
			Format:                 ReceiptBaselineFormatCombined,
			Digest:                 digest,
			Size:                   uint64(len(raw)),
			Epoch:                  receiptRuntime.epoch,
			PolicyFingerprint:      policyFingerprint,
			PreviousGeneration:     receiptRuntime.generation,
			RotatedGeneration:      rotatedGeneration,
			SourceLocalCutoff:      candidate.CutoffLocalSeq,
			ReceiptHighWaterMillis: candidateReceipts.ClockHighWaterMillis,
			SnapshotHLC:            candidate.CutoffHLC,
			RestoreFloor:           clockStage.Floor(),
		}
		if err := validateReceiptBaselineMarker(marker); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.markReceiptCommitFaultLocked()
				panic(recovered)
			}
		}()
		*markerMayBeDurable = true
		_, err = s.log.CommitBoundaryWithPostRingPublication(
			marker,
			marker.RestoreFloor,
			func(mutationlog.Entry) {
				if fault := receiptRuntime.injectInstallFault(receiptBaselineAfterMarkerBeforePublish); fault != nil {
					panic(fault)
				}
				receiptRuntime.generation = rotatedGeneration
				clockStage.Commit()
				originStage.Commit()
				graphStage.Commit()
				retiredStage.Commit()
				storeStage.Commit()
			},
		)
		if err != nil {
			if receiptBaselineWALFailureRequiresFailStop(err) {
				s.markReceiptCommitFaultLocked()
			}
			return fmt.Errorf("service: commit receipt baseline marker: %w", err)
		}
		if fault := receiptRuntime.injectInstallFault(receiptBaselineAfterPublish); fault != nil {
			panic(fault)
		}
		return nil
	})
}

func receiptBaselineMarkerDefinitelyAbsent(err error) bool {
	var aborted *mutationlog.DefiniteWALAbort
	if errors.As(err, &aborted) {
		return true
	}
	// Indeterminate joins take precedence in case a WAL returns one of the
	// readiness sentinels as its underlying I/O error.
	if errors.Is(err, mutationlog.ErrWALIndeterminate) ||
		errors.Is(err, mutationlog.ErrPublicationInterrupted) {
		return false
	}
	// CommitBoundaryWithPostRingPublication returns these only from its
	// readiness and sequence checks, before WAL.Write.
	return errors.Is(err, mutationlog.ErrClosed) ||
		errors.Is(err, mutationlog.ErrSeqExhausted) ||
		errors.Is(err, mutationlog.ErrLegacyWALUncertain)
}

func receiptBaselineWALFailureRequiresFailStop(err error) bool {
	if !receiptBaselineMarkerDefinitelyAbsent(err) {
		return true
	}
	var aborted *mutationlog.DefiniteWALAbort
	// Legacy uncertainty predates this candidate, so discard the candidate
	// while keeping the serving runtime fail-stopped.
	return !errors.As(err, &aborted) && errors.Is(err, mutationlog.ErrLegacyWALUncertain)
}

func nextReceiptRuntimeGeneration(previous [16]byte) ([16]byte, error) {
	for {
		generation, err := newReceiptRuntimeGeneration()
		if err != nil {
			return generation, err
		}
		if generation != previous {
			return generation, nil
		}
	}
}

func receiptBaselineRestoreFloor(snapshot hlc.Timestamp, highWaterMillis int64, nodeID hlc.NodeID) (hlc.Timestamp, error) {
	if snapshot.WallNs <= 0 || snapshot.NodeID == (hlc.NodeID{}) ||
		highWaterMillis < 0 || highWaterMillis > math.MaxInt64/int64(time.Millisecond) ||
		nodeID == (hlc.NodeID{}) {
		return hlc.Timestamp{}, errReceiptBaselineMarker
	}
	floor := snapshot
	floor.NodeID = nodeID
	if floor.Less(snapshot) {
		switch {
		case floor.Logical < math.MaxUint32:
			floor.Logical++
		case floor.WallNs < math.MaxInt64:
			floor.WallNs++
			floor.Logical = 0
		default:
			return hlc.Timestamp{}, errReceiptBaselineMarker
		}
	}
	clockWall := highWaterMillis * int64(time.Millisecond)
	if floor.WallNs < clockWall {
		floor.WallNs = clockWall
		floor.Logical = 0
	}
	return floor, nil
}

func (r *receiptServingRuntime) injectInstallFault(point receiptBaselineInstallFaultPoint) error {
	if r == nil || r.installFault == nil {
		return nil
	}
	return r.installFault(point)
}
