package dataruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/tarp"
)

// TarpGraphStager queues the exact affected graph rows in a publication batch.
// When writer is non-nil, it owns the staged graph locks for this operation.
type TarpGraphStager func(context.Context, uint64, *tarp.Snapshot, graphstore.Writer, graphcache.StorageKeys[string], graphcache.StorageRowWriter[string, *pb.Vertex]) error

const (
	tarpRelayManifest byte = 1
	tarpRelayEntry    byte = 2
	tarpRelayOrigin   byte = 3
)

// The log mutex serializes Write and publication. Keep no additional copy of
// the graph, receipt outcomes, or relay payloads in this owner.
type tarpRelay struct {
	lastReference   relayOrigin // writer and ring publication share the Log mutex
	replayReadFault atomic.Pointer[error]
	graph           *graphcache.GraphCache[string, *pb.Vertex]
	receiptPolicy   *mutationreceipt.Config
	trackSearch     bool // fixed before the runtime admits requests
	diskAdmission   bool // requires every local indexed write to be rollback-capable
	diskSearch      *tarpDiskSearchReader
	searchRefresh   *tarpSearchRefresh
	fingerprint     [32]byte
	db              *graphstore.DB
	clock           *hlc.Clock
	stage           TarpGraphStager
	namespace       string
	capacity        uint64
}

type tarpRelayCut struct {
	receipts             bool
	first, last, origins uint64
	floor                hlc.Timestamp
	digest               [sha256.Size]byte
}

func tarpServiceKey(gen uint64, family byte) []byte {
	return append(graphstore.ServicePrefix(gen), family)
}

func tarpEntryKey(gen, seq uint64) []byte {
	return binary.BigEndian.AppendUint64(tarpServiceKey(gen, tarpRelayEntry), seq)
}

func tarpOriginKey(gen uint64, id hlc.NodeID) []byte {
	return append(tarpServiceKey(gen, tarpRelayOrigin), id[:]...)
}

func encodeTarpClock(raw []byte, stamp hlc.Timestamp) []byte {
	raw = binary.BigEndian.AppendUint64(raw, uint64(stamp.WallNs))
	return binary.BigEndian.AppendUint32(raw, stamp.Logical)
}

func decodeTarpClock(raw []byte, id hlc.NodeID) (hlc.Timestamp, error) {
	if len(raw) != 12 || binary.BigEndian.Uint64(raw) > math.MaxInt64 {
		return hlc.Timestamp{}, errors.New("invalid stored clock")
	}
	stamp := hlc.Timestamp{WallNs: int64(binary.BigEndian.Uint64(raw)), Logical: binary.BigEndian.Uint32(raw[8:]), NodeID: id}
	if stamp.WallNs == math.MaxInt64 && stamp.Logical == math.MaxUint32 {
		return hlc.Timestamp{}, hlc.ErrInvalidRestoreFloor
	}
	return stamp, nil
}

func (cut tarpRelayCut) encode(ns string) []byte {
	binding := sha256.Sum256([]byte(ns))
	raw := append([]byte("LNRR1"), binding[:]...)
	raw = binary.BigEndian.AppendUint64(raw, cut.first)
	raw = binary.BigEndian.AppendUint64(raw, cut.last)
	raw = binary.BigEndian.AppendUint64(raw, cut.origins)
	raw = encodeTarpClock(raw, cut.floor)
	raw = append(raw, cut.digest[:]...)
	if cut.receipts {
		return append(raw, 1)
	}
	return append(raw, 0)
}

func (r *tarpRelay) readCut(snap *tarp.Snapshot, gen uint64) (tarpRelayCut, error) {
	return readTarpRelayCut(snap, gen, r.namespace, r.db.NodeID())
}

