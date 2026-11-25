package dataruntime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/proto"
)

// A failed attempt may have closed the publication gate before changing any
// graph or origin rows. Retry only after proving the still-fenced live state
// equals the active durable cut. Partial merges and uncertain commits stay
// unavailable until the process reopens the durable generation.
func (s *Executor) canRetryTarpSnapshot(ctx context.Context) (bool, error) {
	if !s.snapshotInstallFaulted || s.receiptCommitFaulted || s.publicationFaultCount != 1 || s.snapshotGenerationAttempted || s.storageFault() != nil {
		return false, nil
	}
	// Native replay marks the first live write before invoking its applier.
	// Header-only cancellation leaves the fault closed but can retry immediately.
	if s.snapshotReplayStarted != nil {
		return !s.snapshotReplayStarted(), ctx.Err()
	}
	// Trusted in-process users can mutate GraphCache directly. Certify their
	// raw physical state when the native replay marker is unavailable.
	r := s.runtime.tarp
	view, err := r.db.Read()
	if err != nil {
		return false, err
	}
	defer func() { _ = view.Close() }()
	cut, err := r.readCut(view.Snapshot, view.Generation)
	if err != nil {
		return false, err
	}
	origins := s.origins.States()
	last, _ := s.log.LastSeq()
	if cut.last != last || cut.origins != uint64(len(origins)) {
		return false, nil
	}
	for _, row := range origins {
		raw, err := view.Snapshot.Get(tarpOriginKey(view.Generation, row.Origin))
		if errors.Is(err, tarp.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		stored, err := readTarpOrigin(raw, row.Origin)
		if err != nil {
			return false, err
		}
		if stored != row {
			return false, nil
		}
	}
	rows, err := diskRows()
	if err != nil {
		return false, err
	}
	return s.runtime.graph.MatchesStorageGeneration(ctx, rows, view)
}

// The snapshot guard has completed the graph-only HLC merge, checked EOF
// and original authority, and holds the publication gate. No incoming frame
// was written to the active database during replay. A crash leaves the old
// state until the complete candidate graph and origin cut publish together.
func (s *Executor) commitTarpGraphSnapshot(ctx context.Context, check func() error) error {
	if s.runtime.ReceiptsEnabled() {
		return errors.New("receipt database requires a whole-state Snapshot")
	}
	r := s.runtime.tarp
	target, err := r.db.BeginGeneration()
	if err != nil {
		return err
	}
	defer target.Abort()
	rows, err := graphstorage.Rows()
	if err != nil {
		return err
	}
	if err = s.runtime.graph.WriteStorageGeneration(ctx, rows, target); err != nil {
		return err
	}
	origins := s.origins.States()
	if err = stageTarpOrigins(ctx, target, origins); err != nil {
		return err
	}
	view, err := r.db.Read()
	if err != nil {
		return err
	}
	defer view.Close()
	old, err := r.readCut(view.Snapshot, view.Generation)
	if err != nil {
		return err
	}
	if old.last >= math.MaxUint64-1 {
		return mutationlog.ErrSeqExhausted
	}
	digest, err := tarpGenerationDigest(ctx, target.ID(), view.Snapshot, old.digest)
	if err != nil {
		return err
	}
	next := tarpRelayCut{first: old.last + 2, last: old.last + 1, origins: uint64(len(origins)), floor: s.clock.Now(), digest: digest}
	err = target.Apply(func(gen uint64, b graphstore.Writer) error {
		b.Put(tarpServiceKey(gen, tarpRelayManifest), next.encode(r.namespace))
		return nil
	})
	if err != nil {
		return err
	}
	_, err = s.log.CommitPreparedBoundary(digest, next.floor, func(w mutationlog.WAL, entry mutationlog.Entry) error {
		if w != r || next.last != entry.Seq {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("Tarp snapshot and log frontiers differ")}
		}
		return target.Publish(ctx, func(active, staged uint64, snap *tarp.Snapshot) error {
			if err := check(); err != nil {
				return err
			}
			actual, err := r.readCut(snap, staged)
			if err != nil {
				return err
			}
			if active != view.Generation || actual != next {
				return errors.New("Tarp snapshot publication cut changed")
			}
			return nil
		})
	}, nil)
	if err == nil {
		s.runtime.graph.StorageGenerationPublished()
	}
	return err
}

