package dataruntime

import (
	"errors"
	"io"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

// receiptWALOwnedCandidate holds a fully replayed but unpublished image and
// its live Log, clock/tip journals, and exclusive path lease. ServingRuntime
// installs it only after binding endpoint-generation metadata and restoring
// the HLC; a matching tip still cannot certify an archive cut on its own.
type receiptWALOwnedCandidate struct {
	state         *receiptWALRecoveryCandidate
	logOwner      io.Closer
	journal       *mutationreceipt.ClockJournal
	tip           *mutationlog.FileWALTipJournal
	lease         *mutationlog.FileWALLease
	walProvenance *mutationlog.FileWALTipProvenance
	baseline      receiptBaselineWALScan
}

// Close stops appends, closes both journals, then releases path ownership.
func (c *receiptWALOwnedCandidate) Close() error {
	if c == nil {
		return nil
	}
	return errors.Join(c.logOwner.Close(), c.journal.Close(), c.tip.Close(), c.lease.Close())
}
