package graphcache

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// WholeStateInstall is a reversible in-place replacement of every graph,
// causal, prefix, and search-index datum. It keeps the GraphCache pointer and
// its publication/search locks stable for long-lived readers. The caller must
// defer Abort immediately and finish the stage exactly once.
type WholeStateInstall[S comparable, T any] struct {
	cache    *GraphCache[S, T]
	previous graphCacheWholeState[S, T]
	closed   bool
}

type graphCacheWholeState[S comparable, T any] struct {
	vertices *cache.Cache[S, T]
	edges    *edgeCache[S]
	dict     *dictionary[S]

	prefixIndex   *radix
	prefixExtract func(S) string
	headByTail    map[vertexID]*headIndex

	searchIndex               *search.InvertedIndex[S, search.Document]
	searchExtract             func(S, T) search.Document
	searchPreappliedEvictions map[S]int

	vertexHLC           map[S]hlc.Timestamp
	vertexHLCHighWater  int
	vertexTombstones    map[S]tombstoneEntry
	edgeTombstones      map[EdgeKey[S]]tombstoneEntry
	vertexDeadlines     causalDeadlineHeap[S]
	edgeDeadlines       indexedCausalDeadlineHeap[EdgeKey[S]]
	vertexDeadlineBytes uint64
	edgeDeadlineBytes   uint64
	oldestVertex        time.Time
	oldestEdge          time.Time

	vertexBarriers map[S]hlc.Timestamp
	edgeBarriers   map[EdgeKey[S]]hlc.Timestamp

	vertexUsage          map[S]uint64
	edgeUsage            map[EdgeKey[S]]uint64
	vertexUsageBytes     uint64
	edgeUsageBytes       uint64
	vertexUsagePeak      int
	edgeUsagePeak        int
	vertexHighWater      int
	edgeHighWater        int
	vertexBytesHighWater uint64
	edgeBytesHighWater   uint64
	vertexRejected       uint64
	edgeRejected         uint64
}

// BeginWholeStateInstall stages candidate's complete data and derived indexes
// into c. Candidate must be detached, use the same immutable capacity/index
// policy, and have staging enabled. All direct graph and search readers are
// blocked until Commit or Abort, so no pointer outside GraphCache can observe
// a partial replacement.
func (c *GraphCache[S, T]) BeginWholeStateInstall(candidate *GraphCache[S, T]) (*WholeStateInstall[S, T], error) {
	if c == nil || candidate == nil || c == candidate ||
		c.publicationGate == nil || candidate.publicationGate == nil {
		return nil, errors.New("graphcache: whole-state install requires distinct staged caches")
	}

	candidate.mu.RLock()
	candidate.searchCommitMu.RLock()
	candidateDefaultTTL := candidate.defaultTTL
	candidateCausalLimits := candidate.causalLimits
	candidateHasPrefixIndex := candidate.prefixIndex != nil
	candidateHasSearchIndex := candidate.searchIndex != nil
	next := candidate.wholeStateLocked()
	candidate.searchCommitMu.RUnlock()
	candidate.mu.RUnlock()

	c.mu.Lock()
	c.publicationGate.Lock()
	c.searchCommitMu.Lock()
	if c.defaultTTL != candidateDefaultTTL ||
		c.causalLimits != candidateCausalLimits ||
		(c.prefixIndex != nil) != candidateHasPrefixIndex ||
		(c.searchIndex != nil) != candidateHasSearchIndex {
		c.searchCommitMu.Unlock()
		c.publicationGate.Unlock()
		c.mu.Unlock()
		return nil, errors.New("graphcache: whole-state candidate policy differs")
	}
	previous := c.wholeStateLocked()
	next.vertexUsagePeak = max(next.vertexUsagePeak, previous.vertexUsagePeak)
	next.edgeUsagePeak = max(next.edgeUsagePeak, previous.edgeUsagePeak)
	next.vertexHighWater = max(next.vertexHighWater, previous.vertexHighWater)
	next.edgeHighWater = max(next.edgeHighWater, previous.edgeHighWater)
	next.vertexBytesHighWater = max(next.vertexBytesHighWater, previous.vertexBytesHighWater)
	next.edgeBytesHighWater = max(next.edgeBytesHighWater, previous.edgeBytesHighWater)
	next.vertexRejected = previous.vertexRejected
	next.edgeRejected = previous.edgeRejected
	stage := &WholeStateInstall[S, T]{cache: c, previous: previous}
	c.installWholeStateLocked(next)
	return stage, nil
}

