package dataruntime

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
)

// NewTarpGraphServingRuntime composes a graph restored from db with its
// matching origin and retained relay cut. On success the runtime owns db.
// On failure the caller retains ownership and must discard the candidate.
func NewTarpGraphServingRuntime(ctx context.Context, graph *graphcache.GraphCache[string, *pb.Vertex], db *graphstore.DB, opts mutationlog.Options, stage TarpGraphStager, namespace string) (*ServingRuntime, error) {
	r, err := newTarpServingRuntime(ctx, graph, db, opts, stage, namespace, nil, [32]byte{})
	if err != nil {
		return nil, err
	}
	if err = r.tarp.trimRelay(ctx); err != nil {
		return nil, errors.Join(err, r.log.Close())
	}
	if err = graph.CompleteStorageRecovery(ctx); err != nil {
		return nil, errors.Join(err, r.log.Close())
	}
	return r, nil
}

func newTarpServingRuntime(ctx context.Context, graph *graphcache.GraphCache[string, *pb.Vertex], db *graphstore.DB, opts mutationlog.Options, stage TarpGraphStager, namespace string, policy *mutationreceipt.Config, fingerprint [32]byte) (*ServingRuntime, error) {
	if db == nil || graph == nil || stage == nil || opts.WAL != nil || opts.Retained != nil {
		return nil, errors.New("Tarp graph runtime requires an owned graph, database and graph stager")
	}
	if err := validateDataFormat(namespace); err != nil {
		return nil, err
	}
	clock := hlc.New(db.NodeID(), hlc.Options{})
	relay := &tarpRelay{graph: graph, db: db, stage: stage, clock: clock, namespace: namespace, capacity: uint64(opts.EffectiveCapacity()), receiptPolicy: policy, fingerprint: fingerprint}
	cut, entries, origins, err := relay.resume(ctx)
	if err != nil {
		return nil, err
	}
	if err := clock.RestoreFloor(cut.floor); err != nil {
		return nil, err
	}
	opts.WAL = relay
	opts.Retained = relay
	log, err := mutationlog.ResumeRetained(opts, cut.last, entries)
	if err != nil {
		return nil, err
	}
	rt, err := newGraphRuntime(graph, log, clock, namespace)
	if err != nil {
		return nil, errors.Join(err, log.Close())
	}
	rt.origins = origins
	rt.tarp = relay
	rt.owner = &tarpGraphOwner{log, db}
	return rt, nil
}

type tarpGraphOwner struct {
	log *mutationlog.Log
	db  *graphstore.DB
}

func (o *tarpGraphOwner) Close() error { return errors.Join(o.log.Close(), o.db.Close()) }

func (r *ServingRuntime) GraphStore() *graphstore.DB {
	if r == nil || r.tarp == nil {
		return nil
	}
	return r.tarp.db
}

// TarpBackupWitness binds an independently captured application history to a
// verified physical backup at one generation and local frontier.
type TarpBackupWitness struct {
	NodeID                 hlc.NodeID
	Generation, LocalSeq   uint64
	Digest                 [32]byte
	ReceiptEpoch           mutationreceipt.Epoch
	ReceiptGeneration      [16]byte
	ReceiptHighWaterMillis int64
	ReceiptCount           uint64
	ReceiptDigest          [32]byte
	ReceiptRetiredDigest   [32]byte
	ReceiptBaselineDigest  [32]byte
	ReceiptRetiredCount    uint64
}

func (r *ServingRuntime) TarpBackupWitness(snapshot *tarp.Snapshot, gen uint64) (TarpBackupWitness, error) {
	if r.GraphStore() == nil || snapshot == nil {
		return TarpBackupWitness{}, errors.New("missing Tarp backup view")
	}
	return ReadTarpBackupWitness(r.tarp.db.NodeID(), gen, snapshot, r.namespaceFormat)
}

