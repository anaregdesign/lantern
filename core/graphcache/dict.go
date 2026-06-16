package graphcache

import "sync"

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

// dictionary[S] is a bidirectional, refcounted key↔id table shared by the
// vertex cache and the edge cache inside a single GraphCache. Centralising
// the table guarantees that "the vertex with key K" and "the edge endpoint
// with key K" always resolve to the same vertexID — which is what lets
// edges store endpoints as 4 B integers without losing the auto-create
// invariant.
//
// Concurrency: dictionary uses a single sync.RWMutex. Refcount mutations
// (intern / acquire / release) take the write lock; resolve / lookup take
// the read lock. The expected workload is read-dominated (every edge
// lookup resolves both endpoints), so RWMutex is the right primitive for
// the initial implementation. Sharding can be layered on later without
// changing the public API of this type if profiling shows contention.
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
func (d *dictionary[S]) intern(key S) vertexID {
	d.mu.Lock()
	defer d.mu.Unlock()

	if id, ok := d.forward[key]; ok {
		d.refcount[id]++
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
	d.mu.Lock()
	defer d.mu.Unlock()

	if int(id) >= len(d.refcount) || d.refcount[id] == 0 {
		panic("dictionary: acquire of unallocated vertexID")
	}
	d.refcount[id]++
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

// resolve returns the key for id. It returns the zero value of S and
// false if id is not currently allocated (either never minted or already
// freed).
func (d *dictionary[S]) resolve(id vertexID) (S, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if int(id) >= len(d.refcount) || d.refcount[id] == 0 {
		var zero S
		return zero, false
	}
	return d.reverse[id], true
}

// release decrements the refcount of id by one and frees the id when the
// count reaches zero. It returns true iff the id was freed by this call.
// It panics if id is not currently allocated, because over-release is a
// caller bug that silently corrupts the refcount accounting.
func (d *dictionary[S]) release(id vertexID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if int(id) >= len(d.refcount) || d.refcount[id] == 0 {
		panic("dictionary: release of unallocated vertexID")
	}
	d.refcount[id]--
	if d.refcount[id] > 0 {
		return false
	}
	key := d.reverse[id]
	delete(d.forward, key)
	var zero S
	d.reverse[id] = zero // drop the string reference so the GC can reclaim it
	d.free = append(d.free, id)
	return true
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
// graph package \u2014 callers should rely on the string fast path.
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
// caller MUST hold d.mu for writing.
func (d *dictionary[S]) allocateLocked(key S) vertexID {
	if n := len(d.free); n > 0 {
		id := d.free[n-1]
		d.free = d.free[:n-1]
		d.forward[key] = id
		d.reverse[id] = key
		d.refcount[id] = 1
		return id
	}
	id := vertexID(len(d.reverse))
	d.forward[key] = id
	d.reverse = append(d.reverse, key)
	d.refcount = append(d.refcount, 1)
	return id
}
