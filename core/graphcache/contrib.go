package graphcache

// ContribID is a globally-unique identifier for an additive edge
// contribution. It packs the 16-byte HLC NodeID of the originating writer
// (offset 0..15) and a monotonic 8-byte big-endian sequence assigned by
// that writer's mutation log (offset 16..23).
//
// The all-zero value is reserved as "no contribution identity" and is the
// value used by every local (non-replicated) write. Code that performs
// dedup MUST skip the dedup check when the incoming ContribID is zero —
// otherwise two distinct local Add calls would collapse into one.
//
// ContribID exists so the replication apply path (issue #182) can ignore
// a Mutation it has already seen — at-least-once delivery from a peer
// must not double-count additive edge weight on re-delivery.
type ContribID [24]byte

// IsZero reports whether c is the zero ContribID. Zero means "no
// identity" and disables dedup on the write path.
func (c ContribID) IsZero() bool {
	return c == ContribID{}
}
