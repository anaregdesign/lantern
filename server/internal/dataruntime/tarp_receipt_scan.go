package dataruntime

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"slices"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/proto"
)

// The policy catalog is bounded by the configured receipt capacity. All
// original outcome bytes are read one operation at a time from a pinned cut.
type tarpReceiptTable struct {
	gen           uint64
	snap          *tarp.Snapshot
	cut           tarpReceiptCut
	policies      map[mutationreceipt.Epoch]mutationreceipt.Config
	retired       []mutationreceipt.Epoch
	policyWitness tarpReceiptCut
}

func openTarpReceiptTable(ctx context.Context, gen uint64, snap *tarp.Snapshot, active mutationreceipt.Config) (*tarpReceiptTable, error) {
	cut, err := readTarpReceiptCut(snap, gen)
	if err != nil {
		return nil, err
	}
	validator, err := mutationreceipt.New(active)
	if err != nil {
		return nil, err
	}
	if cut.epoch != active.Epoch || cut.policy != validator.PolicyFingerprint() {
		return nil, errors.New("incompatible active Tarp receipt policy")
	}
	t := &tarpReceiptTable{gen: gen, snap: snap, cut: cut, policies: map[mutationreceipt.Epoch]mutationreceipt.Config{active.Epoch: active}}
	prefix := tarpServiceKey(gen, tarpRetiredPolicy)
	for row, err := range snap.ScanPrefix(ctx, prefix) {
		if err != nil {
			return nil, err
		}
		var wire pb.ReceiptPolicy
		if err = proto.Unmarshal(row.Value, &wire); err != nil {
			return nil, err
		}
		cfg, _, err := receiptSnapshotStoreState(&wire, uint64(cut.highWater))
		if err != nil {
			return nil, err
		}
		if _, dup := t.policies[cfg.Epoch]; dup || !bytes.Equal(row.Key[len(prefix):], cfg.Epoch[:]) {
			return nil, errors.New("stored retired receipt policy identity mismatch")
		}
		if len(t.retired) >= active.MaxEntries {
			return nil, mutationreceipt.ErrCapacity
		}
		t.retired = append(t.retired, cfg.Epoch)
		t.policies[cfg.Epoch] = cfg
		t.policyWitness.addRetired(tarpRetiredPolicy, cfg.Epoch[:], row.Value)
	}
	return t, nil
}

type tarpReceiptRecord struct {
	keyID, key, raw []byte
	retired         bool
}

func (t *tarpReceiptTable) records(ctx context.Context, family byte) iter.Seq2[tarpReceiptRecord, error] {
	return func(yield func(tarpReceiptRecord, error) bool) {
		prefix := tarpServiceKey(t.gen, family)
		for row, err := range t.snap.ScanPrefix(ctx, prefix) {
			if err != nil {
				yield(tarpReceiptRecord{}, err)
				return
			}
			record := tarpReceiptRecord{key: row.Key[len(prefix):], raw: row.Value, retired: family == tarpRetiredRow}
			record.keyID = record.key
			if record.retired {
				if len(record.key) != len(mutationreceipt.Epoch{})+len(mutationreceipt.ID{}) {
					yield(tarpReceiptRecord{}, errors.New("invalid retired receipt key"))
					return
				}
				record.keyID = record.key[len(mutationreceipt.Epoch{}):]
				if !bytes.Equal(record.key[:len(mutationreceipt.Epoch{})], record.keyID[1:17]) {
					yield(tarpReceiptRecord{}, errors.New("retired receipt epoch mismatch"))
					return
				}
			}
			if !yield(record, nil) {
				return
			}
		}
	}
}

// scan merges both physical families in canonical operation-ID order. The
// version byte is common and each ID starts with its deployment epoch, which
// is also the leading component of retired row keys.
func (t *tarpReceiptTable) scan(ctx context.Context, visit func(mutationreceipt.Epoch, mutationreceipt.Receipt) error) (map[mutationreceipt.Epoch]uint64, error) {
	w := t.policyWitness
	nextA, closeA := iter.Pull2(t.records(ctx, tarpReceiptRow))
	defer closeA()
	nextB, closeB := iter.Pull2(t.records(ctx, tarpRetiredRow))
	defer closeB()
	a, ea, hasA := nextA()
	b, eb, hasB := nextB()
	counts := make(map[mutationreceipt.Epoch]uint64, len(t.policies))
	for hasA || hasB {
		if err := errors.Join(ea, eb); err != nil {
			return nil, err
		}
		useActive := hasA && (!hasB || bytes.Compare(a.keyID, b.keyID) < 0)
		record := b
		if useActive {
			record = a
		}
		rec, err := decodeTarpReceipt(record.raw, record.keyID)
		if err != nil {
			return nil, err
		}
		epoch := receiptSnapshotReceiptEpoch(rec.ID)
		cfg, known := t.policies[epoch]
		if !known || record.retired == (epoch == t.cut.epoch) {
			return nil, errors.New("receipt outside its committed epoch")
		}
		if record.retired {
			w.addRetired(tarpRetiredRow, record.key, record.raw)
		} else {
			w.count++
			w.toggleRow(record.raw)
		}
		if rec.DeadlineMillis > t.cut.highWater {
			counts[epoch]++
			if counts[epoch] > uint64(cfg.MaxEntries) {
				return nil, mutationreceipt.ErrCapacity
			}
			if err = visit(epoch, rec); err != nil {
				return nil, err
			}
		}
		if useActive {
			a, ea, hasA = nextA()
		} else {
			b, eb, hasB = nextB()
		}
	}
	if w.count != t.cut.count || w.digest != t.cut.digest || w.retiredCount != t.cut.retiredCount || w.retiredDigest != t.cut.retiredDigest {
		return nil, errors.New("receipt ledger differs from committed witness")
	}
	return counts, ctx.Err()
}

