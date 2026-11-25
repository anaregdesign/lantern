package dataruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func writeReceiptWALClockJournal(t *testing.T, path string, config mutationreceipt.Config, highWater int64) {
	t.Helper()
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt WAL path lease: %v", err)
		}
	}(lease.Close)
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := mutationreceipt.CreateClockJournal(lease.Path(), config.Epoch, store.PolicyFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt clock journal: %v", err)
		}
	}(journal.Close)
	if err := journal.Advance(highWater); err != nil {
		t.Fatal(err)
	}
}

func writeReceiptWALTipJournal(t *testing.T, path string, config mutationreceipt.Config) {
	t.Helper()
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt WAL path lease: %v", err)
		}
	}(lease.Close)
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	tip, err := mutationlog.CreateFileWALTipJournal(lease.Path(), receiptWALTipBinding(config.Epoch, store.PolicyFingerprint()))
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt WAL tip journal: %v", err)
		}
	}(tip.Close)
	if err := tip.VerifyAndCatchUp(lease.Path(), decodeReceiptWALUnion, validateReceiptWALUnionEntry); err != nil {
		t.Fatal(err)
	}
}

func ownedReceiptWALFixture(t *testing.T) (string, mutationreceipt.Config, mutationlog.Entry) {
	t.Helper()
	config, receiptEntry := receiptWALAuditFixture(t)
	seed := recoveryGraphPutEffectEntry(t, 0x72, receiptEntry.HLC.WallNs-1,
		&pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{
			Edge: &pb.Edge{Tail: "tail", Head: "present", Weight: 1,
				Expiration: timestamppb.New(time.Now().Add(2 * time.Hour))},
		}}})
	path := writeReceiptWALAuditEntries(t, seed, receiptEntry)
	return path, config, receiptEntry
}

func TestOpenLeasedReceiptWALCandidateKeepsClockAndPath(t *testing.T) {
	path, config, receiptEntry := ownedReceiptWALFixture(t)
	receipt := receiptEntry.Op.(*edgeDeleteReceiptEnvelope).Receipts[0]
	// A previous unlogged Lookup expired this receipt. The WAL still has
	// its historical commit bytes, and the wall clock rolled backward.
	forward := receipt.DeadlineMillis + 1
	writeReceiptWALClockJournal(t, path, config, forward)
	writeReceiptWALTipJournal(t, path, config)
	now := time.UnixMilli(receipt.DeadlineMillis - 1)
	owner, err := openLeasedReceiptWALCandidate(path, config, now, mutationlog.Options{Capacity: 2}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt WAL candidate owner: %v", err)
		}
	}(owner.Close)
	if got := owner.state.receipts.Stats(); got.HighWaterMillis < forward || got.Entries != 0 {
		t.Fatalf("recovered Store ignored durable expiry: %+v", got)
	}
	if got := owner.journal.HighWaterMillis(); got < forward {
		t.Fatalf("recovered journal moved backward: %d", got)
	}
	if seq, _, verified := owner.tip.Frontier(); !verified || seq != 2 {
		t.Fatalf("recovered WAL tip = %d, verified %v", seq, verified)
	}
	// An aborted Begin never reaches the WAL, yet its clock transition must
	// survive another restart through the newly attached journal sink.
	abortedAt := forward + 123
	tx, err := owner.state.receipts.Begin(time.UnixMilli(abortedAt))
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	if got := owner.journal.HighWaterMillis(); got != abortedAt {
		t.Fatalf("aborted Begin was not durably journaled: got %d, want %d", got, abortedAt)
	}
	if seq, ok := owner.state.log.LastSeq(); !ok || seq != 2 {
		t.Fatalf("live Log frontier = %d, %v", seq, ok)
	}
	if weight, ok := owner.state.graph.GetWeight("tail", "present"); ok || weight != 0 {
		t.Fatalf("recovered graph missed Delete: %g, %v", weight, ok)
	}
	if competitor, err := mutationlog.AcquireFileWALLease(path); competitor != nil || !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		if competitor != nil {
			_ = competitor.Close()
		}
		t.Fatalf("candidate released path while open: %p, %v", competitor, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.state.log.Append(&pb.Mutation{}, receiptEntry.HLC); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("closed candidate Log append = %v", err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("candidate Close leaked lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := openLeasedReceiptWALCandidate(path, config, now, mutationlog.Options{Capacity: 2}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close resumed receipt WAL candidate: %v", err)
		}
	}(resumed.Close)
	if got := resumed.state.receipts.Stats().HighWaterMillis; got != abortedAt {
		t.Fatalf("aborted Begin high-water was lost on second restart: got %d, want %d", got, abortedAt)
	}
}

