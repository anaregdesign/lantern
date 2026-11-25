package dataruntime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestCreateLeasedReceiptWALCandidateFreshRestartAndExclusiveOwner(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	config.ClockHighWater = time.Now().Add(time.Minute)
	path := filepath.Join(t.TempDir(), "fresh.wal")
	configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
	owner, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{Capacity: 2}, time.Hour, configure)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close owned owner.Close: %v", err)
		}
	}(owner.Close)
	if seq, ok := owner.state.log.LastSeq(); ok || seq != 0 {
		t.Fatalf("fresh Log = %d, %v", seq, ok)
	}
	if seq, _, verified := owner.tip.Frontier(); !verified || seq != 0 {
		t.Fatalf("fresh tip = %d, verified %v", seq, verified)
	}
	if got := owner.journal.HighWaterMillis(); got != config.ClockHighWater.UnixMilli() {
		t.Fatalf("fresh clock high-water = %d, want %d", got, config.ClockHighWater.UnixMilli())
	}
	if competitor, err := mutationlog.AcquireFileWALLease(path); competitor != nil || !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		if competitor != nil {
			_ = competitor.Close()
		}
		t.Fatalf("fresh owner lost lease: %p, %v", competitor, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if candidate, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure); candidate != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing WAL treated as fresh: %p, %v", candidate, err)
	}
	restored, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{Capacity: 2}, time.Hour, configure)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close owned restored.Close: %v", err)
		}
	}(restored.Close)
	if seq, ok := restored.state.log.LastSeq(); ok || seq != 0 {
		t.Fatalf("restarted empty Log = %d, %v", seq, ok)
	}
	if got := restored.state.receipts.Stats().HighWaterMillis; got < config.ClockHighWater.UnixMilli() {
		t.Fatalf("restart moved clock backward: %d", got)
	}
}

func TestCreateLeasedReceiptWALCandidateFailsBeforeDiskForInvalidGraph(t *testing.T) {
	config, _ := receiptWALAuditFixture(t)
	path := filepath.Join(t.TempDir(), "invalid-graph.wal")
	configurationFailure := errors.New("index policy failed")
	for _, configure := range []func(*graphcache.GraphCache[string, *pb.Vertex]) error{
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return configurationFailure },
		func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			return graph.PutVertex("unwanted", &pb.Vertex{})
		},
	} {
		owner, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure)
		if owner != nil || err == nil {
			t.Fatalf("invalid graph yielded owner=%p, err=%v", owner, err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid graph created a WAL: %v", err)
		}
	}
}

func TestCreateLeasedReceiptWALCandidateLeavesPartialFilesFailClosed(t *testing.T) {
	for _, sidecar := range []string{".tip", ".clock"} {
		t.Run(sidecar, func(t *testing.T) {
			config, _ := receiptWALAuditFixture(t)
			path := filepath.Join(t.TempDir(), "partial.wal")
			if err := os.WriteFile(path+sidecar, []byte("stale sidecar"), 0o600); err != nil {
				t.Fatal(err)
			}
			configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
			owner, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure)
			if owner != nil || !errors.Is(err, os.ErrExist) {
				t.Fatalf("partial creation = %p, %v", owner, err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("partial WAL was removed instead of inspected: %v", err)
			}
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				t.Fatalf("partial creation leaked lease: %v", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			if candidate, err := createLeasedReceiptWALCandidate(path, config, mutationlog.Options{}, time.Hour, configure); candidate != nil || !errors.Is(err, os.ErrExist) {
				t.Fatalf("partial WAL silently reinitialized: %p, %v", candidate, err)
			}
			if candidate, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure); candidate != nil || err == nil {
				t.Fatalf("partial WAL served after failed creation: %p, %v", candidate, err)
			}
		})
	}
}

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
	receiptState, err := receipts.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("receipt WAL fresh Store snapshot: %w", err)
	}
	retired, err := newEmptyRetiredCatalogSnapshot(config, receiptState.ClockHighWaterMillis)
	if err != nil {
		return nil, fmt.Errorf("receipt WAL fresh retired catalog: %w", err)
	}
	opts.WAL = wal
	log = mutationlog.New(opts)
	walProvenance, err := log.FileWALTipProvenance(lease.Path())
	if err != nil {
		return nil, fmt.Errorf("receipt WAL fresh provenance: %w", err)
	}
	state := &receiptWALRecoveryCandidate{
		graph: graph, receipts: receipts, retired: retired, origins: newOriginStateTracker(), log: log,
	}
	return &receiptWALOwnedCandidate{
		state: state, logOwner: &receiptFreshWALLogOwner{log: log, wal: wal},
		journal: journal, tip: tip, lease: lease, walProvenance: walProvenance,
	}, nil
}

var _ io.Closer = (*receiptFreshWALLogOwner)(nil)
