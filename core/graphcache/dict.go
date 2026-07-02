package graphcache

import (
	"sync"
	"sync/atomic"
)

// vertexID is the internal handle used to refer to a vertex key without
// keeping a copy of its (potentially large) underlying string. It is
// minted by dictionary.intern and recycled when refcount drops to zero.
//
// vertexID is intentionally uint32: 4 GiB of distinct live vertices is
// already a far larger scale than any single Lantern node is expected to
// hold, and halving the per-edge bookkeeping cost from 8 B to 4 B is the
// whole point of introducing the indirection. If a future workload ever
// demands more, widen this single typedef.
type vertexID uint32

// freedRefcount is the tombstone value a refcount is CAS'd to at the moment
// its id is freed (#837). It closes the free/resurrect race: exactly one
// goroutine wins the 0→freedRefcount transition and performs the free, while
// a concurrent intern that resurrected the id (0→1 under d.mu.Lock) makes
// that CAS fail so the loser backs off. allocateLocked resets the slot to 1
// when the id is handed out again.
const freedRefcount = ^uint32(0)

// dictionary[S] is a bidirectional, refcounted key↔id table shared by the
// vertex cache and the edge cache inside a single GraphCache. Centralising
// the table guarantees that "the vertex with key K" and "the edge endpoint
// with key K" always resolve to the same vertexID — which is what lets
// edges store endpoints as 4 B integers without losing the auto-create
// invariant.
//
// Concurrency (#837): the RWMutex protects the STRUCTURE — the forward map,
// the reverse/refcount slice headers, and the freelist. Refcounts themselves
// are mutated with atomics, so the hot pin/release cycle used by every
// lock-free point read (GetWeight / GetEdgeDetail) and every hot-edge
// additive write runs under the READ lock and no longer serializes the
// whole process on a single exclusive mutex:
//
//   - pinBoth / acquire: RLock + CAS-increment-from-nonzero. Observing 0 or
//     freedRefcount means a concurrent release is freeing the id — the pin
//     misses and the caller falls back to its slow path.
//   - release: RLock + CAS decrement. Only the goroutine whose decrement
//     produced 0 escalates to the write lock, where CAS(0→freedRefcount)
//     decides between "really free it" and "an intern resurrected it first".
//   - intern / allocate / releaseKeys: write lock, as before (map and slice
//     structure changes). The write lock excludes all RLock holders, and
//     slice growth in allocateLocked therefore never races an atomic access.
//
// vertexID lifecycle:
//
//	intern(K)     refcount 0 -> 1, returns fresh id (or recycles freed id)
//	intern(K) again refcount n -> n+1, returns same id
//	acquire(id)    refcount n -> n+1 (panics if id is not live)
//	release(id)    refcount n -> n-1, frees the id when it hits 0
//
// "Free" means: the id is pushed onto the freelist and the forward/reverse
// table entries are cleared. The id will be handed out again by a
// subsequent intern of a new key. Callers must never use an id after the
// release call that freed it.
type dictionary[S comparable] struct {
	mu       sync.RWMutex
	forward  map[S]vertexID
	reverse  []S
	refcount []uint32
	free     []vertexID
}

// newDictionary returns an empty dictionary. Capacity hints can be added
// later if profiling shows the initial growth cost matters; the zero
// value already works.
func newDictionary[S comparable]() *dictionary[S] {
	return &dictionary[S]{
		forward: make(map[S]vertexID),
	}
}

// intern returns the vertexID for key, allocating a new one if necessary,
// and increments its refcount by one. Callers MUST pair every intern with
// exactly one release once they stop referring to the key.
//
// Holding d.mu for writing excludes every RLock-side atomic mutator, so the
// increment cannot race a concurrent pin or release. It CAN observe a
// refcount of 0: a releaser may have decremented to 0 under the read lock
// and not yet reached its write-locked free step. Bumping 0→1 here is the
// resurrection path — the releaser's CAS(0→freedRefcount) then fails and
// the id stays live under the same key.
func (d *dictionary[S]) intern(key S) vertexID {
	d.mu.Lock()
	defer d.mu.Unlock()

	if id, ok := d.forward[key]; ok {
		atomic.AddUint32(&d.refcount[id], 1)
		return id
	}
	return d.allocateLocked(key)
}

