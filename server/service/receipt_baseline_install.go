package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

type receiptBaselineInstallFaultPoint string

const (
	receiptBaselineAfterMarkerBeforePublish receiptBaselineInstallFaultPoint = "after-marker-before-publish"
	receiptBaselineAfterPublish             receiptBaselineInstallFaultPoint = "after-baseline-publish"
)

// InstallReceiptBaseline validates a detached RECEIPT_V1 capture and installs
// it into this service's identity-stable runtime objects. It is a private
// in-process composition primitive; no public receipt RPC calls it.
func (s *LanternService) InstallReceiptBaseline(ctx context.Context, capture ReceiptWholeStateCapture) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.runtime == nil || s.runtime.receipt == nil ||
		s.runtime.receipt.owner == nil || s.runtime.receipt.baselineCodec == nil ||
		s.runtime.receipt.baselineInstallGate == nil ||
		s.cache != s.runtime.graph || s.log != s.runtime.log ||
		s.clock != s.runtime.clock || s.origins != s.runtime.origins ||
		s.receiptStore != s.runtime.receipt.store {
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

	raw, err := receiptRuntime.baselineCodec.EncodeReceiptBaseline(ctx, capture)
	if err != nil {
		return fmt.Errorf("service: encode receipt baseline: %w", err)
	}
	candidate, err := receiptRuntime.baselineCodec.StageReceiptBaseline(
		ctx,
		raw,
		receiptRuntime.policy,
		receiptRuntime.defaultTTL,
		receiptRuntime.configureGraph,
	)
	if err != nil {
		return fmt.Errorf("service: validate receipt baseline: %w", err)
	}
	if candidate == nil || candidate.Graph == nil || candidate.Receipts == nil ||
		candidate.Policy.Epoch != receiptRuntime.epoch ||
		candidate.Receipts.PolicyFingerprint() != policyStore.PolicyFingerprint() {
		return fmt.Errorf("%w: incomplete or mismatched baseline candidate", errReceiptBaselineMarker)
	}
	candidateReceipts, err := candidate.Receipts.Snapshot()
	if err != nil {
		return fmt.Errorf("service: snapshot baseline receipt candidate: %w", err)
	}
	sidecars := receiptBaselineSidecarStore{
		walPath: receiptRuntime.owner.lease.Path(),
		fault:   receiptRuntime.sidecarFault,
	}
	committedBaselineDigest := receiptRuntime.committedBaselineDigest
	digest, _, err := sidecars.persist(raw)
	if err != nil {
		return errors.Join(err, sidecars.cleanup(committedBaselineDigest))
	}

	markerMayBeDurable := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if markerMayBeDurable {
				panic(recovered)
			}
			if cleanupErr := sidecars.cleanup(committedBaselineDigest); cleanupErr != nil {
				logger := s.logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error("failed to clean rejected receipt baseline sidecar", "error", cleanupErr)
			}
			panic(recovered)
		}
	}()
	err = s.withExclusiveCommittedView(func() (installErr error) {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.receiptOriginCutMu.Lock()
		defer s.receiptOriginCutMu.Unlock()
		if err := validateOriginStateDominance(candidate.Origins, s.origins.States()); err != nil {
			return err
		}
		rotatedGeneration, err := nextReceiptRuntimeGeneration(receiptRuntime.generation)
		if err != nil {
			return err
		}
		currentReceipts, err := s.receiptStore.Snapshot()
		if err != nil {
			return fmt.Errorf("service: snapshot current receipt Store: %w", err)
		}
		effectiveHighWater := candidateReceipts.ClockHighWaterMillis
		if effectiveHighWater < currentReceipts.ClockHighWaterMillis {
			effectiveHighWater = currentReceipts.ClockHighWaterMillis
		}

		storeStage, err := s.receiptStore.BeginSnapshotInstall(candidateReceipts)
		if err != nil {
			return fmt.Errorf("service: stage receipt Store baseline: %w", err)
		}
		defer storeStage.Abort()
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
			effectiveHighWater,
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
			Digest:                 digest,
			Size:                   uint64(len(raw)),
			Epoch:                  receiptRuntime.epoch,
			PolicyFingerprint:      policyStore.PolicyFingerprint(),
			PreviousGeneration:     receiptRuntime.generation,
			RotatedGeneration:      rotatedGeneration,
			SourceLocalCutoff:      candidate.CutoffLocalSeq,
			ReceiptHighWaterMillis: effectiveHighWater,
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
		markerMayBeDurable = true
		_, err = s.log.CommitBoundaryWithPostRingPublication(marker, marker.RestoreFloor, func(mutationlog.Entry) {
			if fault := receiptRuntime.injectInstallFault(receiptBaselineAfterMarkerBeforePublish); fault != nil {
				panic(fault)
			}
			receiptRuntime.generation = rotatedGeneration
			clockStage.Commit()
			originStage.Commit()
			graphStage.Commit()
			storeStage.Commit()
		})
		if err != nil {
			if !isDefiniteReceiptBaselineWALAbort(err) {
				s.markReceiptCommitFaultLocked()
			}
			return fmt.Errorf("service: commit receipt baseline marker: %w", err)
		}
		if fault := receiptRuntime.injectInstallFault(receiptBaselineAfterPublish); fault != nil {
			panic(fault)
		}
		return nil
	})
	if err != nil {
		if markerMayBeDurable && !isDefiniteReceiptBaselineWALAbort(err) {
			return err
		}
		return errors.Join(err, sidecars.cleanup(committedBaselineDigest))
	}
	receiptRuntime.committedBaselineDigest = digest
	if cleanupErr := sidecars.cleanup(digest); cleanupErr != nil {
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

func isDefiniteReceiptBaselineWALAbort(err error) bool {
	var aborted *mutationlog.DefiniteWALAbort
	return errors.As(err, &aborted)
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