// ReadTarpBackupWitness checks a detached or restored store before admitting a server.
func ReadTarpBackupWitness(node hlc.NodeID, gen uint64, snapshot *tarp.Snapshot, namespace string) (TarpBackupWitness, error) {
	if node == (hlc.NodeID{}) || gen == 0 || snapshot == nil {
		return TarpBackupWitness{}, errors.New("invalid Tarp backup identity or view")
	}
	cut, err := readTarpRelayCut(snapshot, gen, namespace, node)
	if err != nil {
		return TarpBackupWitness{}, err
	}
	w := TarpBackupWitness{NodeID: node, Generation: gen, LocalSeq: cut.last, Digest: cut.digest}
	ledger, e := readTarpReceiptCut(snapshot, gen)
	if cut.receipts {
		if e != nil {
			return TarpBackupWitness{}, e
		}
		w.ReceiptEpoch = ledger.epoch
		w.ReceiptGeneration = ledger.endpoint
		w.ReceiptHighWaterMillis = ledger.highWater
		w.ReceiptCount = ledger.count
		w.ReceiptDigest = ledger.digest
		w.ReceiptRetiredCount = ledger.retiredCount
		w.ReceiptRetiredDigest = ledger.retiredDigest
		w.ReceiptBaselineDigest = ledger.baselineDigest
	} else if !errors.Is(e, tarp.ErrNotFound) {
		if e != nil {
			return TarpBackupWitness{}, e
		}
		return TarpBackupWitness{}, errors.New("unexpected receipt metadata in graph-only backup")
	}
	return w, nil
}

// resume always validates before enabling writes. A changed replay capacity
// can discard the oldest retained entries, but can never rewind the local tip.
func (r *tarpRelay) resume(ctx context.Context) (tarpRelayCut, []mutationlog.Entry, *originStateTracker, error) {
	if err := ctx.Err(); err != nil {
		return tarpRelayCut{}, nil, nil, err
	}
	view, err := r.db.Read()
	if err != nil {
		return tarpRelayCut{}, nil, nil, err
	}
	defer func() {
		if view != nil {
			_ = view.Close()
		}
	}()
	if view.Revision == 0 {
		if err = r.db.Update(func(gen uint64, snap *tarp.Snapshot, b *tarp.WriteBatch) error {
			for _, domain := range []byte{'G', 'S'} {
				prefix := binary.BigEndian.AppendUint64([]byte{domain}, gen)
				for _, e := range snap.KeysPrefix(ctx, prefix) {
					if e != nil {
						return e
					}
					return errors.New("uninitialized Tarp store contains application records")
				}
			}
			if r.receiptPolicy != nil {
				endpoint, err := newReceiptRuntimeGeneration()
				if err != nil {
					return err
				}
				ledger := tarpReceiptCut{epoch: r.receiptPolicy.Epoch, policy: r.fingerprint, endpoint: endpoint}
				b.Put(tarpServiceKey(gen, tarpReceiptManifest), ledger.encode())
			}
			b.Put(tarpServiceKey(gen, tarpRelayManifest), (tarpRelayCut{first: 1, receipts: r.receiptPolicy != nil}).encode(r.namespace))
			return ctx.Err()
		}); err != nil {
			return tarpRelayCut{}, nil, nil, err
		}
		_ = view.Close()
		view, err = r.db.Read()
		if err != nil {
			return tarpRelayCut{}, nil, nil, err
		}
	}
	cut, err := r.readCut(view.Snapshot, view.Generation)
	if err != nil {
		return cut, nil, nil, fmt.Errorf("read Tarp relay manifest: %w", err)
	}
	if cut.receipts != (r.receiptPolicy != nil) {
		return cut, nil, nil, errors.New("Tarp receipt profile differs from persisted relay")
	}
	ledger, e := readTarpReceiptCut(view.Snapshot, view.Generation)
	if r.receiptPolicy != nil {
		if e != nil {
			return cut, nil, nil, e
		}
		if ledger.epoch != r.receiptPolicy.Epoch || ledger.policy != r.fingerprint {
			return cut, nil, nil, errors.New("incompatible stored receipt profile")
		}
	} else if !errors.Is(e, tarp.ErrNotFound) {
		if e != nil {
			return cut, nil, nil, e
		}
		return cut, nil, nil, errors.New("graph-only startup cannot open a receipt store")
	}
	origins := newOriginStateTracker()
	lower, upper := tarp.PrefixRange(tarpServiceKey(view.Generation, tarpRelayOrigin))
	for item, scanErr := range view.Snapshot.ScanRange(ctx, lower, upper) {
		if scanErr != nil {
			return cut, nil, nil, scanErr
		}
		if len(item.Key) != len(lower)+len(hlc.NodeID{}) {
			return cut, nil, nil, errors.New("invalid Tarp origin key")
		}
		id := hlc.NodeID(item.Key[len(lower):])
		row, e := readTarpOrigin(item.Value, id)
		if e != nil {
			return cut, nil, nil, e
		}
		if cut.receipts && row.LastHLC.NodeID != id {
			return cut, nil, nil, errors.New("stored receipt origin HLC identity mismatch")
		}
		origins.m[id] = originRow{seq: row.LastSeq, hlc: row.LastHLC}
	}
	if uint64(len(origins.m)) != cut.origins {
		return cut, nil, nil, errors.New("Tarp origin table differs from committed cut")
	}
	lo, hi := tarp.PrefixRange(tarpServiceKey(view.Generation, tarpRelayEntry))
	pos := cut.first - 1
	var digest [32]byte
	seen := make(map[hlc.NodeID]uint64)
	var retained []mutationlog.Entry
	for item, scanErr := range view.Snapshot.ScanRange(ctx, lo, hi) {
		if scanErr != nil {
			return cut, nil, nil, scanErr
		}
		if len(item.Key) != len(lo)+8 || len(item.Value) <= 32 || pos == math.MaxUint64 || binary.BigEndian.Uint64(item.Key[len(lo):]) != pos+1 || pos+1 > cut.last {
			return cut, nil, nil, errors.New("invalid or noncontiguous Tarp relay entry")
		}
		pos++
		if pos != cut.first && !bytes.Equal(digest[:], item.Value[:32]) {
			return cut, nil, nil, errors.New("Tarp relay history chain differs")
		}
		digest = tarpEntryDigest([32]byte(item.Value[:32]), pos, item.Value[32:])
		op, e := decodeReceiptWALUnion(item.Value[32:])
		if e != nil {
			return cut, nil, nil, e
		}
		m, ok := graphMutationFromLog(op)
		if !ok || len(m.GetOrigin()) != len(hlc.NodeID{}) || m.GetNamespaceFormat() != r.namespace {
			return cut, nil, nil, errors.New("invalid restored relay mutation")
		}
		if r.namespace != "" {
			if err := validatePhysicalDataIdentities(m.ProtoReflect()); err != nil {
				return cut, nil, nil, err
			}
		}
		entry := mutationlog.Entry{Seq: pos, HLC: hlcFromProto(m.GetHlc()), Op: op}
		if e = validateReceiptWALUnionEntry(entry); e != nil {
			return cut, nil, nil, e
		}
		id := hlc.NodeID(m.GetOrigin())
		frontier, exists := origins.m[id]
		if !exists || m.GetSeq() == 0 || m.GetSeq() > frontier.seq || (seen[id] != 0 && m.GetSeq() != seen[id]+1) {
			return cut, nil, nil, errors.New("Tarp relay differs from origin frontier")
		}
		if m.GetSeq() == frontier.seq && !entry.HLC.Equal(frontier.hlc) {
			return cut, nil, nil, errors.New("Tarp relay HLC differs from origin frontier")
		}
		seen[id] = m.GetSeq()
		if cut.last-pos < r.capacity {
			entry.Op = relayOrigin{Origin: id, Seq: m.GetSeq(), Digest: digest}
			retained = append(retained, entry)
		}
	}
	if pos != cut.last || (cut.first <= cut.last && digest != cut.digest) {
		return cut, nil, nil, errors.New("Tarp relay tip differs from manifest")
	}
	for id, seq := range seen {
		if seq != origins.m[id].seq {
			return cut, nil, nil, errors.New("Tarp relay ends before origin frontier")
		}
	}
	return cut, retained, origins, ctx.Err()
}

