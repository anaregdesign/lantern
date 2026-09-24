// Package hlc implements a Hybrid Logical Clock (HLC) suitable for stamping
// mutations in Lantern's leaderless full-replica replication design.
//
// The construction follows Kulkarni, Demirbas, Madappa, Avva and Leone,
// "Logical Physical Clocks and Consistent Snapshots in Globally Distributed
// Databases" (2014). An HLC timestamp interleaves a wall-clock component
// (nanoseconds since the Unix epoch) with a logical counter that bumps when
// two events would otherwise collide on the same wall instant. Comparison is
// lexicographic over (wallNs, logical, nodeID), giving a total order without
// requiring synchronized clocks while staying close to physical time.
//
// This package is a leaf: it imports only the standard library. Wire encoding
// (proto round-trips) lives with the mutation message in pb/.
package hlc

import (
	"errors"
	"math"
	"sync"
	"time"
)

// DefaultMaxSkew is the default ceiling for the gap between a remote
// timestamp's wall component and local wall time. See replication RFC D3.
const DefaultMaxSkew = 500 * time.Millisecond

// ErrSkewExceeded is reported via [Clock.OnSkewExceeded] when an [Update]
// call observes a remote wall time more than MaxSkew ahead of local wall
// time. The remote timestamp is clamped, never rejected, so replication
// continues to make progress even when peers drift.
var ErrSkewExceeded = errors.New("hlc: remote wall time exceeds MaxSkew")

// ErrInvalidRestoreFloor means a persisted timestamp cannot safely seed a
// clock. RestoreFloor leaves the clock unchanged when it returns this error.
var ErrInvalidRestoreFloor = errors.New("hlc: invalid restore floor")

// NodeID identifies the origin of a timestamp. It is opaque to this package;
// callers typically derive it from a stable per-process UUID.
type NodeID [16]byte

// Timestamp is a Hybrid Logical Clock value.
//
// WallNs holds nanoseconds since the Unix epoch as observed at the origin
// clock at the moment the timestamp was produced. Logical is a counter that
// distinguishes events that share a WallNs. NodeID breaks the remaining ties
// so that two distinct origins can never produce equal timestamps.
type Timestamp struct {
	WallNs  int64
	Logical uint32
	NodeID  NodeID
}

// Less reports whether t orders strictly before other in the HLC total order.
func (t Timestamp) Less(other Timestamp) bool {
	if t.WallNs != other.WallNs {
		return t.WallNs < other.WallNs
	}
	if t.Logical != other.Logical {
		return t.Logical < other.Logical
	}
	for i := range t.NodeID {
		if t.NodeID[i] != other.NodeID[i] {
			return t.NodeID[i] < other.NodeID[i]
		}
	}
	return false
}

// Equal reports whether t and other are bit-identical.
func (t Timestamp) Equal(other Timestamp) bool {
	return t.WallNs == other.WallNs &&
		t.Logical == other.Logical &&
		t.NodeID == other.NodeID
}

// Clock is a thread-safe Hybrid Logical Clock for a single origin node.
//
// The zero value is not usable; construct with [New].
type Clock struct {
	nodeID NodeID

	// now returns current wall time in nanoseconds since the Unix epoch.
	// Tests inject a deterministic source via [Options.Now].
	now func() int64

	maxSkewNs      int64
	onSkewExceeded func(remote Timestamp, localWallNs int64, err error)

	mu      sync.Mutex
	wallNs  int64
	logical uint32
}

// Options configures a [Clock]. The zero value is valid: it uses the real
// monotonic wall clock, [DefaultMaxSkew], and a no-op skew callback.
type Options struct {
	// Now overrides the wall-time source. Useful for deterministic tests.
	// Must return nanoseconds since the Unix epoch.
	Now func() int64
	// MaxSkew bounds how far ahead of local wall time a remote stamp may be
	// before being clamped on Update. Zero means [DefaultMaxSkew].
	MaxSkew time.Duration
	// OnSkewExceeded is invoked, with the lock released, when Update clamps
	// a remote timestamp. Use it to emit a metric or log line.
	OnSkewExceeded func(remote Timestamp, localWallNs int64, err error)
}

// New constructs a Clock for the given origin node.
func New(nodeID NodeID, opts Options) *Clock {
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixNano() }
	}
	skew := opts.MaxSkew
	if skew <= 0 {
		skew = DefaultMaxSkew
	}
	cb := opts.OnSkewExceeded
	if cb == nil {
		cb = func(Timestamp, int64, error) {}
	}
	return &Clock{
		nodeID:         nodeID,
		now:            now,
		maxSkewNs:      skew.Nanoseconds(),
		onSkewExceeded: cb,
	}
}