// acquire increments the refcount of an already-live id by one. It panics
// if id is not currently allocated, because that always indicates a
// caller bug: the only way to obtain an id is via intern (which gives the
// caller the first reference), and the only legitimate use for acquire is
// to add a second reference to an id the caller already holds.
func (d *dictionary[S]) acquire(id vertexID) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if int(id) >= len(d.refcount) || !pinRef(&d.refcount[id]) {
		panic("dictionary: acquire of unallocated vertexID")
	}
}

// pinRef CAS-increments *rc if and only if it currently holds a live count
// (neither 0 nor the freed tombstone). Returns false on a dying/freed slot.
// Caller must hold d.mu (read lock suffices) so the slice cannot be
// reallocated under the pointer.
func pinRef(rc *uint32) bool {
	for {
		cur := atomic.LoadUint32(rc)
		if cur == 0 || cur == freedRefcount {
			return false
		}
		if atomic.CompareAndSwapUint32(rc, cur, cur+1) {
			return true
		}
	}
}

// lookup returns the id for key without changing its refcount. Useful for
// read paths that need to translate a key to an id but do not own a
// reference (e.g. GetEdge where the caller has not interned anything).
func (d *dictionary[S]) lookup(key S) (vertexID, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	id, ok := d.forward[key]
	return id, ok
}

// lookupBoth resolves both keys to vertexIDs under a single RLock cycle.
// Returns each id independently with its own found-bit so the caller can
// distinguish "tail missing" from "head missing" without a second lookup.
// Refcounts are unchanged; the returned ids are valid only as long as no
// concurrent release frees them, which is the same contract as lookup.
//
// Edge writers use this on the hot path: if both ids already exist and
// the (tail, head) bucket is present, the write can append to the per-edge
// weight without touching the dict's write lock or the edgeCache write
// lock at all.
func (d *dictionary[S]) lookupBoth(a, b S) (idA, idB vertexID, okA, okB bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	idA, okA = d.forward[a]
	idB, okB = d.forward[b]
	return
}

// pinBoth resolves both keys to vertexIDs and increments their refcounts,
// returning a release func that drops the two references again. While the
// pin is held the ids cannot reach refcount zero, so they can neither be
// freed nor recycled to a different key — which is exactly the ABA
// guarantee a lock-free point read needs: a reader that pins (tail, head)
// before consulting the edge map cannot have its endpoint ids reused out
// from under it between resolution and the bucket lookup.
//
// Since #837 the pin itself runs under the READ lock with CAS increments,
// so concurrent point reads and hot-edge writes no longer serialize on the
// dict's write lock. A pin can now additionally miss when it observes a
// refcount mid-free (0 or the freed tombstone) — the caller treats that
// exactly like an absent key and falls back to its locked slow path.
//
// Returns ok=false (with a no-op release) when either key is absent or
// dying. release is always non-nil and safe to call exactly once; it is
// idempotent only via the caller's own discipline (call it once),
// mirroring release's contract.
//
// A self-pin (a == b) bumps the single shared id twice and the release
// drops it twice, preserving the refcount invariant.
func (d *dictionary[S]) pinBoth(a, b S) (idA, idB vertexID, release func(), ok bool) {
	d.mu.RLock()
	idA, okA := d.forward[a]
	idB, okB := d.forward[b]
	if !okA || !okB {
		d.mu.RUnlock()
		return 0, 0, func() {}, false
	}
	if !pinRef(&d.refcount[idA]) {
		d.mu.RUnlock()
		return 0, 0, func() {}, false
	}
	if !pinRef(&d.refcount[idB]) {
		// Undo the first pin OUTSIDE the read lock: release may need the
		// write lock to free the id, and RWMutex is not upgradable.
		d.mu.RUnlock()
		d.release(idA)
		return 0, 0, func() {}, false
	}
	d.mu.RUnlock()
	return idA, idB, func() {
		d.release(idA)
		d.release(idB)
	}, true
}

// resolve returns the key for id. It returns the zero value of S and
// false if id is not currently allocated (either never minted or already
// freed).
func (d *dictionary[S]) resolve(id vertexID) (S, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if int(id) >= len(d.refcount) {
		var zero S
		return zero, false
	}
	if rc := atomic.LoadUint32(&d.refcount[id]); rc == 0 || rc == freedRefcount {
		var zero S
		return zero, false
	}
	return d.reverse[id], true
}

