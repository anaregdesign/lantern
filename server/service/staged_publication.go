package service

import (
	"errors"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

// stagedPublication owns one caller's WAL attempt, not its typed graph plan or
// rollback journals. The caller holds replicationCutMu and receiptOriginCutMu,
// defers failClosedOnPanic before acquiring Store/Graph/origin stages, and keeps
// those stages' Abort defers. Thus rollback finishes before a panic marks the
// service faulted, and the outer gates exclude observers throughout.
//
// This protocol also serves standalone Create without a receipt Store. Legacy
// graph-first Append and SystemMetadata's install-before-ring publication have
// different contracts and must not use it.
type stagedPublication struct {
	service   *LanternService
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
// precedes dispatcher backpressure; the service publication gates remain held.
// releaseGraph must only release a successfully staged graph transaction.
func (p *stagedPublication) commit(
	op mutationlog.MutationOp,
	ts hlc.Timestamp,
	store *mutationreceipt.Tx,
	releaseGraph func(),
	origin *originRowStage,
) error {
	p.attempted = true
	_, err := p.service.log.CommitWithPostRingPublication(op, ts, func(mutationlog.Entry) {
		if store != nil {
			store.Commit()
		}
		releaseGraph()
		origin.Commit()
	})
	if err != nil && stagedPublicationRequiresRecovery(err) {
		p.service.markReceiptCommitFaultLocked()
	}
	return err
}

// Preserve the existing receipt/Create classifier, including wrapped sentinel
// precedence. Family-specific resource/capacity response codes stay in adapters.
// Neither cancellation nor a generic WAL error certifies a definite abort.
func stagedPublicationRequiresRecovery(err error) bool {
	var definite *mutationlog.DefiniteWALAbort
	return err != nil && !errors.As(err, &definite) &&
		!errors.Is(err, mutationlog.ErrClosed) && !errors.Is(err, mutationlog.ErrSeqExhausted)
}

// markReceiptCommitFaultLocked has no local retry path: a generic WAL error
// may already have committed the envelope. Only a future certified replay or
// receipt-bearing Snapshot may clear this fail-stop state.
func (s *LanternService) markReceiptCommitFaultLocked() {
	if s.receiptCommitFaulted {
		return
	}
	s.receiptCommitFaulted = true
	if s.publicationFaultCount == 0 {
		close(s.publicationFaultCh)
	}
	s.publicationFaultCount++
}
