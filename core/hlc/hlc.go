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
		c.logical++
	}
	return Timestamp{WallNs: c.wallNs, Logical: c.logical, NodeID: c.nodeID}
}

// Update integrates a timestamp received from a peer. The returned timestamp
// is strictly greater than both the previous local state and remote, and
// the clock's internal state advances accordingly.
//
// If remote.WallNs is more than the configured MaxSkew ahead of local wall
// time, the wall component is clamped to localWall + MaxSkew and the
// configured OnSkewExceeded callback fires with [ErrSkewExceeded]. The
// remote timestamp is never rejected — replication keeps making progress
// even when peers drift, and operators observe the drift through the
// callback (typically wired to a counter).
func (c *Clock) Update(remote Timestamp) Timestamp {
	wall := c.now()
	effectiveRemoteWall := remote.WallNs
	clamped := false
	if remote.WallNs > wall+c.maxSkewNs {
		effectiveRemoteWall = wall + c.maxSkewNs
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
		if remote.Logical > c.logical {
			c.logical = remote.Logical + 1
		} else {
			c.logical++
		}
	case maxWall == c.wallNs:
		// Local state already at the leading wall instant.
		c.logical++
	case maxWall == effectiveRemoteWall:
		// Remote (possibly clamped) leads.
		c.wallNs = maxWall
		c.logical = remote.Logical + 1
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
