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
//   - local publication calls Record after the local log append.
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
	if !nextContiguousOriginSeq(seq, prev, ok) {
		return false
	}
	t.m[origin] = originRow{seq: seq, hlc: ts}
	return true
}

func nextContiguousOriginSeq(seq uint64, prev originRow, exists bool) bool {
	return seq != 0 && ((!exists && seq == 1) || (exists && seq == prev.seq+1))
}

// originRowStage is a one-row, single-owner pre-WAL stage. It retains the
// tracker write lock from stageNext through Commit or Abort, keeping States,
// LocalSeq, and concurrent updates from observing the tentative row. The
// caller must also hold its service-wide publication gate for graph/log and
// Snapshot readers; this tracker lock alone is not a cross-component commit.
// A stage must be finished exactly once on the owning goroutine. Abort may be
// deferred as a panic guard because it is a no-op after Commit.
type originRowStage struct {
	tracker  *originStateTracker
	origin   hlc.NodeID
	previous originRow
	existed  bool
	finished bool
}

// stageNext tentatively installs only the next contiguous local or remote
// origin row. All map growth and validation occur before the caller's WAL
// write. A rejected zero origin, duplicate, gap, or zero seq leaves the
// tracker unlocked and unchanged. Snapshot jumps remain AdvanceSnapshot's
// responsibility and cannot be staged here.
func (t *originStateTracker) stageNext(origin hlc.NodeID, seq uint64, ts hlc.Timestamp) (*originRowStage, bool) {
	var zero hlc.NodeID
	if origin == zero {
		return nil, false
	}
	t.mu.Lock()
	release := true
	defer func() {
		if release {
			t.mu.Unlock()
		}
	}()
	prev, exists := t.m[origin]
	if !nextContiguousOriginSeq(seq, prev, exists) {
		return nil, false
	}
	stage := &originRowStage{tracker: t, origin: origin, previous: prev, existed: exists}
	t.m[origin] = originRow{seq: seq, hlc: ts}
	release = false
	return stage, true
}

// Commit makes the already installed row visible by releasing the tracker
// lock. It performs no map mutation or allocation after a successful WAL
// write. The caller's service-wide publication gate must remain held until
// its graph, receipt, and log state are also fully published.
func (s *originRowStage) Commit() {
	if s == nil || s.finished {
		return
	}
	s.finished = true
	s.tracker.mu.Unlock()
}

// Abort restores the exact previous row or absence before releasing the
// tracker lock. It is idempotent so callers can defer it across WAL panics.
func (s *originRowStage) Abort() {
	if s == nil || s.finished {
		return
	}
	if s.existed {
		s.tracker.m[s.origin] = s.previous
	} else {
		delete(s.tracker.m, s.origin)
	}
	s.finished = true
	s.tracker.mu.Unlock()
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