func readTarpRelayCut(snap *tarp.Snapshot, gen uint64, namespace string, node hlc.NodeID) (tarpRelayCut, error) {
	var cut tarpRelayCut
	raw, err := snap.Get(tarpServiceKey(gen, tarpRelayManifest))
	if err != nil {
		return cut, err
	}
	binding := sha256.Sum256([]byte(namespace))
	if len(raw) != 5+32+24+12+32+1 || raw[105] > 1 || string(raw[:5]) != "LNRR1" || !bytes.Equal(raw[5:37], binding[:]) {
		return cut, errors.New("invalid Tarp relay manifest or namespace binding")
	}
	cut.first = binary.BigEndian.Uint64(raw[37:])
	cut.last = binary.BigEndian.Uint64(raw[45:])
	cut.origins = binary.BigEndian.Uint64(raw[53:])
	cut.floor, err = decodeTarpClock(raw[61:73], node)
	copy(cut.digest[:], raw[73:105])
	cut.receipts = raw[105] != 0
	if err != nil || cut.first == 0 || cut.first-1 > cut.last || (cut.last == 0 && (cut.origins != 0 || cut.floor.WallNs != 0 || cut.digest != ([32]byte{}))) {
		return cut, errors.New("invalid Tarp relay cut")
	}
	return cut, nil
}

func readTarpOrigin(raw []byte, id hlc.NodeID) (OriginState, error) {
	if len(raw) != 36 || binary.BigEndian.Uint64(raw) == 0 || id == (hlc.NodeID{}) {
		return OriginState{}, errors.New("invalid persisted origin position")
	}
	ts, err := decodeTarpClock(raw[8:20], hlc.NodeID(raw[20:]))
	if err != nil || ts.WallNs == 0 || ts.NodeID == (hlc.NodeID{}) {
		return OriginState{}, errors.New("invalid persisted origin HLC")
	}
	return OriginState{Origin: id, LastSeq: binary.BigEndian.Uint64(raw), LastHLC: ts}, nil
}

func tarpEntryDigest(previous [32]byte, seq uint64, raw []byte) [32]byte {
	h := sha256.New()
	h.Write(previous[:])
	var pos [8]byte
	binary.BigEndian.PutUint64(pos[:], seq)
	h.Write(pos[:])
	h.Write(raw)
	var result [32]byte
	h.Sum(result[:0])
	return result
}

func (r *tarpRelay) Write(entry mutationlog.Entry) error { return r.write(entry, nil) }

