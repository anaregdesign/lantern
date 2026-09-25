package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// receiptWALOwnedCandidate holds a fully replayed but unpublished image and
// its live Log, journal, and exclusive path lease. No production provider
// installs it: a valid WAL prefix can still omit a later durable receipt, so
// archive/suffix proof and an atomic serving cut are required before any
// receipt status or admission can use this state.
type receiptWALOwnedCandidate struct {
	state    *receiptWALRecoveryCandidate
	logOwner io.Closer
	journal  *mutationreceipt.ClockJournal
	lease    *mutationlog.FileWALLease
}

// Close stops appends, closes the clock journal, then releases path ownership.
func (c *receiptWALOwnedCandidate) Close() error {
	if c == nil {
		return nil
	}
	return errors.Join(c.logOwner.Close(), c.journal.Close(), c.lease.Close())
}

// openLeasedReceiptWALCandidate holds one lease across journal validation,
// strict effect-complete graph/receipt replay, a second Log/WAL resume pass,
// and the returned owner's lifetime. The second pass keeps its writer open;
// its bounded ring must exactly match the detached replay before return.
// The caller must discard this candidate until a trusted complete WAL/archive
// cut and endpoint generation have been certified elsewhere.
func openLeasedReceiptWALCandidate(
	path string,
	config mutationreceipt.Config,
	now time.Time,
	opts mutationlog.Options,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (_ *receiptWALOwnedCandidate, err error) {
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	var journal *mutationreceipt.ClockJournal
	var logOwner io.Closer
	defer func() {
		if err != nil {
			if logOwner != nil {
				err = errors.Join(err, logOwner.Close())
			}
			err = errors.Join(err, journal.Close(), lease.Close())
		}
	}()
	policyStore, err := mutationreceipt.New(config)
	if err != nil {
		return nil, err
	}
	journal, err = mutationreceipt.ResumeClockJournal(lease.Path(), config.Epoch, policyStore.PolicyFingerprint())
	if err != nil {
		return nil, fmt.Errorf("receipt WAL clock journal: %w", err)
	}
	if config.ClockHighWater.IsZero() || config.ClockHighWater.UnixMilli() < journal.HighWaterMillis() {
		config.ClockHighWater = time.UnixMilli(journal.HighWaterMillis())
	}
	state, err := stageEffectCompleteReceiptWALCandidate(lease, config, now, opts, defaultTTL, configureGraph)
	if err != nil {
		return nil, err
	}
	storeSnapshot, err := state.receipts.Snapshot()
	if err != nil {
		return nil, err
	}
	state.receipts, err = mutationreceipt.NewFromSnapshotWithClockHighWaterSink(config, storeSnapshot, journal.Advance)
	if err != nil {
		return nil, fmt.Errorf("receipt WAL clock binding: %w", err)
	}
	var liveLog *mutationlog.Log
	err = lease.WithPath(func(canonicalPath string) error {
		var resumeErr error
		liveLog, logOwner, resumeErr = mutationlog.ResumeLogFromFileWAL(canonicalPath, opts, encodeReceiptWALUnion, decodeReceiptWALUnion, func(mutationlog.Entry) error {
			return nil // The strict first pass installed the detached application image.
		})
		return resumeErr
	})
	if err != nil {
		return nil, err
	}
	if err := matchingReceiptWALLogTail(state.log, liveLog); err != nil {
		return nil, err
	}
	state.log = liveLog
	return &receiptWALOwnedCandidate{state: state, logOwner: logOwner, journal: journal, lease: lease}, nil
}

func matchingReceiptWALLogTail(staged, live *mutationlog.Log) error {
	if staged == nil || live == nil {
		return fmt.Errorf("%w: restored Log is missing", errReceiptWALUnion)
	}
	stagedLast, stagedHas := staged.LastSeq()
	liveLast, liveHas := live.LastSeq()
	stagedRows, liveRows := staged.RetainedEntries(), live.RetainedEntries()
	if stagedLast != liveLast || stagedHas != liveHas || len(stagedRows) != len(liveRows) {
		return fmt.Errorf("%w: live Log frontier differs from replay", errReceiptWALUnion)
	}
	for i := range stagedRows {
		a, b := stagedRows[i], liveRows[i]
		if a.Seq != b.Seq || !a.HLC.Equal(b.HLC) {
			return fmt.Errorf("%w: live Log tail differs at index %d", errReceiptWALUnion, i)
		}
		aBytes, aErr := encodeReceiptWALUnion(a.Op)
		bBytes, bErr := encodeReceiptWALUnion(b.Op)
		if aErr != nil || bErr != nil || !bytes.Equal(aBytes, bBytes) {
			return fmt.Errorf("%w: live Log payload differs at index %d", errReceiptWALUnion, i)
		}
	}
	return nil
}
