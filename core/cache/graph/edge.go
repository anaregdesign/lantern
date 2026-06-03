package graph

import (
	"sync"
	"time"
)

type weightValue struct {
	value      float32
	expiration time.Time
}

// weight aggregates additive contributions to an edge, each with its own
// expiration. It is safe for concurrent use; the cached sum lets readers avoid
// re-scanning the slice on every call.
type weight struct {
	mu     sync.Mutex
	values []weightValue
	sum    float32
	// lastFlushLen is len(values) immediately after the most recent
	// flushLocked. It anchors the amortized-compaction trigger in
	// addWithExpiration: a flush fires when len(values) exceeds
	// max(weightCompactMin, 2*lastFlushLen). This keeps memory within
	// ~2× the working set even on write-only hot edges that no reader
	// touches between GC ticks, while keeping the amortized add cost O(1).
	lastFlushLen int
}

// weightCompactMin is the floor under the 2× growth trigger. Below this we
// never compact — the slice header is already cheap and the linear walk would
// dominate the add. Picked to roughly match an L1-resident weightValue slice.
const weightCompactMin = 64

func newWeight() *weight {
	return &weight{}
}

func (w *weight) value() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return w.sum
}

// latestExpiration returns the furthest-future expiration among the live
// contributions, or the zero time when none remain. This is the moment after
// which the edge weight is guaranteed to be zero.
func (w *weight) latestExpiration() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	var latest time.Time
	for _, v := range w.values {
		if v.expiration.After(latest) {
			latest = v.expiration
		}
	}
	return latest
}

func (w *weight) addWithExpiration(value float32, expiration time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.values = append(w.values, weightValue{
		value:      value,
		expiration: expiration,
	})
	w.sum += value

	// Amortized compaction: a write-only hot edge that no reader visits
	// between GC ticks would otherwise grow without bound, and the eventual
	// flush (on read or on GC) would be O(N) over a giant slice. Trigger
	// compaction once the slice has doubled past its last post-flush size,
	// with a small floor so we skip the work on tiny edges. Cost stays O(1)
	// amortized; worst-case write latency variance rises but is bounded.
	if n := len(w.values); n > weightCompactMin && n > 2*w.lastFlushLen {
		w.flushLocked()
	}
}

func (w *weight) addWithTTL(value float32, ttl time.Duration) {
	w.addWithExpiration(value, time.Now().Add(ttl))
}

func (w *weight) isZero() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return w.sum == 0
}

// snapshot returns the cached sum, the furthest-future expiration among the
// live contributions, and whether any live contributions remain — all under
// a single lock acquisition and a single flush pass. Read-hot callers
// (getDetail) should prefer this over chaining isZero / value /
// latestExpiration, which triple-lock and triple-scan.
func (w *weight) snapshot() (sum float32, latest time.Time, nonZero bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	for _, v := range w.values {
		if v.expiration.After(latest) {
			latest = v.expiration
		}
	}
	return w.sum, latest, w.sum != 0
}

// flushLocked compacts expired entries in place and recomputes the cached sum.
// Caller must hold w.mu.
func (w *weight) flushLocked() {
	now := time.Now()
	write := 0
	var sum float32
	for _, v := range w.values {
		if v.expiration.After(now) {
			w.values[write] = v
			write++
			sum += v.value
		}
	}
	w.values = w.values[:write]
	w.sum = sum
	w.lastFlushLen = write
}

// edgeCache stores directed weighted edges using compact vertexID keys
// instead of the user-facing S type. Translation happens at the public API
// boundary via the shared *dictionary[S]: writes intern (and conditionally
// release on a no-op), reads look up, and the snapshot helpers resolve ids
// back to S so consumers (Neighbor, GraphCache.flush) can stay key-typed.
//
// Refcount discipline: every (tail, head) pair held in tf owns exactly one
// dict reference per endpoint. Adding a new edge interns both; deleting an
// edge releases both. Bumping the weight of an existing edge is refcount-
// neutral.
type edgeCache[S comparable] struct {
	mu         sync.RWMutex
	defaultTTL time.Duration
	dict       *dictionary[S]
	tf         map[vertexID]map[vertexID]*weight
	df         map[vertexID]int
}

func newEdgeCache[S comparable](defaultTTL time.Duration, dict *dictionary[S]) *edgeCache[S] {
	return &edgeCache[S]{
		defaultTTL: defaultTTL,
		dict:       dict,
		tf:         make(map[vertexID]map[vertexID]*weight),
		df:         make(map[vertexID]int),
	}
}

// lookupIDs resolves both endpoints under the dict's lock without bumping
// refcounts. Returns ok=false when either endpoint is unknown to the dict.
func (c *edgeCache[S]) lookupIDs(tail, head S) (vertexID, vertexID, bool) {
	if c.dict == nil {
		return 0, 0, false
	}
	tailID, ok := c.dict.lookup(tail)
	if !ok {
		return 0, 0, false
	}
	headID, ok := c.dict.lookup(head)
	if !ok {
		return 0, 0, false
	}
	return tailID, headID, true
}