func tarpGenerationDigest(ctx context.Context, gen uint64, snap *tarp.Snapshot, previous [32]byte) ([32]byte, error) {
	h := sha256.New()
	h.Write([]byte("Lantern Tarp Snapshot 1"))
	h.Write(previous[:])
	for _, domain := range []byte{'G', 'S'} {
		prefix := binary.BigEndian.AppendUint64([]byte{domain}, gen)
		lo, hi := tarp.PrefixRange(prefix)
		for row, e := range snap.ScanRange(ctx, lo, hi) {
			if e != nil {
				return [32]byte{}, fmt.Errorf("hash Tarp snapshot: %w", e)
			}
			for _, part := range [][]byte{row.Key, row.Value} {
				var size [8]byte
				binary.BigEndian.PutUint64(size[:], uint64(len(part)))
				h.Write(size[:])
				h.Write(part)
			}
		}
	}
	var digest [32]byte
	h.Sum(digest[:0])
	return digest, ctx.Err()
}

// stageTarpReceiptBaseline stores only the detached, verified candidate.
// The final cut rechecks receipt dominance, graph policy and origin continuity.
func (s *Executor) stageTarpReceiptBaseline(ctx context.Context, item preparedReceiptBaselineAttempt) (*graphstore.Generation, tarpReceiptCut, error) {
	var cut tarpReceiptCut
	target, err := s.runtime.tarp.db.BeginGeneration()
	if err != nil {
		return nil, cut, err
	}
	ok := false
	defer func() {
		if !ok {
			target.Abort()
		}
	}()
	rows, err := graphstorage.Rows()
	if err != nil {
		return nil, cut, err
	}
	if err = item.candidate.Graph.WriteStorageGeneration(ctx, rows, target); err != nil {
		return nil, cut, err
	}
	active := item.capture.Receipts
	cut = tarpReceiptCut{epoch: active.Epoch, policy: active.PolicyFingerprint, highWater: active.ClockHighWaterMillis}
	writeReceipts := func(list []mutationreceipt.Receipt, retired *mutationreceipt.Epoch) error {
		for start := 0; start < len(list); {
			if err := ctx.Err(); err != nil {
				return err
			}
			// A single receipt can contain long key provenance. Limit both
			// operations and bytes per staging commit; no partial chunk is live.
			err := target.Apply(func(gen uint64, w graphstore.Writer) error {
				budget := 0
				for n := 0; start < len(list) && n < 128 && budget < 1<<20; n++ {
					row := list[start]
					raw, e := encodeTarpReceipt(row)
					if e != nil {
						return e
					}
					var key []byte
					if retired == nil {
						key = tarpServiceKey(gen, tarpReceiptRow)
						cut.count++
						cut.toggleRow(raw)
					} else {
						identity := append(append([]byte(nil), retired[:]...), row.ID[:]...)
						cut.addRetired(tarpRetiredRow, identity, raw)
						key = append(tarpServiceKey(gen, tarpRetiredRow), retired[:]...)
					}
					w.Put(append(key, row.ID[:]...), raw)
					budget += len(raw)
					start++
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
	if err = writeReceipts(active.Receipts, nil); err != nil {
		return nil, cut, err
	}
	for _, member := range item.capture.Retired.Epochs {
		p := member.Policy
		cfg := mutationreceipt.Config{Epoch: p.Epoch, Retention: p.Retention, MaxEntries: p.MaxEntries, MaxBytes: p.MaxBytes}
		wire := receiptSnapshotPolicy(cfg, member.State.PolicyFingerprint)
		raw, e := proto.MarshalOptions{Deterministic: true}.Marshal(wire)
		if e != nil {
			return nil, cut, e
		}
		if e = target.Apply(func(gen uint64, w graphstore.Writer) error {
			cut.addRetired(tarpRetiredPolicy, p.Epoch[:], raw)
			w.Put(append(tarpServiceKey(gen, tarpRetiredPolicy), p.Epoch[:]...), raw)
			return nil
		}); e != nil {
			return nil, cut, e
		}
		if e = writeReceipts(member.State.Receipts, &p.Epoch); e != nil {
			return nil, cut, e
		}
	}
	if err = stageTarpOrigins(ctx, target, item.candidate.Origins); err != nil {
		return nil, cut, err
	}
	ok = true
	return target, cut, nil
}

func stageTarpOrigins(ctx context.Context, target *graphstore.Generation, origins []OriginState) error {
	for begin := 0; begin < len(origins); begin += 256 {
		chunk := origins[begin:min(begin+256, len(origins))]
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := target.Apply(func(gen uint64, w graphstore.Writer) error {
			for _, state := range chunk {
				w.Put(tarpOriginKey(gen, state.Origin), encodeTarpOrigin(state.LastSeq, state.LastHLC))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

type tarpReceiptInstall struct {
	target      *graphstore.Generation
	ledger      tarpReceiptCut
	originCount int
}

func (s *Executor) publishTarpReceiptBaseline(ctx context.Context, stage *tarpReceiptInstall, marker receiptBaselineMarker, release func(mutationlog.Entry)) error {
	r := s.runtime.tarp
	view, err := r.db.Read()
	if err != nil {
		return err
	}
	defer view.Close()
	previous, err := r.readCut(view.Snapshot, view.Generation)
	if err != nil {
		return err
	}
	if previous.last >= math.MaxUint64-1 {
		return mutationlog.ErrSeqExhausted
	}
	raw, err := marshalReceiptBaselineMarker(marker)
	if err != nil {
		return err
	}
	cut := tarpRelayCut{receipts: true, first: previous.last + 2, last: previous.last + 1, origins: uint64(stage.originCount), floor: marker.RestoreFloor, digest: tarpEntryDigest(previous.digest, previous.last+1, raw)}
	stage.ledger.endpoint = marker.RotatedGeneration
	stage.ledger.baselineDigest = marker.Digest
	err = stage.target.Apply(func(gen uint64, b graphstore.Writer) error {
		b.Put(tarpServiceKey(gen, tarpRelayManifest), cut.encode(r.namespace))
		b.Put(tarpServiceKey(gen, tarpReceiptManifest), stage.ledger.encode())
		b.Put(tarpServiceKey(gen, tarpReceiptBaseline), raw)
		return nil
	})
	if err != nil {
		return &mutationlog.DefiniteWALAbort{Cause: err}
	} // no active head changed
	_, err = s.log.CommitPreparedBoundary(marker, marker.RestoreFloor, func(w mutationlog.WAL, entry mutationlog.Entry) error {
		if w != r || entry.Seq != cut.last {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("Tarp receipt baseline position mismatch")}
		}
		err := stage.target.Publish(ctx, func(active, staged uint64, snap *tarp.Snapshot) error {
			if active != view.Generation {
				return graphstore.ErrGenerationChanged
			}
			got, err := r.readCut(snap, staged)
			if err != nil {
				return err
			}
			ledger, err := readTarpReceiptCut(snap, staged)
			if err != nil {
				return err
			}
			if got != cut || ledger != stage.ledger {
				return errors.New("Tarp receipt baseline cut mismatch")
			}
			if err := replication.CheckSnapshotAdmission(ctx); err != nil {
				return &mutationlog.DefiniteWALAbort{Cause: err}
			}
			return nil
		})
		if errors.Is(err, graphstore.ErrGenerationChanged) {
			return &mutationlog.DefiniteWALAbort{Cause: errReceiptBaselineInstallDrift}
		}
		return err
	}, release)
	return err
}
