package service

import (
	"sort"
	"sync"

	"github.com/anaregdesign/lantern/core/hlc"
)

// originStateTracker records the (last_seq, last_hlc) the local node
// has applied for each known origin NodeID. It is the in-memory
// backing store for the PeerStatus RPC (#186) and the dedup gate for
// remote-applied mutations under the Reading B contract (#415).
//
// Update sites:
//
//   - ApplyMutation, at the TOP of the function, calls Record(origin,
//     seq, hlc) as an atomic claim. The bool return tells the caller
//     whether the watermark advanced — if it did not, the (origin, seq)
//     pair was already applied via another peer hop, so the apply is
//     skipped to avoid duplicate cache writes and a duplicate log entry.
//   - logMutation, after a successful local append, calls Record with
//     the local origin so PeerStatus always reflects the local node's
//     own progress too. The bool return is ignored: local seqs are
//     strictly monotone by construction, so the call always advances.
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

// Record advances the per-origin watermark to (seq, ts) iff seq is
// strictly greater than the previously-recorded seq for origin. It
// returns true when the watermark advanced (the caller may proceed to
// apply and log the mutation) and false when this (origin, seq) was
// already recorded (the caller MUST treat the mutation as a duplicate
// and skip it). The check-and-set is performed under a single Lock so
// concurrent ApplyMutation invocations cannot both observe a stale
// watermark and double-apply.
//
// A zero origin (all-zero NodeID) is silently dropped and returns
// false: the wire protocol forbids zero NodeIDs and accepting them
// would conflate "unset" with "node-zero" in the PeerStatus map.
func (t *originStateTracker) Record(origin hlc.NodeID, seq uint64, ts hlc.Timestamp) bool {
	var zero hlc.NodeID
	if origin == zero {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.m[origin]
	if ok && seq <= prev.seq {
		return false
	}
	t.m[origin] = originRow{seq: seq, hlc: ts}
	return true
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