// release decrements the refcount of id by one and frees the id when the
// count reaches zero. It returns true iff the id was freed by this call.
// It panics if id is not currently allocated, because over-release is a
// caller bug that silently corrupts the refcount accounting.
//
// The decrement itself happens under the read lock; only the goroutine
// whose decrement produced 0 escalates to the write lock, where the
// 0→freedRefcount CAS arbitrates against a concurrent intern that may have
// resurrected the id in the window between the two locks.
func (d *dictionary[S]) release(id vertexID) bool {
	d.mu.RLock()
	if int(id) >= len(d.refcount) {
		d.mu.RUnlock()
		panic("dictionary: release of unallocated vertexID")
	}
	newv := decRef(&d.refcount[id])
	d.mu.RUnlock()
	if newv != 0 {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.freeIfDeadLocked(id)
}

// decRef CAS-decrements *rc, panicking on an over-release (0 or freed slot).
// Returns the post-decrement count. Caller must hold d.mu (read suffices).
func decRef(rc *uint32) uint32 {
	for {
		cur := atomic.LoadUint32(rc)
		if cur == 0 || cur == freedRefcount {
			panic("dictionary: release of unallocated vertexID")
		}
		if atomic.CompareAndSwapUint32(rc, cur, cur-1) {
			return cur - 1
		}
	}
}

// freeIfDeadLocked finalises a release that decremented the count to 0. The
// CAS to the freed tombstone elects exactly one freeer: it fails when a
// concurrent intern resurrected the id (count is no longer 0) or when the
// other branch of a free/resurrect/free cycle already claimed it. Caller
// must hold d.mu for writing.
func (d *dictionary[S]) freeIfDeadLocked(id vertexID) bool {
	if !atomic.CompareAndSwapUint32(&d.refcount[id], 0, freedRefcount) {
		return false
	}
	key := d.reverse[id]
	delete(d.forward, key)
	var zero S
	d.reverse[id] = zero // drop the string reference so the GC can reclaim it
	d.free = append(d.free, id)
	return true
}

// releaseKeys looks up each key and drops one reference to its id, all under a
// single write-lock acquisition. Keys that are not currently interned are
// skipped — mirroring the per-key lookup-then-release, which no-ops when
// the key was never interned (e.g. an edge-only endpoint with no vertex ref).
// It is the batch sibling of lookup+release used by GraphCache's batch
// eviction path so a large delete pays one dict.mu acquisition instead of one
// lookup plus one release per key (#738).
func (d *dictionary[S]) releaseKeys(keys []S) {
	if len(keys) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, key := range keys {
		if id, ok := d.forward[key]; ok {
			if decRef(&d.refcount[id]) == 0 {
				d.freeIfDeadLocked(id)
			}
		}
	}
}

// len returns the number of currently live vertexIDs (i.e. ids with a
// non-zero refcount). It is intended for tests and metrics; it acquires
// the read lock and walks no maps.
func (d *dictionary[S]) len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.forward)
}

// findByProjection scans the live forward map for the first key K whose
// extract(K) equals projected. It exists as the slow-path inverse used
// by GraphCache.ScanByPrefix when S is not string; the typical S=string
// instantiation never reaches this path.
//
// O(N) in the live key count. Intentionally not exposed beyond the
// graph package — callers should rely on the string fast path.
func (d *dictionary[S]) findByProjection(extract func(S) string, projected string) (S, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for k := range d.forward {
		if extract(k) == projected {
			return k, true
		}
	}
	var zero S
	return zero, false
}

// allocateLocked mints a fresh id (or recycles one from the freelist),
// records the forward/reverse mapping, and sets refcount to 1. The
// caller MUST hold d.mu for writing. Recycled slots hold the freed
// tombstone; atomic stores keep the access model uniform even though the
// write lock already excludes every reader.
func (d *dictionary[S]) allocateLocked(key S) vertexID {
	if n := len(d.free); n > 0 {
		id := d.free[n-1]
		d.free = d.free[:n-1]
		d.forward[key] = id
		d.reverse[id] = key
		atomic.StoreUint32(&d.refcount[id], 1)
		return id
	}
	id := vertexID(len(d.reverse))
	d.forward[key] = id
	d.reverse = append(d.reverse, key)
	d.refcount = append(d.refcount, 1)
	return id
}