func TestOpenLeasedReceiptWALCandidateRejectsMissingOrInvalidJournal(t *testing.T) {
	path, config, _ := ownedReceiptWALFixture(t)
	configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
	if owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure); owner != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing journal candidate = %p, %v", owner, err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("missing journal leaked path lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	writeReceiptWALClockJournal(t, path, config, time.Now().UnixMilli())
	if owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure); owner != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing tip candidate = %p, %v", owner, err)
	}
	writeReceiptWALTipJournal(t, path, config)
	wrong := config
	wrong.Epoch = mutationreceipt.Epoch{0x61}
	if owner, err := openLeasedReceiptWALCandidate(path, wrong, time.Now(), mutationlog.Options{}, time.Hour, configure); owner != nil || !errors.Is(err, mutationreceipt.ErrClockJournalBinding) {
		t.Fatalf("wrong-epoch candidate = %p, %v", owner, err)
	}
	lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("wrong-epoch candidate leaked path lease: %v", err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt WAL path lease: %v", err)
		}
	}(lease.Close)
}

func TestOpenLeasedReceiptWALCandidateRejectsLegacyEffectsAndReleasesOwner(t *testing.T) {
	config, receiptEntry := receiptWALAuditFixture(t)
	path := writeReceiptWALAuditEntries(t, auditGraphEntry(1), receiptEntry)
	writeReceiptWALClockJournal(t, path, config, time.Now().UnixMilli())
	writeReceiptWALTipJournal(t, path, config)
	owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil })
	if owner != nil || !errors.Is(err, errReceiptWALUnion) || !strings.Contains(err.Error(), "accepted-effect evidence") {
		t.Fatalf("legacy-effect candidate = %p, %v", owner, err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatalf("failed replay leaked path lease: %v", err)
	}
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close receipt WAL path lease: %v", err)
		}
	}(lease.Close)
}

func TestOpenLeasedReceiptWALCandidateRejectsTipBindingAndLostSuffix(t *testing.T) {
	configure := func(*graphcache.GraphCache[string, *pb.Vertex]) error { return nil }
	for _, tc := range []struct {
		name string
		edit func(*mutationreceipt.Config)
	}{
		{"wrong tip epoch", func(c *mutationreceipt.Config) { c.Epoch = mutationreceipt.Epoch{0x62} }},
		{"wrong tip policy", func(c *mutationreceipt.Config) { c.Retention = 2 * time.Hour }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, config, _ := ownedReceiptWALFixture(t)
			writeReceiptWALClockJournal(t, path, config, time.Now().UnixMilli())
			wrong := config
			tc.edit(&wrong)
			writeReceiptWALTipJournal(t, path, wrong)
			owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure)
			if owner != nil || !errors.Is(err, mutationlog.ErrFileWALTipBinding) {
				t.Fatalf("wrong tip binding = %p, %v", owner, err)
			}
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				t.Fatalf("wrong binding leaked lease: %v", err)
			}
			defer func(closeOwned func() error) {
				if err := closeOwned(); err != nil {
					t.Errorf("close receipt WAL path lease: %v", err)
				}
			}(lease.Close)
		})
	}
	t.Run("valid prefix loses committed suffix", func(t *testing.T) {
		path, config, _ := ownedReceiptWALFixture(t)
		writeReceiptWALClockJournal(t, path, config, time.Now().UnixMilli())
		writeReceiptWALTipJournal(t, path, config)
		cut, err := mutationlog.InspectFileWALCut(path, 1, decodeReceiptWALUnion, validateReceiptWALUnionEntry)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, cut.Offset); err != nil {
			t.Fatal(err)
		}
		owner, err := openLeasedReceiptWALCandidate(path, config, time.Now(), mutationlog.Options{}, time.Hour, configure)
		if owner != nil || !errors.Is(err, mutationlog.ErrFileWALCutUnavailable) {
			t.Fatalf("lost suffix candidate = %p, %v", owner, err)
		}
		lease, err := mutationlog.AcquireFileWALLease(path)
		if err != nil {
			t.Fatalf("lost suffix leaked lease: %v", err)
		}
		defer func(closeOwned func() error) {
			if err := closeOwned(); err != nil {
				t.Errorf("close receipt WAL path lease: %v", err)
			}
		}(lease.Close)
	})
}

