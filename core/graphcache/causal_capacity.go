package graphcache

import (
	"container/heap"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

type causalDeadlineEntry[K comparable] struct {
	key      K
	deadline time.Time
}

type causalDeadlineHeap[K comparable] []causalDeadlineEntry[K]

func (h causalDeadlineHeap[K]) Len() int           { return len(h) }
func (h causalDeadlineHeap[K]) Less(i, j int) bool { return h[i].deadline.Before(h[j].deadline) }
func (h causalDeadlineHeap[K]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *causalDeadlineHeap[K]) Push(value any)    { *h = append(*h, value.(causalDeadlineEntry[K])) }
func (h *causalDeadlineHeap[K]) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	var zero causalDeadlineEntry[K]
	old[last] = zero
	if last == 0 {
		*h = nil
	} else {
		*h = old[:last]
	}
	return value
}

// CausalMetadataLimits bounds locally-originated HA causal identities by
// kind. Zero means unlimited; SetCausalMetadataLimits normalizes negative
// values to zero for defensive direct-core callers. A causal identity consumes
// one slot while its floor is represented by a live HLC watermark, a Put causal
// barrier, or a Delete tombstone; transitions between those representations do
// not consume another slot. Replication apply is intentionally exempt so a
// node never rejects state that another replica already committed.
type CausalMetadataLimits struct {
	MaxVertexEntries int
	MaxEdgeEntries   int
}

// CausalMetadataStats is one lock-consistent observability snapshot of the HA
// causal identity budget. Estimated bytes are a stable logical estimate, not
// a Go heap measurement. Oldest*RetentionDeadline is zero when the kind has no
// retained Delete tombstone; live HLC floors and Put barriers have no deadline.
type CausalMetadataStats struct {
	MaxVertexEntries              int
	MaxEdgeEntries                int
	VertexEntries                 int
	EdgeEntries                   int
	VertexEstimatedBytes          uint64
	EdgeEstimatedBytes            uint64
	VertexEntriesHighWater        int
	EdgeEntriesHighWater          int
	VertexEstimatedBytesHighWater uint64
	EdgeEstimatedBytesHighWater   uint64
	VertexRejected                uint64
	EdgeRejected                  uint64
	VertexOverLimit               bool
	EdgeOverLimit                 bool
	OldestVertexRetentionDeadline time.Time
	OldestEdgeRetentionDeadline   time.Time
}

// CausalMetadataCapacityError reports an atomic local-write rejection. Kind is
// "vertex" or "edge"; Current excludes Requested because the mutation has not
// modified graph or causal state.
type CausalMetadataCapacityError struct {
	Kind      string
	Current   int
	Requested int
	Limit     int
}

func (e *CausalMetadataCapacityError) Error() string {
	knob := "LANTERN_MAX_VERTEX_CAUSAL_ENTRIES"
	if e.Kind == "edge" {
		knob = "LANTERN_MAX_EDGE_CAUSAL_ENTRIES"
	}
	return fmt.Sprintf("%s causal metadata capacity: %d retained + %d new would exceed %s=%d", e.Kind, e.Current, e.Requested, knob, e.Limit)
}

// The estimates deliberately include the authoritative causal record, budget
// ledger entry, and lazy deadline-index entries. String payload bytes are
// added separately. These constants are versioned observability units rather
// than allocator-specific claims, so dashboards remain comparable across Go
// releases.
const (
	causalVertexIdentityBaseBytes      uint64 = 96
	causalEdgeIdentityBaseBytes        uint64 = 160
	causalVertexDeadlineEntryBaseBytes uint64 = 48
	causalEdgeDeadlineEntryBaseBytes   uint64 = 64
	causalUsageShrinkFloor                    = 1024
	causalUsageShrinkDivisor                  = 4
)

