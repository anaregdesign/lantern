package service

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type receiptFreshWALLogOwner struct {
	log *mutationlog.Log
	wal *mutationlog.FileWAL
}

func (o *receiptFreshWALLogOwner) Close() error {
	return errors.Join(o.log.Close(), o.wal.Close())
}

// createLeasedReceiptWALCandidate builds a new, empty, unpublished durable
// image. Its graph policy and derived search index are checked before file
// creation; then one lease covers FileWAL, tip, clock journal, and Log through
// Close. Existing WAL/sidecars are never overwritten or silently interpreted
// as fresh. A partial creation failure may leave files behind and must fail
// closed on the next start rather than deleting uncertain durable bytes.
// The production runtime must add and bind endpoint-generation metadata before
// installing the candidate; the candidate itself exposes no receipt capability.
func createLeasedReceiptWALCandidate(
	path string,
	config mutationreceipt.Config,
	opts mutationlog.Options,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (_ *receiptWALOwnedCandidate, err error) {
	if opts.WAL != nil || configureGraph == nil {
		return nil, fmt.Errorf("%w: fresh WAL requires no configured writer and a graph policy", errReceiptWALUnion)
	}
	policyStore, err := mutationreceipt.New(config)
	if err != nil {
		return nil, err
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](defaultTTL)
	if err := configureGraph(graph); err != nil {
		return nil, fmt.Errorf("receipt WAL graph configuration: %w", err)
	}
	if !emptyReceiptWALRecoveryGraph(graph) {
		return nil, fmt.Errorf("%w: graph configuration populated the fresh cache", errReceiptWALUnion)
	}
	if err := graph.CompleteSearchIndexRecovery(); err != nil {
		return nil, fmt.Errorf("receipt WAL search rebuild: %w", err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	var wal *mutationlog.FileWAL
	var tip *mutationlog.FileWALTipJournal
	var journal *mutationreceipt.ClockJournal
	var log *mutationlog.Log
	defer func() {
		if err != nil {
			if log != nil {
				err = errors.Join(err, log.Close())
			}
			if wal != nil {
				err = errors.Join(err, wal.Close())
			}
			err = errors.Join(err, journal.Close(), tip.Close(), lease.Close())
		}
	}()
	policy := policyStore.PolicyFingerprint()
	err = lease.WithPath(func(canonicalPath string) error {
		var createErr error
		wal, createErr = mutationlog.CreateFileWAL(canonicalPath, encodeReceiptWALUnion)
		if createErr != nil {
			return createErr
		}
		tip, createErr = mutationlog.CreateFileWALTipJournal(canonicalPath, receiptWALTipBinding(config.Epoch, policy))
		if createErr != nil {
			return createErr
		}
		unexpected := func([]byte) (mutationlog.MutationOp, error) { return nil, errReceiptWALUnion }
		if createErr = tip.VerifyAndCatchUp(canonicalPath, unexpected, func(mutationlog.Entry) error {
			return errReceiptWALUnion
		}); createErr != nil {
			return createErr
		}
		if createErr = wal.BindTipJournal(tip); createErr != nil {
			return createErr
		}
		journal, createErr = mutationreceipt.CreateClockJournal(canonicalPath, config.Epoch, policy)
		return createErr
	})
	if err != nil {
		return nil, err
	}
	receipts, err := mutationreceipt.NewWithClockHighWaterSink(config, journal.Advance)
	if err != nil {
		return nil, fmt.Errorf("receipt WAL fresh clock binding: %w", err)
	}
	opts.WAL = wal
	log = mutationlog.New(opts)
	walProvenance, err := log.FileWALTipProvenance(lease.Path())
	if err != nil {
		return nil, fmt.Errorf("receipt WAL fresh provenance: %w", err)
	}
	state := &receiptWALRecoveryCandidate{
		graph: graph, receipts: receipts, origins: newOriginStateTracker(), log: log,
	}
	return &receiptWALOwnedCandidate{
		state: state, logOwner: &receiptFreshWALLogOwner{log: log, wal: wal},
		journal: journal, tip: tip, lease: lease, walProvenance: walProvenance,
	}, nil
}

var _ io.Closer = (*receiptFreshWALLogOwner)(nil)
