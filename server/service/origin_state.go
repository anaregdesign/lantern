package service

import (
	"sort"
	"sync"

	"github.com/anaregdesign/lantern/core/hlc"
)

// originStateTracker records the (last_seq, last_hlc) the local node
// has applied for each known origin NodeID. It is the in-memory
// backing store for the PeerStatus RPC (#186).
//
// Update sites:
//
//   - ApplyMutation, after a successful remote-origin apply, calls
//     Record(origin, seq, hlc).
//   - logMutation, after a successful local append, calls Record with
//     the local origin so PeerStatus always reflects the local node's
//     own progress too.
//
// Concurrency: a single RWMutex protects the map. Updates are O(1);
// the snapshot returned by States() is a copy so callers can iterate
// without holding the lock.
type originStateTracker struct {
	mu sync.RWMutex
	m  map[hlc.NodeID]originRow
}

type originRow struct {
	seq uint64
	hlc hlc.Timestamp
}

func newOriginStateTracker() *originStateTracker {
	return &originStateTracker{m: make(map[hlc.NodeID]originRow)}
}

// Record updates the per-origin watermark with the strictly-greatest
// (seq, hlc) ever seen. Lower seqs are ignored — replays and
// out-of-order delivery must not regress the watermark.
//
// A zero origin (all-zero NodeID) is silently dropped: the wire
// protocol forbids zero NodeIDs and accepting them would conflate
// "unset" with "node-zero" in the PeerStatus map.
func (t *originStateTracker) Record(origin hlc.NodeID, seq uint64, ts hlc.Timestamp) {
	var zero hlc.NodeID
	if origin == zero {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.m[origin]
	if ok && seq <= prev.seq {
		return
	}
	t.m[origin] = originRow{seq: seq, hlc: ts}
}

// OriginState is a snapshot row returned by States().
type OriginState struct {
	Origin  hlc.NodeID
	LastSeq uint64
	LastHLC hlc.Timestamp
}

// States returns a copy of every recorded origin in deterministic
// (Origin-bytes ascending) order. Safe to call concurrently with
// Record.
func (t *originStateTracker) States() []OriginState {
	t.mu.RLock()
	out := make([]OriginState, 0, len(t.m))
	for id, row := range t.m {
		out = append(out, OriginState{Origin: id, LastSeq: row.seq, LastHLC: row.hlc})
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Origin, out[j].Origin
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	return out
}

// LocalSeq returns the highest recorded seq for origin, or 0 when
// the origin has never been seen. Safe to call concurrently with
// Record.
func (t *originStateTracker) LocalSeq(origin hlc.NodeID) uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.m[origin].seq
}

// OriginCount returns the number of distinct origin NodeIDs currently
// recorded. Used by provider/metrics to sample
// lantern_origin_states_count. Safe to call concurrently with Record.
func (t *originStateTracker) OriginCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.m)
}
