package service

import (
	"bytes"
	"crypto/sha256"
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
// its live Log, clock/tip journals, and exclusive path lease. ServingRuntime
// installs it only after binding endpoint-generation metadata and restoring
// the HLC; a matching tip still cannot certify an archive cut on its own.
type receiptWALOwnedCandidate struct {
	state    *receiptWALRecoveryCandidate
	logOwner io.Closer
	journal  *mutationreceipt.ClockJournal
	tip      *mutationlog.FileWALTipJournal
	lease    *mutationlog.FileWALLease
}

// Close stops appends, closes both journals, then releases path ownership.
func (c *receiptWALOwnedCandidate) Close() error {
	if c == nil {
		return nil
	}
	return errors.Join(c.logOwner.Close(), c.journal.Close(), c.tip.Close(), c.lease.Close())
}

func receiptWALTipBinding(epoch mutationreceipt.Epoch, policy [sha256.Size]byte) [sha256.Size]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("lantern-receipt-wal-tip-v1\x00"))
	_, _ = h.Write(epoch[:])
	_, _ = h.Write(policy[:])
	var binding [sha256.Size]byte
	copy(binding[:], h.Sum(nil))
	return binding
}

// openLeasedReceiptWALCandidate holds one lease across journal validation,
// strict effect-complete graph/receipt replay, a second Log/WAL resume pass,
// and the returned owner's lifetime. The second pass keeps its writer open;
// its bounded ring must exactly match the detached replay before return.
// An optional preflight runs under that lease after policy validation but
// before any journal can advance; the production runtime uses it to validate
// endpoint-generation metadata without mutating a mismatched durable cut.
// The caller must discard this candidate until the complete WAL cut and
// endpoint generation have been certified elsewhere.
func openLeasedReceiptWALCandidate(
	path string,
	config mutationreceipt.Config,
	now time.Time,
	opts mutationlog.Options,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
	preflight ...func(string, [sha256.Size]byte) error,
) (_ *receiptWALOwnedCandidate, err error) {
	if len(preflight) > 1 {
		return nil, fmt.Errorf("%w: at most one preflight is supported", errReceiptWALUnion)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	var journal *mutationreceipt.ClockJournal
	var tip *mutationlog.FileWALTipJournal
	var logOwner io.Closer
	defer func() {
		if err != nil {
			if logOwner != nil {
				err = errors.Join(err, logOwner.Close())
			}
			err = errors.Join(err, journal.Close(), tip.Close(), lease.Close())
		}
	}()
	policyStore, err := mutationreceipt.New(config)
	if err != nil {
		return nil, err
	}
	policy := policyStore.PolicyFingerprint()
	if len(preflight) == 1 && preflight[0] != nil {
		if err := lease.WithPath(func(canonicalPath string) error {
			return preflight[0](canonicalPath, policy)
		}); err != nil {
			return nil, err
		}
	}
	journal, err = mutationreceipt.ResumeClockJournal(lease.Path(), config.Epoch, policy)
	if err != nil {
		return nil, fmt.Errorf("receipt WAL clock journal: %w", err)
	}
	tip, err = mutationlog.ResumeFileWALTipJournal(lease.Path(), receiptWALTipBinding(config.Epoch, policy))
	if err != nil {
		return nil, fmt.Errorf("receipt WAL tip journal: %w", err)
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
		liveLog, logOwner, resumeErr = mutationlog.ResumeLogFromFileWALWithTip(canonicalPath, opts, encodeReceiptWALUnion, decodeReceiptWALUnion, validateReceiptWALUnionEntry, func(mutationlog.Entry) error {
			return nil // The strict first pass installed the detached application image.
		}, tip)
		return resumeErr
	})
	if err != nil {
		return nil, err
	}
	if err := matchingReceiptWALLogTail(state.log, liveLog); err != nil {
		return nil, err
	}
	state.log = liveLog
	return &receiptWALOwnedCandidate{state: state, logOwner: logOwner, journal: journal, tip: tip, lease: lease}, nil
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
