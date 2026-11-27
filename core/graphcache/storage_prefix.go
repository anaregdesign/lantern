package graphcache

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

// DeleteByPrefixFromCommittedCut selects the exact bounded victims on the
// committed Tarp cut. The caller must hold the serving publication gate with
// no graph effects awaiting WAL repair. Only the declared storage key
// projection is eligible. Preflight and application share the graph lock.
func (c *GraphCache[S, T]) DeleteByPrefixFromCommittedCut(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time, preflight func([]S) error) ([]S, error) {
	if preflight == nil || limit == 0 {
		return nil, errors.New("graphcache: stored prefix delete requires preflight and a positive limit")
	}
	if expiration.IsZero() {
		return c.deleteByPrefixKeys(ctx, prefix, uint64(limit), preflight, true)
	}
	return c.deleteByPrefixHLC(ctx, prefix, limit, ts, expiration, true, preflight, true)
}

func (c *GraphCache[S, T]) storedPrefixDeleteEnabledLocked(requested bool) bool {
	return requested && c.storage != nil && c.storagePrefixOrder
}

// Scan only admitted metadata ranges, with one wall sample for TTL and
// tombstones. Count live candidates before HLC filtering, preserving the
// original prefix victim cap even when some are causally superseded.
func (c *GraphCache[S, T]) storedPrefixDeleteVictimsLocked(ctx context.Context, prefix string, limit uint64, ts *hlc.Timestamp) ([]S, bool, error) {
	if err := c.StorageError(); err != nil {
		return nil, false, err
	}
	scope := queryViewFromContext(ctx)
	now := time.Now()
	var keys []S
	var candidates uint64
	err := c.storage.db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		for interval := range scope.Ranges(prefix, false, false) {
			for row, err := range c.storage.rows.LiveVertexRange(ctx, snap, gen, interval.Lower, interval.Upper, nil, false, true, now) {
				if err != nil {
					return err
				}
				candidates++
				if ts == nil {
					keys = append(keys, row.Key)
				} else {
					withoutFloors := storedVertexBefore{
						meta:    graphstore.VertexMetadata{Expiration: row.Vertex.Expiration, HLC: row.Vertex.HLC},
						present: true,
					}
					before, err := c.storage.readVertexFloors(snap, gen, row.Key, withoutFloors)
					if err != nil {
						return err
					}
					if before.writeAllowed(*ts, now) {
						keys = append(keys, row.Key)
					}
				}
				if candidates == limit {
					return nil
				}
			}
		}
		return nil
	})
	if err != nil && err != ctx.Err() {
		return nil, false, c.storage.fence(err)
	}
	return keys, candidates != 0, ctx.Err()
}

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
