package graphcache

import (
	"errors"
	"time"
)

// ErrEdgeEndpointNotLive rejects a complete constrained local batch before any
// Edge or endpoint changes. It carries no endpoint identity or stored value.
var ErrEdgeEndpointNotLive = errors.New("graphcache: edge endpoint not live")

// RetainDanglingEdgeHistory keeps accepted Edge sources until their own expiry
// even when an endpoint has not arrived or is no longer visible. Public reads
// and snapshots still require live endpoints; private replication snapshots
// retain these sources. Capacity limits continue to account for their buckets.
// Select this generic storage mode before storing any graph state.
func (c *GraphCache[S, T]) RetainDanglingEdgeHistory() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vertices.Count() != 0 || c.edges.count() != 0 {
		panic("graphcache: retained Edge history must be selected before writes")
	}
	c.retainDanglingEdgeHistory = true
}

func (c *GraphCache[S, T]) validateEdgeEndpointsLocked(items []EdgeItem[S], now time.Time) error {
	for _, item := range items {
		if item.RequireLiveEndpoints &&
			(!c.vertices.HasAt(item.Tail, now) || !c.vertices.HasAt(item.Head, now)) {
			return ErrEdgeEndpointNotLive
		}
	}
	return nil
}