// SetCausalMetadataLimits replaces the local-origin admission limits. It is
// safe to lower a limit below current replicated usage: existing/converged
// state remains intact and only a later local mutation needing a new identity
// is rejected. The call does not retroactively evict causal floors.
func (c *GraphCache[S, T]) SetCausalMetadataLimits(limits CausalMetadataLimits) {
	if limits.MaxVertexEntries < 0 {
		limits.MaxVertexEntries = 0
	}
	if limits.MaxEdgeEntries < 0 {
		limits.MaxEdgeEntries = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.causalLimits = limits
}

// CausalMetadataStats returns current usage, stable byte estimates, all-time
// high-water values, cumulative local rejects, and the oldest retained Delete-
// tombstone deadline. Deadline minima are maintained by lazy heaps, so the
// lock-consistent snapshot itself is O(1).
func (c *GraphCache[S, T]) CausalMetadataStats() CausalMetadataStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stats := CausalMetadataStats{
		MaxVertexEntries:              c.causalLimits.MaxVertexEntries,
		MaxEdgeEntries:                c.causalLimits.MaxEdgeEntries,
		VertexEntries:                 len(c.vertexCausalUsage),
		EdgeEntries:                   len(c.edgeCausalUsage),
		VertexEstimatedBytes:          c.vertexCausalEstimatedBytesLocked(),
		EdgeEstimatedBytes:            c.edgeCausalEstimatedBytesLocked(),
		VertexEntriesHighWater:        c.vertexCausalHighWater,
		EdgeEntriesHighWater:          c.edgeCausalHighWater,
		VertexEstimatedBytesHighWater: c.vertexCausalBytesHighWater,
		EdgeEstimatedBytesHighWater:   c.edgeCausalBytesHighWater,
		VertexRejected:                c.vertexCausalRejected,
		EdgeRejected:                  c.edgeCausalRejected,
	}
	stats.VertexOverLimit = stats.MaxVertexEntries > 0 && stats.VertexEntries > stats.MaxVertexEntries
	stats.EdgeOverLimit = stats.MaxEdgeEntries > 0 && stats.EdgeEntries > stats.MaxEdgeEntries
	stats.OldestVertexRetentionDeadline = c.oldestVertexTombstoneDeadline
	stats.OldestEdgeRetentionDeadline = c.oldestEdgeTombstoneDeadline
	return stats
}

const causalDeadlineCompactSlack = 1024

func (c *GraphCache[S, T]) trackVertexTombstoneDeadlineLocked(key S, deadline time.Time) {
	if !deadline.IsZero() {
		heap.Push(&c.vertexTombstoneDeadlines, causalDeadlineEntry[S]{key: key, deadline: deadline})
		c.vertexTombstoneDeadlineBytes += causalVertexDeadlineEntryBaseBytes + causalKeyPayloadBytes(key)
		c.updateVertexCausalBytesHighWaterLocked()
	}
	c.compactVertexTombstoneDeadlinesLocked()
	c.refreshOldestVertexTombstoneDeadlineLocked()
}

func (c *GraphCache[S, T]) trackEdgeTombstoneDeadlineLocked(key EdgeKey[S], deadline time.Time) {
	if !deadline.IsZero() {
		heap.Push(&c.edgeTombstoneDeadlines, causalDeadlineEntry[EdgeKey[S]]{key: key, deadline: deadline})
		c.edgeTombstoneDeadlineBytes += causalEdgeDeadlineEntryBaseBytes + causalKeyPayloadBytes(key.Tail) + causalKeyPayloadBytes(key.Head)
		c.updateEdgeCausalBytesHighWaterLocked()
	}
	c.compactEdgeTombstoneDeadlinesLocked()
	c.refreshOldestEdgeTombstoneDeadlineLocked()
}

func (c *GraphCache[S, T]) refreshOldestVertexTombstoneDeadlineLocked() {
	for len(c.vertexTombstoneDeadlines) > 0 {
		entry := c.vertexTombstoneDeadlines[0]
		current, ok := c.vertexTombstones[entry.key]
		if ok && !current.expiration.IsZero() && current.expiration.Equal(entry.deadline) {
			c.oldestVertexTombstoneDeadline = entry.deadline
			return
		}
		removed := heap.Pop(&c.vertexTombstoneDeadlines).(causalDeadlineEntry[S])
		c.vertexTombstoneDeadlineBytes -= causalVertexDeadlineEntryBaseBytes + causalKeyPayloadBytes(removed.key)
	}
	c.oldestVertexTombstoneDeadline = time.Time{}
}

func (c *GraphCache[S, T]) refreshOldestEdgeTombstoneDeadlineLocked() {
	for len(c.edgeTombstoneDeadlines) > 0 {
		entry := c.edgeTombstoneDeadlines[0]
		current, ok := c.edgeTombstones[entry.key]
		if ok && !current.expiration.IsZero() && current.expiration.Equal(entry.deadline) {
			c.oldestEdgeTombstoneDeadline = entry.deadline
			return
		}
		removed := heap.Pop(&c.edgeTombstoneDeadlines).(causalDeadlineEntry[EdgeKey[S]])
		c.edgeTombstoneDeadlineBytes -= causalEdgeDeadlineEntryBaseBytes + causalKeyPayloadBytes(removed.key.Tail) + causalKeyPayloadBytes(removed.key.Head)
	}
	c.oldestEdgeTombstoneDeadline = time.Time{}
}

