package graphcache

// onVertexEvicted keeps all vertex-owned side structures in sync with a
// vertex cache eviction. It is installed as cache.Cache.SetOnEvict and may be
// invoked by Delete, Clear, or Flush. Some callers hold GraphCache.mu when the
// hook fires and some do not, so this helper must not assume the aggregate lock
// is held.
func (c *GraphCache[S, T]) onVertexEvicted(key S) {
	if c.dict != nil {
		if id, ok := c.dict.lookup(key); ok {
			c.dict.release(id)
		}
	}
	c.deleteVertexIndexes(key)
}

// onExplicitVertexStoredLocked updates vertex secondary indexes after a caller
// has stored an explicit user value. Prefix membership changes only on first
// insert, while search postings refresh on every put because overwrites can
// change the indexed document. Caller must hold c.mu.
func (c *GraphCache[S, T]) onExplicitVertexStoredLocked(key S, value T, firstInsert bool) {
	if firstInsert {
		c.insertVertexPrefixLocked(key)
	}
	if c.searchIndex != nil {
		c.searchIndex.Index(key, c.searchExtract(value))
	}
}

// onEndpointVertexCreatedLocked updates the vertex-side indexes for an
// auto-created edge endpoint. Endpoint creation records key existence for
// prefix scans but deliberately does not index search content: the zero T value
// is not an explicit user document. Caller must hold c.mu.
func (c *GraphCache[S, T]) onEndpointVertexCreatedLocked(key S) {
	c.insertVertexPrefixLocked(key)
}

func (c *GraphCache[S, T]) insertVertexPrefixLocked(key S) {
	if c.prefixIndex != nil {
		c.prefixIndex.insert(c.prefixExtract(key))
	}
}

func (c *GraphCache[S, T]) deleteVertexIndexes(key S) {
	if c.prefixIndex != nil {
		c.prefixIndex.delete(c.prefixExtract(key))
	}
	if c.searchIndex != nil {
		c.searchIndex.Delete(key)
	}
}

// onEdgeAddedLocked maintains the per-tail head index after a successful
// edges.addWithExpiration. When created is false the underlying bucket already
// existed and the index already reflects it; the call is a no-op. Caller must
// hold c.mu.
func (c *GraphCache[S, T]) onEdgeAddedLocked(created bool, tailID, headID vertexID, head S) {
	if !created || c.headByTail == nil {
		return
	}
	hi := c.headByTail[tailID]
	if hi == nil {
		hi = newHeadIndex()
		c.headByTail[tailID] = hi
	}
	hi.insert(headID, c.prefixExtract(head))
}

// onEdgeDeletedLocked maintains the per-tail head index after a successful
// edges.delete (or before a flushFunc deleteLocked). The head argument is the
// original S key the caller already has in hand, so we never have to resolve it
// through the dict, which matters because dict refs are released by deleteLocked.
// Caller must hold c.mu.
func (c *GraphCache[S, T]) onEdgeDeletedLocked(tailID, headID vertexID, head S) {
	if c.headByTail == nil {
		return
	}
	hi, ok := c.headByTail[tailID]
	if !ok {
		return
	}
	if empty := hi.delete(headID, c.prefixExtract(head)); empty {
		delete(c.headByTail, tailID)
	}
}

// headIndexOnFlushDeleteLocked returns the onDelete callback handed to
// edgeCache.flushFunc. It captures head S BEFORE deleteLocked runs (flushFunc
// invokes the callback first), so resolveID is guaranteed to succeed. Returns
// nil when the head index is disabled, in which case flushFunc skips the call
// entirely. Caller must hold c.mu.
func (c *GraphCache[S, T]) headIndexOnFlushDeleteLocked() func(tailID, headID vertexID) {
	if c.headByTail == nil {
		return nil
	}
	return func(tailID, headID vertexID) {
		head, ok := c.edges.resolveID(headID)
		if !ok {
			return
		}
		c.onEdgeDeletedLocked(tailID, headID, head)
	}
}

// disableHeadIndexForTesting drops the per-tail head index so
// ScanEdgesByPrefix falls back to the v1 materialise-and-sort path. It exists
// so regression tests and before/after benchmarks can exercise the fallback
// under identical data. Not part of the public API.
func (c *GraphCache[S, T]) disableHeadIndexForTesting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headByTail = nil
}
