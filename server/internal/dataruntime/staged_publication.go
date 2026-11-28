package dataruntime

import (
	"errors"
	"log/slog"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// stagedPublication owns one caller's WAL attempt, not its typed graph plan or
// rollback journals. The caller holds replicationCutMu and receiptOriginCutMu,
// defers failClosedOnPanic before acquiring Store/Graph/origin stages, and keeps
// those stages' Abort defers. Thus rollback finishes before a panic marks the
// executor faulted, and the outer gates exclude observers throughout.
//
// Standalone Create and stored contribution Delete can use this protocol with
// no receipt Store. Legacy graph-first Append and SystemMetadata's
// install-before-ring publication have different contracts.
type stagedPublication struct {
	service   *Executor
	attempted bool
}

func (p *stagedPublication) failClosedOnPanic() {
	if value := recover(); value != nil {
		if p.attempted {
			p.service.markReceiptCommitFaultLocked()
		}
		panic(value)
	}
}

// commit releases already-staged visibility after ring/sequence installation,
// while Log.mu still excludes log readers. Store -> Graph -> origin release
// precedes dispatcher backpressure; the executor publication gates remain held.
// releaseGraph must only release a successfully staged graph transaction.
func (p *stagedPublication) commit(
	op mutationlog.MutationOp,
	ts hlc.Timestamp,
	store *mutationreceipt.Tx,
	releaseGraph func(),
	origin *originRowStage,
	staged ...graphcache.StorageRowWriter[string, *pb.Vertex],
) error {
	p.attempted = true
	release := func(mutationlog.Entry) {
		if store != nil {
			store.Commit()
		}
		releaseGraph()
		origin.Commit()
	}
	var err error
	if p.service.runtime != nil && p.service.runtime.tarp != nil {
		write := func(w mutationlog.WAL, entry mutationlog.Entry) error {
			if w != p.service.runtime.tarp || len(staged) != 1 || staged[0] == nil {
				return &mutationlog.DefiniteWALAbort{Cause: errors.New("Tarp publication requires the exact staged graph and owner")}
			}
			return p.service.runtime.tarp.write(entry, staged[0])
		}
		_, err = p.service.log.CommitPrepared(op, ts, write, release)
	} else {
		_, err = p.service.log.CommitWithPostRingPublication(op, ts, release)
	}
	if err != nil && stagedPublicationRequiresRecovery(err) {
		p.service.markReceiptCommitFaultLocked()
	}
	return err
}

// Uncertain outcomes take precedence over abort/readiness sentinels: a WAL can
// return ErrClosed or ErrSeqExhausted as its underlying error, and the Log then
// joins it with ErrWALIndeterminate. Family-specific response codes stay in the
// adapters; they cannot certify whether the durable write was absent.
func stagedPublicationRequiresRecovery(err error) bool {
	if errors.Is(err, mutationlog.ErrWALIndeterminate) ||
		errors.Is(err, mutationlog.ErrPublicationInterrupted) {
		return true
	}
	var definite *mutationlog.DefiniteWALAbort
	return err != nil && !errors.As(err, &definite) &&
		!errors.Is(err, mutationlog.ErrClosed) && !errors.Is(err, mutationlog.ErrSeqExhausted)
}

// markReceiptCommitFaultLocked has no local retry path: a generic WAL error
// may already have committed the envelope. Only a future certified replay or
// receipt-bearing Snapshot may clear this fail-stop state.
func (s *Executor) markReceiptCommitFaultLocked() {
	if s.receiptCommitFaulted {
		return
	}
	faultCountBefore := s.publicationFaultCount
	s.receiptCommitFaulted = true
	if s.publicationFaultCount == 0 {
		close(s.publicationFaultCh)
	}
	s.publicationFaultCount++
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("publication fault transition",
		slog.String("event", "receipt_commit_fault_set"),
		slog.String("fault_source", "receipt_commit"),
		slog.String("source", "mark_receipt_commit_fault"),
		slog.Bool("receipt_fault_before", false),
		slog.Bool("receipt_fault_after", true),
		slog.Bool("snapshot_fault_at_set", s.snapshotInstallFaulted),
		slog.Int("fault_count_before", faultCountBefore),
		slog.Int("fault_count_after", s.publicationFaultCount))
}