func (t *tarpReceiptTable) capture(ctx context.Context) (mutationreceipt.Snapshot, mutationreceipt.RetiredCatalogSnapshot, error) {
	active := mutationreceipt.Snapshot{Version: 1, Epoch: t.cut.epoch, PolicyFingerprint: t.cut.policy, ClockHighWaterMillis: t.cut.highWater}
	retired := mutationreceipt.RetiredCatalogSnapshot{Version: 1, ClockHighWaterMillis: t.cut.highWater}
	members := make(map[mutationreceipt.Epoch]*mutationreceipt.RetiredEpochSnapshot)
	for _, epoch := range t.retired {
		cfg := t.policies[epoch]
		validator, err := mutationreceipt.New(cfg)
		if err != nil {
			return active, retired, err
		}
		members[epoch] = &mutationreceipt.RetiredEpochSnapshot{Policy: mutationreceipt.RetiredEpochPolicy{Epoch: epoch, Retention: cfg.Retention, MaxEntries: cfg.MaxEntries, MaxBytes: cfg.MaxBytes}, State: mutationreceipt.Snapshot{Version: 1, Epoch: epoch, PolicyFingerprint: validator.PolicyFingerprint(), ClockHighWaterMillis: t.cut.highWater}}
	}
	_, err := t.scan(ctx, func(epoch mutationreceipt.Epoch, receipt mutationreceipt.Receipt) error {
		if epoch == t.cut.epoch {
			active.Receipts = append(active.Receipts, receipt)
		} else {
			m := members[epoch]
			m.State.Receipts = append(m.State.Receipts, receipt)
		}
		return nil
	})
	if err != nil {
		return active, retired, err
	}
	for _, e := range t.retired {
		m := members[e]
		if len(m.State.Receipts) != 0 {
			retired.Epochs = append(retired.Epochs, *m)
		}
	}
	return active, retired, nil
}

func (t *tarpReceiptTable) prepareFrames(ctx context.Context, header *pb.SnapshotHeader, footer *pb.SnapshotFooter, origins []OriginState) error {
	metadata := &pb.SnapshotReceiptMetadata{ActivePolicy: receiptSnapshotPolicy(t.policies[t.cut.epoch], t.cut.policy), ClockHighWaterUnixMs: uint64(t.cut.highWater)}
	for _, o := range origins {
		metadata.OriginCutoffs = append(metadata.OriginCutoffs, &pb.OriginState{Origin: slices.Clone(o.Origin[:]), LastSeq: o.LastSeq, LastHlc: hlcToProto(o.LastHLC)})
	}
	counts, err := t.scan(ctx, func(_ mutationreceipt.Epoch, rec mutationreceipt.Receipt) error {
		_, err := t.frame(rec)
		return err
	})
	if err != nil {
		return err
	}
	footer.ActiveReceiptCount = counts[t.cut.epoch]
	footer.OriginCount = uint64(len(origins))
	for _, epoch := range t.retired {
		if counts[epoch] == 0 {
			continue
		}
		cfg := t.policies[epoch]
		validator, err := mutationreceipt.New(cfg)
		if err != nil {
			return err
		}
		metadata.RetiredPolicies = append(metadata.RetiredPolicies, receiptSnapshotPolicy(cfg, validator.PolicyFingerprint()))
		footer.RetiredReceiptCount += counts[epoch]
		footer.RetiredEpochCount++
	}
	header.ReceiptMetadata = metadata
	return ValidateReceiptSnapshotFrame(&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Header{Header: header}})
}

func (t *tarpReceiptTable) frame(rec mutationreceipt.Receipt) (*pb.SnapshotResponse, error) {
	row, err := receiptSnapshotRow(rec)
	if err != nil {
		return nil, err
	}
	f := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Receipt{Receipt: row}}
	return f, ValidateReceiptSnapshotFrame(f)
}
