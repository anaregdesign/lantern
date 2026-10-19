// Package decisiontrace captures bounded, nonsecret diagnostic facts. It is
// disabled by default and never supplies authorization inputs. Capture/Drain
// are used by explicit diagnostic fixtures, not public HTTP/RPC endpoints.
package decisiontrace

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event contains fixed classifications, numeric samples and public-byte
// digests only. Never put arbitrary errors, identities, endpoints or packets
// in these fields. Check refers to the actual native sample, not a resample.
type Event struct {
	At             time.Time `json:"at"`
	Kind           string    `json:"kind"`
	Object         uint64    `json:"object,omitempty"`
	Check          uint64    `json:"check,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Stage          string    `json:"stage,omitempty"`
	NativeReason   string    `json:"native_reason,omitempty"`
	Native         uint64    `json:"native,omitempty"`
	Anchor         uint64    `json:"anchor,omitempty"`
	HasAnchor      bool      `json:"has_anchor,omitempty"`
	AgeUpper       uint64    `json:"age_upper,omitempty"`
	Width          uint64    `json:"width,omitempty"`
	Low            uint64    `json:"low,omitempty"`
	High           uint64    `json:"high,omitempty"`
	Attempt        uint64    `json:"attempt,omitempty"`
	Turn           uint64    `json:"turn,omitempty"`
	DNSCount       int       `json:"dns_count,omitempty"`
	DialAttempts   int       `json:"dial_attempts,omitempty"`
	DNSDigest      string    `json:"dns_digest,omitempty"`
	EndpointDigest string    `json:"endpoint_digest,omitempty"`
	Family         string    `json:"family,omitempty"`
	Elapsed        int64     `json:"elapsed,omitempty"`
	HasDeadline    bool      `json:"has_deadline,omitempty"`
	DeadlineLeft   int64     `json:"deadline_left,omitempty"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	Delay          int64     `json:"delay,omitempty"`
	Repeats        uint64    `json:"previous_repeats,omitempty"`
}

// AcquisitionCounts are monotone process-owned counters, not lossy trace
// events and never currentness permits. They contain no source/identity data.
type AcquisitionCounts struct {
	Attempts, Writes, Installations uint64
	InFlight, Failed, Denied        bool
}

var active atomic.Pointer[Capture]
var serial atomic.Uint64

func NextID() uint64 {
	if !Enabled() {
		return 0
	}
	return serial.Add(1)
}
func Enabled() bool { return active.Load() != nil }

// Capture uses a fixed-capacity memory queue. Emit performs no I/O, does not
// call a user callback and never waits for its mutex or for available capacity.
// The fixture owns draining/serialization outside every product lock.
type Capture struct {
	mu      sync.Mutex
	events  []Event
	limit   int
	dropped atomic.Uint64
}

func Start(limit int) *Capture {
	if limit < 1 || limit > 65536 {
		return nil
	}
	c := &Capture{events: make([]Event, 0, limit), limit: limit}
	if !active.CompareAndSwap(nil, c) {
		return nil
	}
	return c
}
func Emit(e Event) {
	c := active.Load()
	if c == nil {
		return
	}
	e.At = time.Now().UTC() // correlation only; never an authority clock
	if !c.mu.TryLock() {
		c.dropped.Add(1)
		return
	}
	if len(c.events) < c.limit {
		c.events = append(c.events, e)
	} else {
		c.dropped.Add(1)
	}
	c.mu.Unlock()
}
func (c *Capture) Drain() ([]Event, uint64) {
	c.mu.Lock()
	result := append([]Event(nil), c.events...)
	c.events = c.events[:0]
	c.mu.Unlock()
	return result, c.dropped.Load()
}
func (c *Capture) Stop() { active.CompareAndSwap(c, nil) }

// Transition belongs to the same lock as the decision it observes. It folds
// identical outcomes until the next transition; counts never drive admission.
type Transition struct {
	key     string
	repeats uint64
}

func (s *Transition) Emit(key string, e Event) {
	if !Enabled() {
		return
	}
	if s.key == key {
		s.repeats++
		return
	}
	e.Repeats = s.repeats
	s.key, s.repeats = key, 0
	Emit(e)
}