// NodeID returns the origin identifier this clock stamps timestamps with.
func (c *Clock) NodeID() NodeID { return c.nodeID }

// RestoreFloor seeds the clock from a verified, committed timestamp. It does
// not apply the live-peer skew clamp: a backward wall-clock jump must not put
// new mutations below a timestamp that was already committed. The caller must
// validate the durable WAL/Snapshot cut before calling this method; an
// untrusted remote timestamp belongs in Update instead.
//
// RestoreFloor is safe to call concurrently with Now and Update. It never
// moves an already-used clock backward. A negative wall timestamp or the
// maximum representable wall/logical pair cannot provide a safe next stamp.
func (c *Clock) RestoreFloor(floor Timestamp) error {
	if floor.WallNs < 0 || (floor.WallNs == math.MaxInt64 && floor.Logical == math.MaxUint32) {
		return ErrInvalidRestoreFloor
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if floor.WallNs > c.wallNs || (floor.WallNs == c.wallNs && floor.Logical > c.logical) {
		c.wallNs = floor.WallNs
		c.logical = floor.Logical
	}
	return nil
}

// bumpLogical advances the clock past the current wall/logical pair. The
// representable HLC space is exhausted only at its absolute maximum; fail
// closed there instead of wrapping into an older timestamp.
func (c *Clock) bumpLogical() {
	if c.logical == math.MaxUint32 {
		if c.wallNs == math.MaxInt64 {
			panic("hlc: timestamp space exhausted")
		}
		c.wallNs++
		c.logical = 0
		return
	}
	c.logical++
}

// Now returns the next timestamp from this clock. The returned timestamp is
// strictly greater than every previously returned timestamp from the same
// clock and from any remote timestamp previously passed to [Clock.Update].
func (c *Clock) Now() Timestamp {
	wall := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if wall > c.wallNs {
		c.wallNs = wall
		c.logical = 0
	} else {
		c.bumpLogical()
	}
	return Timestamp{WallNs: c.wallNs, Logical: c.logical, NodeID: c.nodeID}
}

// Update integrates a timestamp received from a peer. The returned timestamp
// is strictly greater than the previous local state and the effective remote
// timestamp after any skew clamp, and the clock's internal state advances.
//
// If remote.WallNs is more than the configured MaxSkew ahead of local wall
// time, the wall component is clamped to localWall + MaxSkew and the
// configured OnSkewExceeded callback fires with [ErrSkewExceeded]. The
// remote timestamp is never rejected — replication keeps making progress
// even when peers drift, and operators observe the drift through the
// callback (typically wired to a counter).
func (c *Clock) Update(remote Timestamp) Timestamp {
	wall := c.now()
	// Saturate the skew ceiling instead of overflowing near MaxInt64.
	ceiling := int64(math.MaxInt64)
	if wall <= math.MaxInt64-c.maxSkewNs {
		ceiling = wall + c.maxSkewNs
	}
	effectiveRemoteWall := remote.WallNs
	effectiveRemoteLogical := remote.Logical
	clamped := false
	if remote.WallNs > ceiling {
		effectiveRemoteWall = ceiling
		// The remote logical counter belongs to its rejected future wall
		// instant. Carrying it into the clamped instant can push the
		// result past the skew ceiling when that counter is exhausted.
		effectiveRemoteLogical = 0
		clamped = true
	}

	c.mu.Lock()
	maxWall := wall
	if effectiveRemoteWall > maxWall {
		maxWall = effectiveRemoteWall
	}
	if c.wallNs > maxWall {
		maxWall = c.wallNs
	}

	switch {
	case maxWall == c.wallNs && maxWall == effectiveRemoteWall:
		// Both local state and remote are at the same wall instant. The
		// logical counter must exceed both contributing counters.
		if effectiveRemoteLogical > c.logical {
			c.logical = effectiveRemoteLogical
		}
		c.bumpLogical()
	case maxWall == c.wallNs:
		// Local state already at the leading wall instant.
		c.bumpLogical()
	case maxWall == effectiveRemoteWall:
		// Remote (possibly clamped) leads.
		c.wallNs = maxWall
		c.logical = effectiveRemoteLogical
		c.bumpLogical()
	default:
		// Physical wall time has moved past both prior states.
		c.wallNs = maxWall
		c.logical = 0
	}

	out := Timestamp{WallNs: c.wallNs, Logical: c.logical, NodeID: c.nodeID}
	c.mu.Unlock()

	if clamped {
		c.onSkewExceeded(remote, wall, ErrSkewExceeded)
	}
	return out
}