func (r *tarpRelay) write(entry mutationlog.Entry, staged graphcache.StorageRowWriter[string, *pb.Vertex]) error {
	abort := func(e error) error { return &mutationlog.DefiniteWALAbort{Cause: e} }
	if fault := r.replayReadFault.Load(); fault != nil {
		return *fault
	}
	record, err := encodeRelayRecord(entry.Op, sha256.Size)
	if err != nil {
		return abort(err)
	}
	raw := record[sha256.Size:]
	m, ok := graphMutationFromLog(entry.Op)
	if !ok || entry.Seq == 0 || m.GetSeq() == 0 || len(m.GetOrigin()) != len(hlc.NodeID{}) || !bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) || m.GetNamespaceFormat() != r.namespace || !hlcFromProto(m.GetHlc()).Equal(entry.HLC) {
		return abort(errors.New("invalid operation/relay identity for graph publication"))
	}
	if r.namespace != "" {
		if err := validatePhysicalDataIdentities(m.ProtoReflect()); err != nil {
			return abort(err)
		}
	}
	keys, err := tarpMutationKeys(m)
	if err != nil {
		return abort(err)
	}
	id := hlc.NodeID(m.GetOrigin())
	// Graph-only live relay applies the skew clamp. Receipt-certified
	// publication instead restores the exact accepted HLC floor. Keep the
	// same contract across process restart for each profile.
	localFloor := entry.HLC
	localFloor.NodeID = r.db.NodeID()
	if id != r.db.NodeID() && r.receiptPolicy == nil {
		localFloor = r.clock.Update(entry.HLC)
	}

	var committedDigest [32]byte
	var admissionIndex *search.DiskIndex
	var admissionView *search.DiskAdmission
	defer func() {
		if admissionView != nil {
			_ = admissionView.Close()
		}
	}()
	err = r.db.UpdateRows(context.Background(), func(gen uint64, snap *tarp.Snapshot, batch graphstore.Writer) error {
		cut, err := r.readCut(snap, gen)
		if err != nil {
			return err
		}
		if cut.last == math.MaxUint64 || entry.Seq != cut.last+1 {
			return errors.New("noncontiguous local Tarp relay position")
		}
		existing, err := snap.Get(tarpOriginKey(gen, id))
		if errors.Is(err, tarp.ErrNotFound) {
			if m.GetSeq() != 1 {
				return errors.New("new Tarp origin has a gap")
			}
			cut.origins++
		} else if err != nil {
			return err
		} else {
			previous, e := readTarpOrigin(existing, id)
			if e != nil {
				return e
			}
			if previous.LastSeq == math.MaxUint64 || m.GetSeq() != previous.LastSeq+1 {
				return errors.New("noncontiguous Tarp origin position")
			}
		}
		if meta, ok := receiptWALEnvelopeInfo(entry.Op); ok {
			if err := r.stageReceipts(gen, snap, batch, meta); err != nil {
				return err
			}
		}
		if r.trackSearch {
			publication := tarpEntryDigest(cut.digest, entry.Seq, raw)
			stageGraph := func(batch graphstore.Writer) error {
				return r.stage(context.Background(), gen, snap, batch, keys, staged)
			}
			var check func(tarpSearchCut, searchPostimages) error
			if r.diskAdmission && id == r.db.NodeID() {
				check = func(searchCut tarpSearchCut, proposal searchPostimages) error {
					if staged == nil {
						return errUnstagedDiskSearchWrite
					}
					timed, ok := staged.(interface{ StorageApplicationTime() time.Time })
					if !ok || r.diskSearch == nil {
						return search.ErrIndexIncomplete
					}
					if admissionView == nil {
						admissionIndex = r.diskSearch.base.Load()
						if admissionIndex == nil {
							return search.ErrIndexIncomplete
						}
						var err error
						admissionView, err = admissionIndex.PinAdmission(context.Background())
						if err != nil {
							return err
						}
					}
					return r.diskSearch.validateAdmission(context.Background(), admissionIndex, admissionView,
						snap, gen, searchCut, timed.StorageApplicationTime(), proposal)
				}
			}
			if err := stageTarpGraphSearchChanges(context.Background(), gen, snap, batch, cut.digest, publication, stageGraph, check); err != nil {
				return err
			}
		} else if err = r.stage(context.Background(), gen, snap, batch, keys, staged); err != nil {
			return err
		}
		if cut.floor.Less(localFloor) {
			cut.floor = localFloor
		}
		batch.Put(tarpOriginKey(gen, id), encodeTarpOrigin(m.GetSeq(), entry.HLC))
		copy(record[:sha256.Size], cut.digest[:])
		batch.Put(tarpEntryKey(gen, entry.Seq), record)
		cut.digest = tarpEntryDigest(cut.digest, entry.Seq, raw)
		committedDigest = cut.digest
		cut.last = entry.Seq
		if cut.last-cut.first+1 > r.capacity {
			// Normal writes drop one row. A lowered runtime limit was already
			// applied in bounded chunks at startup.
			batch.Delete(tarpEntryKey(gen, cut.first))
			cut.first++
		}
		batch.Put(tarpServiceKey(gen, tarpRelayManifest), cut.encode(r.namespace))
		return nil
	})
	if err == nil {
		r.lastReference = relayOrigin{Origin: id, Seq: m.GetSeq(), Digest: committedDigest}
		r.graph.StoragePublished(keys)
		return nil
	}
	// An old graph-first caller cannot roll back its in-memory effects. Fence
	// that path even though the Tarp batch itself definitely did not commit.
	if errors.Is(err, errUnstagedDiskSearchWrite) {
		return errUnstagedDiskSearchWrite
	}
	// A callback failure never published, but a storage read fault or an
	// unhealthy/uncertain DB still requires recovery by the serving runtime.
	var opErr *tarp.OpError
	if errors.As(err, &opErr) {
		if opErr.Outcome == tarp.NotApplied && !errors.Is(err, tarp.ErrUnhealthy) {
			return abort(err)
		}
		return err
	}
	if errors.Is(err, tarp.ErrTooLarge) {
		return abort(err)
	}
	return err
}

func encodeTarpOrigin(seq uint64, ts hlc.Timestamp) []byte {
	value := binary.BigEndian.AppendUint64(nil, seq)
	value = encodeTarpClock(value, ts)
	return append(value, ts.NodeID[:]...)
}
