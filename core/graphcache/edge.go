package graphcache

import (
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
)

type weightValue struct {
	value      float32
	expiration time.Time
	// contribID identifies the originating contribution for replicated
	// (idempotent) writes. The zero value means "no identity" and disables
	// dedup; local non-replicated writes always use the zero value so two
	// distinct local Add calls remain distinct.
	contribID ContribID
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
	// lastHLC is the most recent HLC timestamp accepted by a Put-style
	// (LWW) write. Zero value means "no LWW write has happened yet" — in
	// that case the next Put-with-HLC always wins. Used only by the
	// replication apply path; local writers do not touch it.
	lastHLC hlc.Timestamp
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
	w.addWithExpirationContrib(value, expiration, ContribID{})
}

// addWithExpirationContrib is the dedup-aware sibling of addWithExpiration.
// When contribID is non-zero and a live (unexpired) entry with the same
// contribID is already present, the call is a no-op and returns false —
// this is the idempotency guarantee the replication apply path (#182)
// needs when a peer re-delivers a mutation. A zero contribID disables
// dedup and behaves exactly like addWithExpiration; this is the local
// (non-replicated) write path.
func (w *weight) addWithExpirationContrib(value float32, expiration time.Time, contribID ContribID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !contribID.IsZero() {
		now := time.Now()
		for _, v := range w.values {
			if v.contribID == contribID && cache.IsLiveAt(v.expiration, now) {
				return false
			}
		}
	}
	w.values = append(w.values, weightValue{
		value:      value,
		expiration: expiration,
		contribID:  contribID,
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
	return true
}

func (w *weight) addWithTTL(value float32, ttl time.Duration) {
	w.addWithExpiration(value, time.Now().Add(ttl))
}

// putWithExpirationHLC atomically replaces the contributions with a single
// (value, expiration) contribution if ts is at or after the most recent
// HLC accepted by this method. Returns applied=true when the write went
// through. Used by the replication apply path (#182) to enforce Put-style
// last-writer-wins semantics across origins; the cached sum and lastHLC
// move together so subsequent comparisons are consistent.
//
// A zero ts is treated as "no causality" and is always applied (the local
// PutEdge path goes through addWithExpiration / direct mutation instead;
// this helper is intentionally narrow to the replicated-Put case).
func (w *weight) putWithExpirationHLC(value float32, expiration time.Time, ts hlc.Timestamp) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ts.Less(w.lastHLC) {
		return false
	}
	w.values = w.values[:0]
	w.values = append(w.values, weightValue{value: value, expiration: expiration})
	w.sum = value
	w.lastFlushLen = 1
	w.lastHLC = ts
	return true
}

func (w *weight) isZero() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return w.sum == 0
}

// replace atomically swaps all contributions for a single (value, expiration)
// contribution under the weight's own lock. A concurrent lock-free reader
// (snapshot) therefore observes either the pre-replace or the post-replace
// value and never an empty bucket. This is what lets the local PutEdge path
// replace an existing edge in place instead of delete+add, preserving the
// atomic-replace contract (no transient NotFound) for point reads that no
// longer hold GraphCache.mu (#740).
//
// lastHLC is reset to the zero value so the resulting state is identical to
// the fresh newWeight()+single-add the old delete+add path produced; the
// replicated LWW path uses putWithExpirationHLC, not this helper.
func (w *weight) replace(value float32, expiration time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.values = w.values[:0]
	w.values = append(w.values, weightValue{value: value, expiration: expiration})
	w.sum = value
	w.lastFlushLen = 1
	w.lastHLC = hlc.Timestamp{}
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

// snapshotEntry returns a copy of the bucket's live contributions, the
// recorded lastHLC, and whether any live contributions remain — all under
// a single lock. Used by the replication snapshot path (#184) to preserve
// per-contribution decomposition (so the receiver's ContribID dedup stays
// effective when the live tail re-delivers the same writes).
//
// `now` is passed in so an entire snapshot pass observes a single
// reference time and edges decide expiration consistently.
func (w *weight) snapshotEntry(now time.Time) ([]SnapshotContribution, hlc.Timestamp, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]SnapshotContribution, 0, len(w.values))
	for _, v := range w.values {
		if !cache.IsLiveAt(v.expiration, now) {
			continue
		}
		out = append(out, SnapshotContribution{
			Weight:     v.value,
			Expiration: v.expiration,
			ContribID:  v.contribID,
		})
	}
	if len(out) == 0 {
		return nil, w.lastHLC, false
	}
	return out, w.lastHLC, true
}