func (c *GraphCache[S, T]) compactVertexTombstoneDeadlinesLocked() {
	if len(c.vertexTombstoneDeadlines) <= 2*len(c.vertexTombstones)+causalDeadlineCompactSlack {
		return
	}
	deadlineCount := 0
	for _, tombstone := range c.vertexTombstones {
		if !tombstone.expiration.IsZero() {
			deadlineCount++
		}
	}
	rebuilt := make(causalDeadlineHeap[S], 0, deadlineCount)
	for key, tombstone := range c.vertexTombstones {
		if !tombstone.expiration.IsZero() {
			rebuilt = append(rebuilt, causalDeadlineEntry[S]{key: key, deadline: tombstone.expiration})
		}
	}
	heap.Init(&rebuilt)
	c.vertexTombstoneDeadlines = rebuilt
	c.vertexTombstoneDeadlineBytes = 0
	for _, entry := range rebuilt {
		c.vertexTombstoneDeadlineBytes += causalVertexDeadlineEntryBaseBytes + causalKeyPayloadBytes(entry.key)
	}
}

func (c *GraphCache[S, T]) compactEdgeTombstoneDeadlinesLocked() {
	if len(c.edgeTombstoneDeadlines) <= 2*len(c.edgeTombstones)+causalDeadlineCompactSlack {
		return
	}
	deadlineCount := 0
	for _, tombstone := range c.edgeTombstones {
		if !tombstone.expiration.IsZero() {
			deadlineCount++
		}
	}
	rebuilt := make(causalDeadlineHeap[EdgeKey[S]], 0, deadlineCount)
	for key, tombstone := range c.edgeTombstones {
		if !tombstone.expiration.IsZero() {
			rebuilt = append(rebuilt, causalDeadlineEntry[EdgeKey[S]]{key: key, deadline: tombstone.expiration})
		}
	}
	heap.Init(&rebuilt)
	c.edgeTombstoneDeadlines = rebuilt
	c.edgeTombstoneDeadlineBytes = 0
	for _, entry := range rebuilt {
		c.edgeTombstoneDeadlineBytes += causalEdgeDeadlineEntryBaseBytes + causalKeyPayloadBytes(entry.key.Tail) + causalKeyPayloadBytes(entry.key.Head)
	}
}

func (c *GraphCache[S, T]) vertexCausalEstimatedBytesLocked() uint64 {
	return c.vertexCausalUsageBytes + c.vertexTombstoneDeadlineBytes
}

func (c *GraphCache[S, T]) edgeCausalEstimatedBytesLocked() uint64 {
	return c.edgeCausalUsageBytes + c.edgeTombstoneDeadlineBytes
}

func (c *GraphCache[S, T]) updateVertexCausalBytesHighWaterLocked() {
	if current := c.vertexCausalEstimatedBytesLocked(); current > c.vertexCausalBytesHighWater {
		c.vertexCausalBytesHighWater = current
	}
}

func (c *GraphCache[S, T]) updateEdgeCausalBytesHighWaterLocked() {
	if current := c.edgeCausalEstimatedBytesLocked(); current > c.edgeCausalBytesHighWater {
		c.edgeCausalBytesHighWater = current
	}
}

func causalKeyPayloadBytes[S comparable](key S) uint64 {
	if value, ok := any(key).(string); ok {
		return uint64(len(value))
	}
	return 0
}

func causalEdgeEstimatedBytes[S comparable](key EdgeKey[S]) uint64 {
	return causalEdgeIdentityBaseBytes + causalKeyPayloadBytes(key.Tail) + causalKeyPayloadBytes(key.Head)
}

func (c *GraphCache[S, T]) ensureVertexCausalUsageLocked(key S) {
	if _, ok := c.vertexCausalUsage[key]; ok {
		return
	}
	if c.vertexCausalUsage == nil {
		c.vertexCausalUsage = make(map[S]uint64)
	}
	bytes := causalVertexIdentityBaseBytes + causalKeyPayloadBytes(key)
	c.vertexCausalUsage[key] = bytes
	c.vertexCausalUsageBytes += bytes
	if len(c.vertexCausalUsage) > c.vertexCausalUsagePeak {
		c.vertexCausalUsagePeak = len(c.vertexCausalUsage)
	}
	if len(c.vertexCausalUsage) > c.vertexCausalHighWater {
		c.vertexCausalHighWater = len(c.vertexCausalUsage)
	}
	c.updateVertexCausalBytesHighWaterLocked()
}

func (c *GraphCache[S, T]) ensureEdgeCausalUsageLocked(key EdgeKey[S]) {
	if _, ok := c.edgeCausalUsage[key]; ok {
		return
	}
	if c.edgeCausalUsage == nil {
		c.edgeCausalUsage = make(map[EdgeKey[S]]uint64)
	}
	bytes := causalEdgeEstimatedBytes(key)
	c.edgeCausalUsage[key] = bytes
	c.edgeCausalUsageBytes += bytes
	if len(c.edgeCausalUsage) > c.edgeCausalUsagePeak {
		c.edgeCausalUsagePeak = len(c.edgeCausalUsage)
	}
	if len(c.edgeCausalUsage) > c.edgeCausalHighWater {
		c.edgeCausalHighWater = len(c.edgeCausalUsage)
	}
	c.updateEdgeCausalBytesHighWaterLocked()
}