// Trim only after the entire startup cut passes graph, relay and receipt
// validation. Failed validation never discards an existing relay suffix.
func (r *tarpRelay) trimRelay(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done := false
		err := r.db.Update(func(gen uint64, snap *tarp.Snapshot, b *tarp.WriteBatch) error {
			cut, err := r.readCut(snap, gen)
			if err != nil {
				return err
			}
			count := cut.last - (cut.first - 1)
			if count <= r.capacity {
				done = true
				return nil
			}
			drop := min(count-r.capacity, 1024)
			end := cut.first + drop
			for seq := cut.first; seq < end; seq++ {
				b.Delete(tarpEntryKey(gen, seq))
			}
			cut.first = end
			b.Put(tarpServiceKey(gen, tarpRelayManifest), cut.encode(r.namespace))
			done = count-drop <= r.capacity
			return ctx.Err()
		})
		if err != nil || done {
			return err
		}
	}
}

// The historical Append path may retry an uncertain WAL append. A Tarp write
// is immediately durable or indeterminate and may never reuse its sequence
// until the persisted cut has been reopened and reconciled.
func (s *Executor) appendGraphLog(op mutationlog.MutationOp, ts hlc.Timestamp) error {
	if s.runtime == nil || s.runtime.tarp == nil {
		_, err := s.log.Append(op, ts)
		return err
	}
	_, err := s.log.CommitWithPostRingPublication(op, ts, nil)
	if stagedPublicationRequiresRecovery(err) {
		s.markReceiptCommitFaultLocked()
	}
	return err
}
