package service

import (
	"sort"
	"sync"

	"github.com/anaregdesign/lantern/core/hlc"
)

// originStateTracker records the contiguous committed (last_seq, last_hlc)
// prefix for each origin NodeID. It backs PeerStatus and Snapshot cutoffs.
//
// Update sites:
//
//   - ApplyMutation calls Record after graph apply and relay-log append.
//   - logMutation calls Record after the local log append.
//   - ApplySnapshotWatermarks calls AdvanceSnapshot after completing replay;
//     a verified snapshot is the only way to skip a missing log prefix.
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

// Record advances only to the next contiguous seq. It returns false for
// duplicates and gaps, preventing a high observed seq from hiding an
// unpublished prefix. The caller must hold the service commit gate across
// graph/log work; this tracker lock only protects map access.
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
	if seq == 0 || (ok && seq != prev.seq+1) || (!ok && seq != 1) {
		return false
	}
	t.m[origin] = originRow{seq: seq, hlc: ts}
	return true
}

// AdvanceSnapshot accepts a verified snapshot's per-origin cutoff. Snapshot
// replay has already materialized every effect through this seq, so this is
// the one intentional jump over log entries absent from this replica.
func (t *originStateTracker) AdvanceSnapshot(origin hlc.NodeID, seq uint64, ts hlc.Timestamp) bool {
	var zero hlc.NodeID
	if origin == zero || seq == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= t.m[origin].seq {
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

// LocalSeq returns the contiguous committed cutoff for origin, or 0 when
// the origin has never been seen. Safe to call concurrently with Record.
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
