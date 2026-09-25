package graphcache

import (
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func newVertexTransactionTestCache() *GraphCache[string, string] {
	c := NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	c.EnableSearchIndex(
		func(_ string, value string) search.Document { return search.Text(value) },
		strings.Compare,
	)
	return c
}

type vertexTransactionState struct {
	base               stagedDeleteState
	physical           map[string]stagedVertexSlot[string]
	search             search.IndexMemoryStats
	vertexPrefix       []string
	dictReverse        []string
	dictRefcounts      []uint32
	vertexDeadlines    []causalDeadlineEntry[string]
	vertexHLCHighWater int
	vertexHLC          map[string]hlc.Timestamp
	vertexTombstones   map[string]tombstoneEntry
	vertexBarriers     map[string]hlc.Timestamp
	vertexCausalUsage  map[string]uint64
}

func captureVertexTransactionState(c *GraphCache[string, string]) vertexTransactionState {
	state := vertexTransactionState{
		base:     captureStagedDeleteState(c),
		physical: make(map[string]stagedVertexSlot[string]),
		search:   c.SearchIndexMemoryStats(),
	}
	c.mu.RLock()
	c.vertices.RangeAt(time.Time{}, func(key, value string, expiration time.Time) bool {
		state.physical[key] = stagedVertexSlot[string]{
			value: value, expiration: expiration, present: true,
		}
		return true
	})
	state.vertexDeadlines = append([]causalDeadlineEntry[string](nil), c.vertexTombstoneDeadlines...)
	state.vertexHLCHighWater = c.vertexHLCHighWater
	state.vertexHLC = mapsClone(c.vertexHLC)
	state.vertexTombstones = mapsClone(c.vertexTombstones)
	state.vertexBarriers = mapsClone(c.vertexCausalBarriers)
	state.vertexCausalUsage = mapsClone(c.vertexCausalUsage)
	c.mu.RUnlock()
	c.dict.mu.RLock()
	state.dictReverse = append([]string(nil), c.dict.reverse...)
	state.dictRefcounts = make([]uint32, len(c.dict.refcount))
	for i := range c.dict.refcount {
		state.dictRefcounts[i] = atomic.LoadUint32(&c.dict.refcount[i])
	}
	c.dict.mu.RUnlock()
	if c.prefixIndex != nil {
		c.prefixIndex.walkPrefix("", func(key string) bool {
			state.vertexPrefix = append(state.vertexPrefix, key)
			return true
		})
	}
	return state
}

func mapsClone[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func equalVertexTransactionAbortState(left, right vertexTransactionState) bool {
	left.search.RetainedTermSlots = 0
	left.search.RetainedOrdinals = 0
	left.search.EstimatedRetainedBytes = 0
	left.search.RebuildCount = 0
	left.search.LastRebuildDuration = 0
	left.search.WriteLockAcquisitions = 0
	left.search.Generation = 0
	right.search.RetainedTermSlots = 0
	right.search.RetainedOrdinals = 0
	right.search.EstimatedRetainedBytes = 0
	right.search.RebuildCount = 0
	right.search.LastRebuildDuration = 0
	right.search.WriteLockAcquisitions = 0
	right.search.Generation = 0
	return reflect.DeepEqual(left, right)
}