func (c *GraphCache[S, T]) wholeStateLocked() graphCacheWholeState[S, T] {
	return graphCacheWholeState[S, T]{
		vertices: c.vertices,
		edges:    c.edges,
		dict:     c.dict,

		prefixIndex:               c.prefixIndex,
		prefixExtract:             c.prefixExtract,
		headByTail:                c.headByTail,
		searchIndex:               c.searchIndex,
		searchExtract:             c.searchExtract,
		searchPreappliedEvictions: c.searchPreappliedEvictions,

		vertexHLC:           c.vertexHLC,
		vertexHLCHighWater:  c.vertexHLCHighWater,
		vertexTombstones:    c.vertexTombstones,
		edgeTombstones:      c.edgeTombstones,
		vertexDeadlines:     c.vertexTombstoneDeadlines,
		edgeDeadlines:       c.edgeTombstoneDeadlines,
		vertexDeadlineBytes: c.vertexTombstoneDeadlineBytes,
		edgeDeadlineBytes:   c.edgeTombstoneDeadlineBytes,
		oldestVertex:        c.oldestVertexTombstoneDeadline,
		oldestEdge:          c.oldestEdgeTombstoneDeadline,

		vertexBarriers: c.vertexCausalBarriers,
		edgeBarriers:   c.edgeCausalBarriers,

		vertexUsage:          c.vertexCausalUsage,
		edgeUsage:            c.edgeCausalUsage,
		vertexUsageBytes:     c.vertexCausalUsageBytes,
		edgeUsageBytes:       c.edgeCausalUsageBytes,
		vertexUsagePeak:      c.vertexCausalUsagePeak,
		edgeUsagePeak:        c.edgeCausalUsagePeak,
		vertexHighWater:      c.vertexCausalHighWater,
		edgeHighWater:        c.edgeCausalHighWater,
		vertexBytesHighWater: c.vertexCausalBytesHighWater,
		edgeBytesHighWater:   c.edgeCausalBytesHighWater,
		vertexRejected:       c.vertexCausalRejected,
		edgeRejected:         c.edgeCausalRejected,
	}
}

func (c *GraphCache[S, T]) installWholeStateLocked(state graphCacheWholeState[S, T]) {
	c.vertices = state.vertices
	c.edges = state.edges
	c.dict = state.dict

	c.prefixIndex = state.prefixIndex
	c.prefixExtract = state.prefixExtract
	c.headByTail = state.headByTail
	c.searchIndex = state.searchIndex
	c.searchExtract = state.searchExtract
	c.searchPreappliedEvictions = state.searchPreappliedEvictions

	c.vertexHLC = state.vertexHLC
	c.vertexHLCHighWater = state.vertexHLCHighWater
	c.vertexTombstones = state.vertexTombstones
	c.edgeTombstones = state.edgeTombstones
	c.vertexTombstoneDeadlines = state.vertexDeadlines
	c.edgeTombstoneDeadlines = state.edgeDeadlines
	c.vertexTombstoneDeadlineBytes = state.vertexDeadlineBytes
	c.edgeTombstoneDeadlineBytes = state.edgeDeadlineBytes
	c.oldestVertexTombstoneDeadline = state.oldestVertex
	c.oldestEdgeTombstoneDeadline = state.oldestEdge

	c.vertexCausalBarriers = state.vertexBarriers
	c.edgeCausalBarriers = state.edgeBarriers

	c.vertexCausalUsage = state.vertexUsage
	c.edgeCausalUsage = state.edgeUsage
	c.vertexCausalUsageBytes = state.vertexUsageBytes
	c.edgeCausalUsageBytes = state.edgeUsageBytes
	c.vertexCausalUsagePeak = state.vertexUsagePeak
	c.edgeCausalUsagePeak = state.edgeUsagePeak
	c.vertexCausalHighWater = state.vertexHighWater
	c.edgeCausalHighWater = state.edgeHighWater
	c.vertexCausalBytesHighWater = state.vertexBytesHighWater
	c.edgeCausalBytesHighWater = state.edgeBytesHighWater
	c.vertexCausalRejected = state.vertexRejected
	c.edgeCausalRejected = state.edgeRejected

	// Incremental GC plans name dictionary IDs from the replaced state and
	// cannot cross a whole-state boundary.
	c.gcSweepPlan = nil
	c.gcSweepPos = 0
	c.vertices.SetOnEvictMany(c.onVerticesEvicted)
}

// Commit publishes the staged state by releasing only already-held locks.
func (s *WholeStateInstall[S, T]) Commit() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	s.cache.searchCommitMu.Unlock()
	s.cache.publicationGate.Unlock()
	s.cache.mu.Unlock()
}

// Abort restores the exact pre-install state and releases the visibility
// locks. It is safe after Commit and may be deferred as a panic guard.
func (s *WholeStateInstall[S, T]) Abort() {
	if s == nil || s.closed {
		return
	}
	s.cache.installWholeStateLocked(s.previous)
	s.closed = true
	s.cache.searchCommitMu.Unlock()
	s.cache.publicationGate.Unlock()
	s.cache.mu.Unlock()
}
