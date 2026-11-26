package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

// EnableStorageKeyPrefixIndex declares that extract returns the exact ordered
// key projection encoded by the graphstore Rows. A detached staging graph uses
// a radix until its generation is published. Bound serving graphs use the
// database's existing ordered Vertex and Edge keys.
func (c *GraphCache[S, T]) EnableStorageKeyPrefixIndex(extract func(S) string) {
	c.EnablePrefixIndex(extract)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storagePrefixOrder = true
}

// Caller owns the fresh cache or its exclusive publication cut.
func (c *GraphCache[S, T]) releaseStorageIndexes() {
	// Public edge scans use the stored (tail, head) order. Internal scans
	// that operate on a staged bucket support sorting its current heads.
	c.headByTail = nil
	if c.storagePrefixOrder {
		c.prefixIndex = nil
	}
}

// walkVertexPrefix walks the configured projection, respecting each admitted
// interval before accessing a current row. Inclusive bounds are needed when
// resuming the tail of an edge cursor.
func (c *GraphCache[S, T]) walkVertexPrefix(ctx context.Context, prefix, after string, inclusive, reverse, edges bool, visit func(string) bool) {
	if c.prefixIndex != nil {
		walkVisiblePrefix(ctx, c.prefixIndex, prefix, after, inclusive, reverse, edges, visit)
		return
	}
	c.walkStoredVertexKeys(ctx, prefix, after, inclusive, reverse, edges, func(key S) bool {
		return visit(c.prefixExtract(key))
	})
}

func (c *GraphCache[S, T]) walkStoredVertexKeys(ctx context.Context, prefix, after string, inclusive, reverse, edges bool, visit func(S) bool) {
	scope := queryViewFromContext(ctx)
	err := c.storage.db.WithCommittedSnapshot(func(g uint64, snap *tarp.Snapshot) error {
		for interval := range scope.Ranges(prefix, edges, reverse) {
			lo, hi := interval.Lower, interval.Upper
			if after != "" {
				// The immediate successor of any complete binary string is
				// itself followed by NUL, independent of UTF-8 boundaries.
				if reverse {
					upper := after
					if inclusive {
						upper += "\x00"
					}
					hi = earlierEnd(hi, upper)
				} else {
					lower := after
					if !inclusive {
						lower += "\x00"
					}
					lo = max(lo, lower)
				}
			}
			if hi != "" && lo >= hi {
				continue
			}
			for key, err := range c.storage.rows.VertexKeysRange(ctx, snap, g, lo, hi, nil, reverse) {
				if err != nil {
					return err
				}
				projected := c.prefixExtract(key)
				if edges && !scope.Vertex(projected) {
					continue
				}
				if !visit(key) {
					return nil
				}
			}
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		c.storage.fence(err)
	}
}

func (c *GraphCache[S, T]) walkStoredTailHeads(ctx context.Context, tail S, tailProj, prefix, after string, visit func(string, S, string, S, float32, time.Time) bool) bool {
	scope := queryViewFromContext(ctx)
	remaining := true
	now := time.Now()
	err := c.storage.db.WithCommittedSnapshot(func(g uint64, snap *tarp.Snapshot) error {
		for bounds := range scope.Ranges(prefix, true, false) {
			lo := bounds.Lower
			if after != "" {
				lo = max(lo, after+"\x00")
			}
			if bounds.Upper != "" && lo >= bounds.Upper {
				continue
			}
			for head, err := range c.storage.rows.EdgeHeadsRange(ctx, snap, g, tail, lo, bounds.Upper, nil) {
				if err != nil {
					return err
				}
				projected := c.prefixExtract(head)
				if !scope.Edge(tailProj, projected) {
					continue
				}
				d, err := c.storage.rows.EdgeAt(ctx, snap, g, graphstore.EdgeKey[S]{Tail: tail, Head: head}, now)
				if err != nil {
					return err
				}
				if d.Found && !visit(tailProj, tail, projected, head, d.Weight, d.Expiration) {
					remaining = false
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		if ctx.Err() == nil {
			c.storage.fence(err)
		}
		return false
	}
	return remaining
}