func (c *edgeCache[S]) get(tail, head S) (float32, bool) {
	v, _, ok := c.getDetail(tail, head)
	return v, ok
}

// getDetail returns the current weight and the latest contribution
// expiration, so callers can surface the edge's effective deadline.
func (c *edgeCache[S]) getDetail(tail, head S) (float32, time.Time, bool) {
	tailID, headID, ok := c.lookupIDs(tail, head)
	if !ok {
		return 0, time.Time{}, false
	}

	c.mu.RLock()
	heads, ok := c.tf[tailID]
	if !ok {
		c.mu.RUnlock()
		return 0, time.Time{}, false
	}
	w, ok := heads[headID]
	c.mu.RUnlock()
	if !ok {
		return 0, time.Time{}, false
	}

	sum, latest, nonZero := w.snapshot()
	if !nonZero {
		// The edge has fully decayed. Leave physical reclamation to the
		// next GC tick (edges.flush) so the read path stays allocation
		// free and avoids spawning a goroutine per hot-path read.
		return 0, time.Time{}, false
	}
	return sum, latest, true
}

// headsOf returns the raw vertexID->*weight head map for `tail` without
// acquiring the edgeCache lock. The caller must hold the surrounding
// GraphCache.mu (R or W); under that lock, all edgeCache mutators are
// serialized so c.tf is stable for the duration of the read. Intended for
// the Neighbor read path, which only visits a small subset of tails and
// would otherwise pay the O(V+E) cost of snapshotTF on every call.
func (c *edgeCache[S]) headsOf(tail S) (map[vertexID]*weight, bool) {
	if c.dict == nil {
		return nil, false
	}
	tailID, ok := c.dict.lookup(tail)
	if !ok {
		return nil, false
	}
	heads, ok := c.tf[tailID]
	return heads, ok
}

// docFreq returns df[head] without locking the edgeCache. Same lock
// contract as headsOf: caller must hold the surrounding GraphCache.mu.
func (c *edgeCache[S]) docFreq(head vertexID) int {
	return c.df[head]
}

// resolveID resolves a vertexID back to S without locking the edgeCache.
// Same lock contract as headsOf.
func (c *edgeCache[S]) resolveID(id vertexID) (S, bool) {
	if c.dict == nil {
		var zero S
		return zero, false
	}
	return c.dict.resolve(id)
}

// snapshotTF returns a shallow copy of the tail->heads map keyed by S so
// callers can iterate without holding the edgeCache lock. The inner *weight
// values are shared and remain individually thread-safe.
//
// Resolution from vertexID back to S happens under the edgeCache RLock so
// the returned S keys are guaranteed to match the ids that were live at
// snapshot time, even if a concurrent dict release later reuses an id.
func (c *edgeCache[S]) snapshotTF() map[S]map[S]*weight {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[S]map[S]*weight, len(c.tf))
	for tailID, heads := range c.tf {
		tail, ok := c.resolve(tailID)
		if !ok {
			continue
		}
		row := make(map[S]*weight, len(heads))
		for headID, w := range heads {
			head, ok := c.resolve(headID)
			if !ok {
				continue
			}
			row[head] = w
		}
		out[tail] = row
	}
	return out
}

func (c *edgeCache[S]) snapshotDF() map[S]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[S]int, len(c.df))
	for id, n := range c.df {
		key, ok := c.resolve(id)
		if !ok {
			continue
		}
		out[key] = n
	}
	return out
}

// resolve is a tiny wrapper so the lookupless tests (no dict) degrade
// gracefully rather than nil-panicking. Production paths always have a
// dict configured by NewGraphCache.
func (c *edgeCache[S]) resolve(id vertexID) (S, bool) {
	if c.dict == nil {
		var zero S
		return zero, false
	}
	return c.dict.resolve(id)
}