func (c *GraphCache[S, T]) vertexHasCausalStateLocked(key S) bool {
	if ts, ok := c.vertexHLC[key]; ok && ts != (hlc.Timestamp{}) {
		return true
	}
	if _, ok := c.vertexCausalBarriers[key]; ok {
		return true
	}
	_, ok := c.vertexTombstones[key]
	return ok
}

func (c *GraphCache[S, T]) edgeHasCausalStateLocked(key EdgeKey[S]) bool {
	if ts, ok := c.edges.lastPutHLC(key.Tail, key.Head); ok && ts != (hlc.Timestamp{}) {
		return true
	}
	if _, ok := c.edgeCausalBarriers[key]; ok {
		return true
	}
	_, ok := c.edgeTombstones[key]
	return ok
}

func (c *GraphCache[S, T]) reconcileVertexCausalUsageLocked(key S) {
	if c.vertexHasCausalStateLocked(key) {
		c.ensureVertexCausalUsageLocked(key)
		return
	}
	bytes, ok := c.vertexCausalUsage[key]
	if !ok {
		return
	}
	delete(c.vertexCausalUsage, key)
	c.vertexCausalUsageBytes -= bytes
	c.shrinkVertexCausalUsageLocked()
}

func (c *GraphCache[S, T]) reconcileEdgeCausalUsageLocked(key EdgeKey[S]) {
	if c.edgeHasCausalStateLocked(key) {
		c.ensureEdgeCausalUsageLocked(key)
		return
	}
	bytes, ok := c.edgeCausalUsage[key]
	if !ok {
		return
	}
	delete(c.edgeCausalUsage, key)
	c.edgeCausalUsageBytes -= bytes
	c.shrinkEdgeCausalUsageLocked()
}

func (c *GraphCache[S, T]) shrinkVertexCausalUsageLocked() {
	after := len(c.vertexCausalUsage)
	if after == 0 {
		c.vertexCausalUsage = nil
		c.vertexCausalUsagePeak = 0
		return
	}
	if c.vertexCausalUsagePeak < causalUsageShrinkFloor || after > c.vertexCausalUsagePeak/causalUsageShrinkDivisor {
		return
	}
	rebuilt := make(map[S]uint64, after)
	for key, bytes := range c.vertexCausalUsage {
		rebuilt[key] = bytes
	}
	c.vertexCausalUsage = rebuilt
	c.vertexCausalUsagePeak = after
}

func (c *GraphCache[S, T]) shrinkEdgeCausalUsageLocked() {
	after := len(c.edgeCausalUsage)
	if after == 0 {
		c.edgeCausalUsage = nil
		c.edgeCausalUsagePeak = 0
		return
	}
	if c.edgeCausalUsagePeak < causalUsageShrinkFloor || after > c.edgeCausalUsagePeak/causalUsageShrinkDivisor {
		return
	}
	rebuilt := make(map[EdgeKey[S]]uint64, after)
	for key, bytes := range c.edgeCausalUsage {
		rebuilt[key] = bytes
	}
	c.edgeCausalUsage = rebuilt
	c.edgeCausalUsagePeak = after
}

func (c *GraphCache[S, T]) checkVertexCausalCapacityLocked(keys []S) error {
	limit := c.causalLimits.MaxVertexEntries
	if limit <= 0 {
		return nil
	}
	seen := make(map[S]struct{}, len(keys))
	requested := 0
	for _, key := range keys {
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if _, retained := c.vertexCausalUsage[key]; !retained {
			requested++
		}
	}
	if requested == 0 {
		return nil
	}
	if len(c.vertexCausalUsage)+requested <= limit {
		return nil
	}
	c.vertexCausalRejected++
	return &CausalMetadataCapacityError{Kind: "vertex", Current: len(c.vertexCausalUsage), Requested: requested, Limit: limit}
}

func (c *GraphCache[S, T]) checkEdgeCausalCapacityLocked(keys []EdgeKey[S]) error {
	limit := c.causalLimits.MaxEdgeEntries
	if limit <= 0 {
		return nil
	}
	seen := make(map[EdgeKey[S]]struct{}, len(keys))
	requested := 0
	for _, key := range keys {
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if _, retained := c.edgeCausalUsage[key]; !retained {
			requested++
		}
	}
	if requested == 0 {
		return nil
	}
	if len(c.edgeCausalUsage)+requested <= limit {
		return nil
	}
	c.edgeCausalRejected++
	return &CausalMetadataCapacityError{Kind: "edge", Current: len(c.edgeCausalUsage), Requested: requested, Limit: limit}
}