func TestMatchingReceiptWALLogTailRejectsPayloadDrift(t *testing.T) {
	first := auditGraphEntry(1)
	staged := mutationlog.New(mutationlog.Options{})
	live := mutationlog.New(mutationlog.Options{})
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close staged mutation log: %v", err)
		}
	}(staged.Close)
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close live mutation log: %v", err)
		}
	}(live.Close)
	if _, err := staged.Append(first.Op, first.HLC); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Append(first.Op, first.HLC); err != nil {
		t.Fatal(err)
	}
	if err := matchingReceiptWALLogTail(staged, live); err != nil {
		t.Fatalf("matching tails = %v", err)
	}
	other := auditGraphEntry(1)
	other.Op.(*pb.Mutation).GetOp().GetPutVertex().Vertex.Key = "different"
	changed := mutationlog.New(mutationlog.Options{})
	defer func(closeOwned func() error) {
		if err := closeOwned(); err != nil {
			t.Errorf("close changed mutation log: %v", err)
		}
	}(changed.Close)
	if _, err := changed.Append(other.Op, other.HLC); err != nil {
		t.Fatal(err)
	}
	if err := matchingReceiptWALLogTail(staged, changed); !errors.Is(err, errReceiptWALUnion) {
		t.Fatalf("changed tail payload = %v", err)
	}
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
	return openLeasedReceiptWALCandidateInternal(
		path, config, now, opts, defaultTTL, configureGraph, hlc.NodeID{}, nil, nil, preflight...,
	)
}

func openLeasedReceiptWALCandidateWithBaseline(
	path string,
	config mutationreceipt.Config,
	now time.Time,
	opts mutationlog.Options,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
	nodeID hlc.NodeID,
	codec ReceiptBaselineArchiveCodec,
	validateBaseline func(receiptBaselineWALScan) error,
	preflight ...func(string, [sha256.Size]byte) error,
) (_ *receiptWALOwnedCandidate, err error) {
	if nodeID == (hlc.NodeID{}) || codec == nil || validateBaseline == nil {
		return nil, errors.New("service: baseline-aware WAL recovery requires node ID, archive codec, and generation validator")
	}
	return openLeasedReceiptWALCandidateInternal(
		path, config, now, opts, defaultTTL, configureGraph, nodeID, codec, validateBaseline, preflight...,
	)
}

func openLeasedReceiptWALCandidateInternal(
	path string,
	config mutationreceipt.Config,
	now time.Time,
	opts mutationlog.Options,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
	nodeID hlc.NodeID,
	codec ReceiptBaselineArchiveCodec,
	validateBaseline func(receiptBaselineWALScan) error,
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
	var baseline receiptBaselineWALScan
	if codec != nil {
		err = lease.WithPath(func(canonicalPath string) error {
			var scanErr error
			baseline, scanErr = scanReceiptBaselineWAL(canonicalPath, config, nodeID)
			return scanErr
		})
		if err != nil {
			return nil, err
		}
		if err := validateBaseline(baseline); err != nil {
			return nil, err
		}
	}
	var state *receiptWALRecoveryCandidate
	if baseline.hasMarker {
		err = lease.WithPath(func(canonicalPath string) error {
			var resumeErr error
			state, logOwner, resumeErr = resumeReceiptBaselineWALCandidate(
				context.Background(),
				canonicalPath,
				config,
				now,
				opts,
				defaultTTL,
				configureGraph,
				codec,
				journal.HighWaterMillis(),
				tip,
				baseline,
			)
			return resumeErr
		})
	} else {
		state, err = stageEffectCompleteReceiptWALCandidate(lease, config, now, opts, defaultTTL, configureGraph)
	}
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
	if !baseline.hasMarker {
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
	}
	walProvenance, err := state.log.FileWALTipProvenance(lease.Path())
	if err != nil {
		return nil, fmt.Errorf("receipt WAL live provenance: %w", err)
	}
	if codec != nil {
		err = lease.WithPath(func(canonicalPath string) error {
			return (receiptBaselineSidecarStore{walPath: canonicalPath}).cleanup(
				baseline.marker.reference(),
			)
		})
		if err != nil {
			return nil, err
		}
	}
	return &receiptWALOwnedCandidate{
		state: state, logOwner: logOwner, journal: journal, tip: tip, lease: lease,
		walProvenance: walProvenance, baseline: baseline,
	}, nil
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
