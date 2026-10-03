package graphcache

import (
	"context"
	"time"
)

// SnapshotGraphContext captures an admitted query view under one read lock.
// Hidden values and contribution arrays are never copied. Private replication
// and persistence continue to use the complete SnapshotGraph boundary.
func (c *GraphCache[S, T]) SnapshotGraphContext(ctx context.Context) (GraphSnapshot[S, T], error) {
	if err := ctx.Err(); err != nil {
		return GraphSnapshot[S, T]{}, err
	}
	view := queryViewFromContext(ctx)
	if view == nil {
		return c.SnapshotGraph(), nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	var snapshot GraphSnapshot[S, T]
	visible := make(map[S]bool)
	if c.prefixIndex != nil {
		walkVisiblePrefix(ctx, c.prefixIndex, "", "", true, false, false, func(projected string) bool {
			if ctx.Err() != nil {
				return false
			}
			key, known := c.keyForProjection(projected)
			if !known {
				return true
			}
			value, expiration, found := c.vertices.PeekWithExpiration(key)
			if !found || !expiration.IsZero() && !now.Before(expiration) {
				return true
			}
			visible[key] = true
			snapshot.Vertices = append(snapshot.Vertices, SnapshotVertex[S, T]{Key: key, Value: value, Expiration: expiration, HLC: c.vertexHLC[key]})
			return true
		})
	}
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		if ctx.Err() != nil {
			return false
		}
		if !visible[tail] || !visible[head] || !c.queryEdgeVisible(view, tail, head) {
			return true
		}
		contributions, ts, live := w.snapshotEntry(now)
		if live {
			snapshot.Edges = append(snapshot.Edges, SnapshotEdge[S]{Tail: tail, Head: head, HLC: ts, Contributions: contributions})
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return GraphSnapshot[S, T]{}, err
	}
	return snapshot, nil
}
