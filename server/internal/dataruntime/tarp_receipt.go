package dataruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
	"google.golang.org/protobuf/proto"
)

const (
	tarpReceiptManifest byte = 4
	tarpReceiptRow      byte = 5
	tarpRetiredPolicy   byte = 6
	tarpRetiredRow      byte = 7
	tarpReceiptBaseline byte = 8
)

type tarpReceiptCut struct {
	epoch          mutationreceipt.Epoch
	policy         [32]byte
	endpoint       [16]byte
	highWater      int64
	count          uint64
	digest         [32]byte
	retiredCount   uint64
	retiredDigest  [32]byte
	baselineDigest [32]byte
}

func (c tarpReceiptCut) encode() []byte {
	out := append([]byte("LNRC1"), c.epoch[:]...)
	out = append(out, c.policy[:]...)
	out = append(out, c.endpoint[:]...)
	out = binary.BigEndian.AppendUint64(out, uint64(c.highWater))
	out = binary.BigEndian.AppendUint64(out, c.count)
	out = append(out, c.digest[:]...)
	out = binary.BigEndian.AppendUint64(out, c.retiredCount)
	out = append(out, c.retiredDigest[:]...)
	return append(out, c.baselineDigest[:]...)
}
func readTarpReceiptCut(snap *tarp.Snapshot, gen uint64) (tarpReceiptCut, error) {
	var c tarpReceiptCut
	raw, err := snap.Get(tarpServiceKey(gen, tarpReceiptManifest))
	if err != nil {
		return c, err
	}
	if len(raw) != 189 || string(raw[:5]) != "LNRC1" {
		return c, errors.New("invalid Tarp receipt manifest")
	}
	copy(c.epoch[:], raw[5:21])
	copy(c.policy[:], raw[21:53])
	copy(c.endpoint[:], raw[53:69])
	c.highWater = int64(binary.BigEndian.Uint64(raw[69:77]))
	c.count = binary.BigEndian.Uint64(raw[77:85])
	copy(c.digest[:], raw[85:117])
	c.retiredCount = binary.BigEndian.Uint64(raw[117:125])
	copy(c.retiredDigest[:], raw[125:157])
	copy(c.baselineDigest[:], raw[157:189])
	if c.epoch == (mutationreceipt.Epoch{}) || c.endpoint == ([16]byte{}) || c.highWater < 0 || c.highWater > math.MaxInt64/int64(time.Millisecond) {
		return c, errors.New("invalid Tarp receipt binding or high-water")
	}
	marker, err := snap.Get(tarpServiceKey(gen, tarpReceiptBaseline))
	if c.baselineDigest == ([32]byte{}) && errors.Is(err, tarp.ErrNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	m, err := unmarshalReceiptBaselineMarker(marker)
	if err != nil || m.Digest != c.baselineDigest || m.Epoch != c.epoch || m.PolicyFingerprint != c.policy || m.RotatedGeneration != c.endpoint || m.ReceiptHighWaterMillis > c.highWater {
		return c, errors.New("invalid Tarp receipt baseline binding")
	}
	return c, nil
}

// Each row contributes its canonical hash to the order-independent active
// ledger checksum. Updates and removals change the checksum in the same batch.
func (c *tarpReceiptCut) toggleRow(data []byte) {
	hash := sha256.Sum256(data)
	for i, v := range hash {
		c.digest[i] ^= v
	}
}

// Retired rows and policies are immutable for a generation. Their family
// and identity bind each checksum to its physical catalog membership.
func (c *tarpReceiptCut) addRetired(family byte, key, data []byte) {
	h := sha256.New()
	h.Write([]byte{family})
	h.Write(key)
	h.Write(data)
	var sum [32]byte
	h.Sum(sum[:0])
	for i := range sum {
		c.retiredDigest[i] ^= sum[i]
	}
	c.retiredCount++
}

func encodeTarpReceipt(receipt mutationreceipt.Receipt) ([]byte, error) {
	row, err := receiptSnapshotRow(receipt)
	if err != nil {
		return nil, err
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(row)
}
func decodeTarpReceipt(raw, keyID []byte) (mutationreceipt.Receipt, error) {
	row := new(pb.SnapshotReceipt)
	if err := proto.Unmarshal(raw, row); err != nil {
		return mutationreceipt.Receipt{}, err
	}
	if !bytes.Equal(row.GetOperationId(), keyID) || rejectProtoUnknownFields(row.ProtoReflect()) != nil {
		return mutationreceipt.Receipt{}, errors.New("invalid stored receipt identity")
	}
	return receiptFromSnapshotRow(row)
}

// stageReceipts persists original results independently of the bounded relay
// window. Expired follower evidence is not resurrected on replay.
func (r *tarpRelay) stageReceipts(gen uint64, snap *tarp.Snapshot, b graphstore.Writer, meta receiptWALEnvelopeMetadata) error {
	if r.receiptPolicy == nil {
		return errors.New("receipt-bearing envelope in graph-only Tarp profile")
	}
	cut, err := readTarpReceiptCut(snap, gen)
	if err != nil {
		return err
	}
	if cut.epoch != meta.epoch || cut.policy != meta.policy {
		return errors.New("Tarp receipt epoch or policy mismatch")
	}
	for _, receipt := range meta.receipts {
		if receipt.DeadlineMillis <= cut.highWater {
			continue
		}
		raw, e := encodeTarpReceipt(receipt)
		if e != nil {
			return e
		}
		key := append(tarpServiceKey(gen, tarpReceiptRow), receipt.ID[:]...)
		existing, e := snap.Get(key)
		if e == nil {
			if !bytes.Equal(existing, raw) {
				return mutationreceipt.ErrIntentConflict
			}
			continue
		}
		if !errors.Is(e, tarp.ErrNotFound) {
			return e
		}
		b.Put(key, raw)
		cut.count++
		cut.toggleRow(raw)
	}
	b.Put(tarpServiceKey(gen, tarpReceiptManifest), cut.encode())
	return nil
}

// advanceReceiptClock is the synchronous time observation boundary, including
// read-only status and rejected admission. Active IDs are ordered by issue time
// within their fixed epoch and retention policy. Physically expired rows can
// be cleared in bounded commits once the high-water is durable.
func (r *tarpRelay) advanceReceiptClock(ms int64) error {
	if ms < 0 || ms > math.MaxInt64/int64(time.Millisecond) {
		return mutationreceipt.ErrInvalidClock
	}
	for {
		more := false
		err := r.db.Update(func(gen uint64, snap *tarp.Snapshot, b *tarp.WriteBatch) error {
			cut, err := readTarpReceiptCut(snap, gen)
			if err != nil {
				return err
			}
			if ms < cut.highWater {
				return mutationreceipt.ErrInvalidClock
			}
			changed := ms > cut.highWater
			cut.highWater = ms
			lo, hi := tarp.PrefixRange(tarpServiceKey(gen, tarpReceiptRow))
			count := 0
			for row, e := range snap.ScanRange(context.Background(), lo, hi) {
				if e != nil {
					return e
				}
				rec, e := decodeTarpReceipt(row.Value, row.Key[len(lo):])
				if e != nil {
					return e
				}
				if rec.DeadlineMillis > ms {
					break
				}
				if count == 256 {
					more = true
					break
				}
				if cut.count == 0 {
					return errors.New("Tarp receipt count underflow")
				}
				b.Delete(row.Key)
				cut.count--
				cut.toggleRow(row.Value)
				changed = true
				count++
			}
			if changed {
				b.Put(tarpServiceKey(gen, tarpReceiptManifest), cut.encode())
			}
			return nil
		})
		if err != nil || !more {
			return err
		}
	}
}

// NewTarpReceiptServingRuntime binds a fresh or existing Tarp data cut to
// an explicitly configured receipt policy. Fresh and restart are distinct:
// neither may silently replace or initialize the other's state.
func NewTarpReceiptServingRuntime(graph *graphcache.GraphCache[string, *pb.Vertex], db *graphstore.DB, stage TarpGraphStager, config DurableReceiptWALRuntimeConfig, fresh bool) (*ServingRuntime, error) {
	if db == nil || db.NodeID() != config.NodeID || config.NodeID == (hlc.NodeID{}) || config.BaselineCodec == nil || config.ConfigureGraph == nil || config.StartupRestore != nil {
		return nil, errors.New("invalid Tarp receipt runtime configuration")
	}
	policy := clearReceiptClockHighWater(config.Receipt)
	validator, err := mutationreceipt.New(policy)
	if err != nil {
		return nil, err
	}
	view, err := db.Read()
	if err != nil {
		return nil, err
	}
	revision := view.Revision
	_ = view.Close()
	if (revision == 0) != fresh {
		if fresh {
			return nil, fmt.Errorf("fresh Tarp receipt directory already initialized: %w", fs.ErrExist)
		}
		return nil, fmt.Errorf("missing persisted Tarp receipt initialization: %w", fs.ErrNotExist)
	}
	rt, err := newTarpServingRuntime(graph, db, config.Log, stage, config.NamespaceFormat, &policy, validator.PolicyFingerprint())
	if err != nil {
		return nil, err
	}
	view, err = db.Read()
	if err != nil {
		return nil, errors.Join(err, rt.log.Close())
	}
	cut, active, retired, err := rt.tarp.readReceiptState(view.Snapshot, view.Generation, policy)
	_ = view.Close()
	if err != nil {
		return nil, errors.Join(err, rt.log.Close())
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	startup := config.Receipt
	if startup.ClockHighWater.Before(now) {
		startup.ClockHighWater = now
	}
	store, err := mutationreceipt.NewFromSnapshotWithClockHighWaterSink(startup, active, rt.tarp.advanceReceiptClock)
	if err != nil {
		return nil, errors.Join(err, rt.log.Close())
	}
	ms := store.Stats().HighWaterMillis
	slot, err := newRetiredReceiptCatalogSlot(policy, ms, retired)
	if err != nil {
		return nil, errors.Join(err, rt.log.Close())
	}
	if err = rt.clock.RestoreFloor(hlc.Timestamp{WallNs: ms * int64(time.Millisecond), NodeID: db.NodeID()}); err != nil {
		return nil, errors.Join(err, rt.log.Close())
	}
	rt.receipt = &receiptServingRuntime{store: store, retired: slot, policy: policy, epoch: policy.Epoch, generation: cut.endpoint, operationAdmission: newReceiptOperationAdmission(), baselineCodec: config.BaselineCodec, defaultTTL: config.DefaultTTL, configureGraph: config.ConfigureGraph}
	if cut.baselineDigest != ([32]byte{}) {
		rt.receipt.committedBaseline = receiptBaselineReference{Format: ReceiptBaselineFormatCombined, Digest: cut.baselineDigest}
	}
	return rt, nil
}

func (r *tarpRelay) readReceiptState(snap *tarp.Snapshot, gen uint64, policy mutationreceipt.Config) (tarpReceiptCut, mutationreceipt.Snapshot, mutationreceipt.RetiredCatalogSnapshot, error) {
	var active mutationreceipt.Snapshot
	var retired mutationreceipt.RetiredCatalogSnapshot
	cut, err := readTarpReceiptCut(snap, gen)
	if err != nil {
		return cut, active, retired, err
	}
	active = mutationreceipt.Snapshot{Version: 1, Epoch: cut.epoch, PolicyFingerprint: cut.policy, ClockHighWaterMillis: cut.highWater}
	retired = mutationreceipt.RetiredCatalogSnapshot{Version: 1, ClockHighWaterMillis: cut.highWater}
	var check tarpReceiptCut
	lo, hi := tarp.PrefixRange(tarpServiceKey(gen, tarpReceiptRow))
	for row, e := range snap.ScanRange(context.Background(), lo, hi) {
		if e != nil {
			return cut, active, retired, e
		}
		rec, e := decodeTarpReceipt(row.Value, row.Key[len(lo):])
		if e != nil {
			return cut, active, retired, e
		}
		if receiptSnapshotReceiptEpoch(rec.ID) != cut.epoch {
			return cut, active, retired, errors.New("stored receipt epoch mismatch")
		}
		check.count++
		check.toggleRow(row.Value)
		if rec.DeadlineMillis > cut.highWater {
			if len(active.Receipts) >= policy.MaxEntries {
				return cut, active, retired, mutationreceipt.ErrCapacity
			}
			active.Receipts = append(active.Receipts, rec)
		}
	}
	if check.count != cut.count || check.digest != cut.digest {
		return cut, active, retired, errors.New("active receipt ledger differs from committed witness")
	}
	lo, hi = tarp.PrefixRange(tarpServiceKey(gen, tarpRetiredPolicy))
	for row, e := range snap.ScanRange(context.Background(), lo, hi) {
		if e != nil {
			return cut, active, retired, e
		}
		var wire pb.ReceiptPolicy
		if e = proto.Unmarshal(row.Value, &wire); e != nil {
			return cut, active, retired, e
		}
		cfg, state, e := receiptSnapshotStoreState(&wire, uint64(cut.highWater))
		if e != nil {
			return cut, active, retired, e
		}
		if !bytes.Equal(cfg.Epoch[:], row.Key[len(lo):]) || cfg.Epoch == cut.epoch {
			return cut, active, retired, errors.New("stored retired receipt policy identity mismatch")
		}
		check.addRetired(tarpRetiredPolicy, cfg.Epoch[:], row.Value)
		member := mutationreceipt.RetiredEpochSnapshot{Policy: mutationreceipt.RetiredEpochPolicy{Epoch: cfg.Epoch, Retention: cfg.Retention, MaxEntries: cfg.MaxEntries, MaxBytes: cfg.MaxBytes}, State: state}
		prefix := append(tarpServiceKey(gen, tarpRetiredRow), cfg.Epoch[:]...)
		lower, upper := tarp.PrefixRange(prefix)
		for item, scanErr := range snap.ScanRange(context.Background(), lower, upper) {
			if scanErr != nil {
				return cut, active, retired, scanErr
			}
			rec, scanErr := decodeTarpReceipt(item.Value, item.Key[len(lower):])
			if scanErr != nil || receiptSnapshotReceiptEpoch(rec.ID) != cfg.Epoch {
				return cut, active, retired, fmt.Errorf("invalid stored retired receipt: %w", mutationreceipt.ErrInvalidSnapshot)
			}
			check.addRetired(tarpRetiredRow, item.Key[len(tarpServiceKey(gen, tarpRetiredRow)):], item.Value)
			if rec.DeadlineMillis > cut.highWater {
				if len(member.State.Receipts) >= cfg.MaxEntries {
					return cut, active, retired, mutationreceipt.ErrCapacity
				}
				member.State.Receipts = append(member.State.Receipts, rec)
			}
		}
		if len(member.State.Receipts) > 0 {
			retired.Epochs = append(retired.Epochs, member)
		}
	}
	if check.retiredCount != cut.retiredCount || check.retiredDigest != cut.retiredDigest {
		return cut, active, retired, errors.New("retired receipt catalog differs from committed witness")
	}
	return cut, active, retired, nil
}