func (c *edgeCache[S]) addWithExpiration(tail, head S, w float32, expiration time.Time) {
	// Fast path: existing edge. A hot counter-style edge gets updated every
	// few ms; the slow path below would acquire the dict write lock twice
	// (intern tail, intern head) and the dict release lock twice (revert
	// the bumps) plus the edgeCache write lock — five global serialization
	// points to append a float to a slice. If both endpoints are already
	// interned AND the (tail, head) bucket already exists, we can skip
	// every dict mutation and the edgeCache write lock entirely: a single
	// dict RLock to resolve both ids, a single edgeCache RLock to fetch the
	// *weight pointer, then the leaf weight.mu append. Refcounts are
	// untouched because we neither add nor remove an edge.
	//
	// Race note: a concurrent delete between RUnlock and the weight append
	// can leave us appending to a *weight that has just been detached from
	// the map. That is harmless — the weight pointer stays valid, but the
	// orphan is no longer reachable through tf, so the write effectively
	// vanishes. The user-visible semantics of "concurrent add+delete on the
	// same edge" are already racy under the slow path; the fast path
	// preserves that contract without introducing new visibility issues.
	if c.dict != nil {
		if tailID, headID, okT, okH := c.dict.lookupBoth(tail, head); okT && okH {
			c.mu.RLock()
			if heads, ok := c.tf[tailID]; ok {
				if edge, ok := heads[headID]; ok {
					c.mu.RUnlock()
					edge.addWithExpiration(w, expiration)
					return
				}
			}
			c.mu.RUnlock()
		}
	}

	// Slow path: new edge (or first writer when dict is nil in tests).
	// Intern both endpoints OUTSIDE the edgeCache lock so the dict can serve
	// readers in parallel. The intern call is the +1 we either keep (new
	// edge) or undo (existing edge) under the edgeCache lock below.
	tailID, headID := c.internEndpoints(tail, head)

	c.mu.Lock()
	defer c.mu.Unlock()

	heads, ok := c.tf[tailID]
	if !ok {
		heads = make(map[vertexID]*weight)
		c.tf[tailID] = heads
	}

	edge, existed := heads[headID]
	if !existed {
		edge = newWeight()
		heads[headID] = edge
		c.df[headID]++
	} else if c.dict != nil {
		// Edge already present: revert the intern bumps to keep the
		// "one ref per endpoint per edge" invariant. This branch is
		// now rare — the fast path above handles the common case —
		// but it remains correct for the lookupBoth-missed window
		// (e.g. tail interned between lookupBoth and intern).
		c.dict.release(tailID)
		c.dict.release(headID)
	}

	edge.addWithExpiration(w, expiration)
}

func (c *edgeCache[S]) internEndpoints(tail, head S) (vertexID, vertexID) {
	if c.dict == nil {
		return 0, 0
	}
	return c.dict.intern(tail), c.dict.intern(head)
}

func (c *edgeCache[S]) addWithTTL(tail, head S, w float32, ttl time.Duration) {
	c.addWithExpiration(tail, head, w, time.Now().Add(ttl))
}

func (c *edgeCache[S]) add(tail, head S, w float32) {
	c.addWithTTL(tail, head, w, c.defaultTTL)
}

// delete removes the (tail, head) edge. It returns true if the edge
// was present (and therefore removed by this call), false otherwise.
// Releases one dict reference per endpoint on a successful removal.
func (c *edgeCache[S]) delete(tail, head S) bool {
	tailID, headID, ok := c.lookupIDs(tail, head)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteLocked(tailID, headID)
}

// deleteLocked performs the same work as delete but assumes the caller already
// holds c.mu in write mode AND has already resolved the endpoints to ids.
// On a successful removal it releases one dict reference per endpoint.
func (c *edgeCache[S]) deleteLocked(tailID, headID vertexID) bool {
	heads, ok := c.tf[tailID]
	if !ok {
		return false
	}
	if _, ok := heads[headID]; !ok {
		return false
	}

	delete(heads, headID)
	c.df[headID]--
	if c.df[headID] <= 0 {
		delete(c.df, headID)
	}
	if len(heads) == 0 {
		delete(c.tf, tailID)
	}
	if c.dict != nil {
		c.dict.release(tailID)
		c.dict.release(headID)
	}
	return true
}

func (c *edgeCache[S]) flush() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for tailID, heads := range c.tf {
		for headID, w := range heads {
			if w.isZero() {
				c.deleteLocked(tailID, headID)
				removed++
			}
		}
	}
	return removed
}

// flushFunc is the fused single-walk sweep used by GraphCache GC. In one
// pass it removes zero-weight edges (counted in `zero`) and edges for which
// the supplied keep predicate returns false (counted in `dangling`). A nil
// keep skips the second check and behaves like flush.
//
// Caller is responsible for any cross-structure consistency: the predicate
// runs while c.mu is held write-locked, so it must not take c.mu itself.
// The typical caller (GraphCache.Watch) wraps the call in the surrounding
// GraphCache.mu.Lock() so the predicate can read sibling state (e.g. the
// vertex cache) without further locking.
func (c *edgeCache[S]) flushFunc(keep func(tail, head vertexID) bool) (zero, dangling int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for tailID, heads := range c.tf {
		for headID, w := range heads {
			switch {
			case w.isZero():
				if c.deleteLocked(tailID, headID) {
					zero++
				}
			case keep != nil && !keep(tailID, headID):
				if c.deleteLocked(tailID, headID) {
					dangling++
				}
			}
		}
	}
	return
}

// count returns the current number of (tail, head) edges held in tf.
// It acquires only an RLock so it is safe to call from metric collectors.
func (c *edgeCache[S]) count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, heads := range c.tf {
		n += len(heads)
	}
	return n
}
