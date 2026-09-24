package graphcache

import (
	"sort"
	"time"
)

// EdgeDetail is one request-index-aligned GetEdgeDetails result. Found is
// false for an absent, expired, zero-sum, or dangling edge.
type EdgeDetail struct {
	Weight     float32
	Expiration time.Time
	Found      bool
}

type edgeReadBucket[S comparable] struct {
	key            EdgeKey[S]
	tailID, headID vertexID
	weight         *weight
	detail         EdgeDetail
}

// GetEdgeDetails observes all requested edges at one cut. Duplicate keys
// receive the same result in their original request positions. GraphCache.mu
// excludes structural writes, GC, and snapshots that install graph state;
// taking every distinct weight lock before sampling time also excludes the
// existing-edge Add fast path, which deliberately bypasses GraphCache.mu.
//
// Weight locks are acquired in dictionary-ID order. Other multi-edge readers
// use the same order, while the Add fast path holds at most one weight lock.
// No dictionary or edge-map lock is acquired after the first weight lock, and
// every bucket is unlocked before GraphCache.mu is released. A future staged
// publication gate can therefore exclude this whole read, including the
// service's singular GetEdge facade, by taking c.mu.Lock. The standalone core
// GetEdgeDetail point read remains lock-free.
func (c *GraphCache[S, T]) GetEdgeDetails(keys []EdgeKey[S]) []EdgeDetail {
	results := make([]EdgeDetail, len(keys))
	if len(keys) == 0 {
		return results
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	byKey := make(map[EdgeKey[S]]int, len(keys))
	unique := make([]edgeReadBucket[S], 0, len(keys))
	order := make([]int, 0, len(keys))
	for _, key := range keys {
		if _, duplicate := byKey[key]; duplicate {
			continue
		}
		byKey[key] = len(unique)
		entry := edgeReadBucket[S]{key: key}
		if tailID, headID, ok := c.edges.lookupIDs(key.Tail, key.Head); ok {
			c.edges.mu.RLock()
			if heads := c.edges.tf[tailID]; heads != nil {
				entry.weight = heads[headID]
			}
			c.edges.mu.RUnlock()
			if entry.weight != nil {
				entry.tailID, entry.headID = tailID, headID
				order = append(order, len(unique))
			}
		}
		unique = append(unique, entry)
	}

	sort.Slice(order, func(i, j int) bool {
		a, b := unique[order[i]], unique[order[j]]
		if a.tailID != b.tailID {
			return a.tailID < b.tailID
		}
		return a.headID < b.headID
	})
	for _, i := range order {
		unique[i].weight.mu.Lock()
	}
	defer func() {
		for i := len(order) - 1; i >= 0; i-- {
			unique[order[i]].weight.mu.Unlock()
		}
	}()

	// This instant is inside the interval when every requested weight is
	// locked and the graph structure is stable. Apply it to both endpoint
	// liveness and contribution expiration.
	now := time.Now()
	for _, i := range order {
		entry := &unique[i]
		if !c.vertices.HasAt(entry.key.Tail, now) || !c.vertices.HasAt(entry.key.Head, now) {
			continue
		}
		w, exp, ok := entry.weight.snapshotLockedAt(now)
		if ok {
			entry.detail = EdgeDetail{Weight: w, Expiration: exp, Found: true}
		}
	}
	for i, key := range keys {
		results[i] = unique[byKey[key]].detail
	}
	return results
}