// flushLocked compacts expired entries in place and recomputes the cached sum.
// Caller must hold w.mu.
func (w *weight) flushLocked() {
	now := time.Now()
	write := 0
	var sum float32
	for _, v := range w.values {
		if cache.IsLiveAt(v.expiration, now) {
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
//
// getDetail is a point read that does NOT require the caller to hold
// GraphCache.mu: it pins both endpoint ids (pinBoth) so a concurrent
// delete/recreate cannot recycle an endpoint id to a different key between
// resolution and the bucket lookup (the ABA hazard), and it reads the edge
// map under the edgeCache's own RLock. The returned *weight pointer stays
// valid even if a concurrent delete detaches it from c.tf — snapshot() takes
// the weight's own mutex — so the read observes a consistent pre- or
// post-delete value, matching the existing racy point-read contract.
func (c *edgeCache[S]) getDetail(tail, head S) (float32, time.Time, bool) {
	if c.dict == nil {
		return 0, time.Time{}, false
	}
	tailID, headID, release, ok := c.dict.pinBoth(tail, head)
	if !ok {
		return 0, time.Time{}, false
	}
	defer release()

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

// corpusStats returns the number of tails (documents) and the total number
// of (tail, head) edges held in tf, WITHOUT locking the edgeCache. Same lock
// contract as headsOf: the caller must hold the surrounding GraphCache.mu.
// The BM25 edge weighting derives its corpus terms from these once per
// traversal: N = tails and AvgLen = edges / tails (mean out-degree). Unlike
// count() it takes no edgeCache lock, so it composes inside the Neighbor read
// path which already holds GraphCache.mu read-locked.
func (c *edgeCache[S]) corpusStats() (tails, edges int) {
	tails = len(c.tf)
	for _, heads := range c.tf {
		edges += len(heads)
	}
	return tails, edges
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

// rangeBuckets invokes fn for every (tail, head, *weight) live in the
// cache, resolving both endpoints back to S under the edgeCache RLock.
// fn returns false to stop iteration early. Used by the replication
// snapshot path (#184); the per-bucket *weight pointer is safe to read
// after the iteration returns because GraphCache.SnapshotEdges holds
// c.GraphCache.mu for the whole snapshot pass and no concurrent mutator
// can drop the bucket out from under it.
func (c *edgeCache[S]) rangeBuckets(fn func(tail, head S, w *weight) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for tailID, heads := range c.tf {
		tail, ok := c.resolve(tailID)
		if !ok {
			continue
		}
		for headID, w := range heads {
			head, ok := c.resolve(headID)
			if !ok {
				continue
			}
			if !fn(tail, head, w) {
				return
			}
		}
	}
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

// addWithExpiration appends a (value, expiration) contribution to the
// (tail, head) edge. It returns:
//
//   - created: true if this call created a new (tail, head) bucket (i.e.
//     this is the first contribution for the edge), false when the bucket
//     already existed and this call merely appended to its weight.
//   - tailID, headID: the dict-resolved endpoint IDs, valid whenever a
//     dict is configured. Callers use these IDs to maintain side indexes
//     (e.g. the per-tail head radix) without re-entering the dict.
//
// Fast path: existing edge. A hot counter-style edge gets updated every
// few ms; the slow path below would acquire the dict write lock twice
// (intern tail, intern head) and the dict release lock twice (revert
// the bumps) plus the edgeCache write lock — five global serialization
// points to append a float to a slice. If both endpoints are already
// interned AND the (tail, head) bucket already exists, we can skip
// every dict mutation and the edgeCache write lock entirely.
//
// Race note: a concurrent delete between RUnlock and the weight append
// can leave us appending to a *weight that has just been detached from
// the map. That is harmless — the weight pointer stays valid, but the
// orphan is no longer reachable through tf, so the write effectively
// vanishes. The user-visible semantics of "concurrent add+delete on the
// same edge" are already racy under the slow path; the fast path
// preserves that contract without introducing new visibility issues.
func (c *edgeCache[S]) addWithExpiration(tail, head S, w float32, expiration time.Time) (created bool, tailID, headID vertexID) {
	created, tailID, headID, _ = c.addWithExpirationContrib(tail, head, w, expiration, ContribID{})
	return
}

// addWithExpirationContrib is the dedup-aware sibling used by the
// replication apply path. When contribID is non-zero and the same
// contribution has already been recorded on the (tail, head) edge, the
// call is an exact no-op (no intern, no append, no bucket creation) and
// returns applied=false. A zero contribID falls through to the legacy
// additive semantics.
//
// The dict-refcount and bucket-creation accounting mirror addWithExpiration
// so callers can drive both helpers through onEdgeAddedLocked without
// branching on the dedup outcome — applied=false simply means "skip
// downstream side indexes too".
func (c *edgeCache[S]) addWithExpirationContrib(tail, head S, w float32, expiration time.Time, contribID ContribID) (created bool, tailID, headID vertexID, applied bool) {
	if c.dict != nil {
		if tID, hID, okT, okH := c.dict.lookupBoth(tail, head); okT && okH {
			c.mu.RLock()
			if heads, ok := c.tf[tID]; ok {
				if edge, ok := heads[hID]; ok {
					c.mu.RUnlock()
					applied = edge.addWithExpirationContrib(w, expiration, contribID)
					return false, tID, hID, applied
				}
			}
			c.mu.RUnlock()
		}
	}

	// Slow path: new edge (or first writer when dict is nil in tests).
	// Intern both endpoints OUTSIDE the edgeCache lock so the dict can serve
	// readers in parallel. The intern call is the +1 we either keep (new
	// edge) or undo (existing edge) under the edgeCache lock below.
	tailID, headID = c.internEndpoints(tail, head)

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
		created = true
	} else if c.dict != nil {
		// Edge already present: revert the intern bumps to keep the
		// "one ref per endpoint per edge" invariant. This branch is
		// now rare — the fast path above handles the common case —
		// but it remains correct for the lookupBoth-missed window
		// (e.g. tail interned between lookupBoth and intern).
		c.dict.release(tailID)
		c.dict.release(headID)
	}

	applied = edge.addWithExpirationContrib(w, expiration, contribID)
	return created, tailID, headID, applied
}

func (c *edgeCache[S]) internEndpoints(tail, head S) (vertexID, vertexID) {
	if c.dict == nil {
		return 0, 0
	}
	return c.dict.intern(tail), c.dict.intern(head)
}

// putWithExpiration atomically replaces the (tail, head) edge weight with a
// single contribution. When the bucket already exists the replacement happens
// in place under the weight's own lock (edge.replace), so a lock-free reader
// never observes a transient missing edge — this is the local PutEdge
// atomic-replace contract, previously enforced by holding GraphCache.mu across
// a delete+add. When the bucket is absent it is created exactly like
// addWithExpiration. Returns created plus the endpoint ids so the caller can
// maintain side indexes without re-entering the dict.
func (c *edgeCache[S]) putWithExpiration(tail, head S, w float32, expiration time.Time) (created bool, tailID, headID vertexID) {
	if c.dict != nil {
		if tID, hID, okT, okH := c.dict.lookupBoth(tail, head); okT && okH {
			c.mu.RLock()
			if heads, ok := c.tf[tID]; ok {
				if edge, ok := heads[hID]; ok {
					c.mu.RUnlock()
					edge.replace(w, expiration)
					return false, tID, hID
				}
			}
			c.mu.RUnlock()
		}
	}

	tailID, headID = c.internEndpoints(tail, head)

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
		created = true
	} else if c.dict != nil {
		c.dict.release(tailID)
		c.dict.release(headID)
	}
	edge.replace(w, expiration)
	return created, tailID, headID
}

// addExistingContribByID appends to the weight of an already-present
// (tailID, headID) edge identified by pre-resolved ids, without interning,
// creating buckets, or touching the dict. It returns ok=false when the
// bucket is absent (the caller must then fall back to the slow path), and
// applied to report whether the contribution changed the weight (mirrors
// addWithExpirationContrib's dedup result).
//
// The caller MUST have pinned both ids (see dictionary.pinBoth) for the
// duration of this call: addExistingContribByID does NOT hold GraphCache.mu,
// so the pin is what prevents a concurrent DeleteEdge + vertex flush from
// freeing and recycling an endpoint id mid-append. Structural reads of
// c.tf are serialized against bucket create/delete by c.mu (the edgeCache
// RWMutex); the subsequent weight append is serialized by the per-edge
// weight lock. If the bucket is deleted between the RUnlock here and the
// append, the append lands on a now-orphaned *weight and is harmlessly
// discarded — the same benign race documented for addWithExpirationContrib.
func (c *edgeCache[S]) addExistingContribByID(tailID, headID vertexID, w float32, expiration time.Time, contribID ContribID) (applied, ok bool) {
	c.mu.RLock()
	heads, hok := c.tf[tailID]
	if !hok {
		c.mu.RUnlock()
		return false, false
	}
	edge, eok := heads[headID]
	c.mu.RUnlock()
	if !eok {
		return false, false
	}
	return edge.addWithExpirationContrib(w, expiration, contribID), true
}

// putWithExpirationHLC is the LWW-aware sibling of addWithExpiration used
// by the replication apply path (#182) for replicated Put-style writes.
// It creates the (tail, head) bucket if absent (returning created=true)
// and then asks the underlying weight to apply the contribution under
// last-writer-wins semantics. applied=false means the stored last HLC was
// strictly newer than ts and the write was skipped — callers must still
// honour the bucket-creation return so dict refcounts and side indexes
// stay consistent.
func (c *edgeCache[S]) putWithExpirationHLC(tail, head S, w float32, expiration time.Time, ts hlc.Timestamp) (created bool, tailID, headID vertexID, applied bool) {
	if c.dict != nil {
		if tID, hID, okT, okH := c.dict.lookupBoth(tail, head); okT && okH {
			c.mu.RLock()
			if heads, ok := c.tf[tID]; ok {
				if edge, ok := heads[hID]; ok {
					c.mu.RUnlock()
					applied = edge.putWithExpirationHLC(w, expiration, ts)
					return false, tID, hID, applied
				}
			}
			c.mu.RUnlock()
		}
	}

	tailID, headID = c.internEndpoints(tail, head)

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
		created = true
	} else if c.dict != nil {
		c.dict.release(tailID)
		c.dict.release(headID)
	}
	applied = edge.putWithExpirationHLC(w, expiration, ts)
	return created, tailID, headID, applied
}

func (c *edgeCache[S]) addWithTTL(tail, head S, w float32, ttl time.Duration) {
	c.addWithExpiration(tail, head, w, time.Now().Add(ttl))
}

func (c *edgeCache[S]) add(tail, head S, w float32) {
	c.addWithTTL(tail, head, w, c.defaultTTL)
}

// delete removes the (tail, head) edge. It returns deleted=true together
// with the resolved tail/head vertexIDs whenever the edge was present (so
// callers can maintain side indexes keyed by ID without re-entering the
// dict). On a successful removal it releases one dict reference per
// endpoint.
func (c *edgeCache[S]) delete(tail, head S) (deleted bool, tailID, headID vertexID) {
	tID, hID, ok := c.lookupIDs(tail, head)
	if !ok {
		return false, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deleteLocked(tID, hID) {
		return true, tID, hID
	}
	return false, tID, hID
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
// onDelete, if non-nil, fires once per successful removal with the
// (tailID, headID) of the edge just deleted. It runs while c.mu is held
// write-locked alongside the keep predicate, so it must not take c.mu
// itself. It exists so callers can maintain side indexes (e.g. the
// per-tail head radix) under the same critical section as the underlying
// delete, preserving the atomicity contract observed by ScanEdgesByPrefix.
//
// Caller is responsible for any cross-structure consistency: the predicate
// runs while c.mu is held write-locked, so it must not take c.mu itself.
// The typical caller (GraphCache.Watch) wraps the call in the surrounding
// GraphCache.mu.Lock() so the predicate can read sibling state (e.g. the
// vertex cache) without further locking.
func (c *edgeCache[S]) flushFunc(keep func(tail, head vertexID) bool, onDelete func(tail, head vertexID)) (zero, dangling int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for tailID, heads := range c.tf {
		z, d := c.sweepHeadsLocked(tailID, heads, keep, onDelete)
		zero += z
		dangling += d
	}
	return
}

// tailIDs snapshots the current set of tail vertexIDs present in tf. It is the
// plan source for the incremental GC sweep (#744): GraphCache walks the
// returned IDs in bounded batches across ticks. Stale IDs (tails deleted before
// they are visited) are tolerated by flushTails, and tails created after the
// snapshot are swept in the next cycle.
func (c *edgeCache[S]) tailIDs() []vertexID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]vertexID, 0, len(c.tf))
	for tailID := range c.tf {
		ids = append(ids, tailID)
	}
	return ids
}

// flushTails is the bounded counterpart to flushFunc (#744): it applies the
// same zero-weight + keep-predicate sweep, but only to the supplied tail
// buckets. Tails absent from tf (deleted since the plan was built) are skipped.
// Counts and onDelete semantics match flushFunc exactly.
func (c *edgeCache[S]) flushTails(tailIDs []vertexID, keep func(tail, head vertexID) bool, onDelete func(tail, head vertexID)) (zero, dangling int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, tailID := range tailIDs {
		heads, ok := c.tf[tailID]
		if !ok {
			continue
		}
		z, d := c.sweepHeadsLocked(tailID, heads, keep, onDelete)
		zero += z
		dangling += d
	}
	return
}

// sweepHeadsLocked removes zero-weight edges (counted in `zero`) and edges for
// which keep returns false (counted in `dangling`) from one tail's head bucket.
// onDelete, if non-nil, fires once per removal before the underlying delete.
// Caller must hold c.mu write-locked. Shared by flushFunc (full walk) and
// flushTails (bounded incremental walk).
func (c *edgeCache[S]) sweepHeadsLocked(tailID vertexID, heads map[vertexID]*weight, keep func(tail, head vertexID) bool, onDelete func(tail, head vertexID)) (zero, dangling int) {
	for headID, w := range heads {
		switch {
		case w.isZero():
			if onDelete != nil {
				onDelete(tailID, headID)
			}
			if c.deleteLocked(tailID, headID) {
				zero++
			}
		case keep != nil && !keep(tailID, headID):
			if onDelete != nil {
				onDelete(tailID, headID)
			}
			if c.deleteLocked(tailID, headID) {
				dangling++
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
