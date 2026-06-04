package pubsub

import "time"

// FullPolicy selects the behavior of Publish when a Subscription's delivery
// channel is full. The default, FullPolicyBlock, preserves the historical
// contract: Publish blocks until a worker drains the channel. DropNewest and
// DropOldest trade strict delivery for liveness and are observable via a
// slog.Warn line on every drop.
type FullPolicy int

const (
	// FullPolicyBlock blocks Publish until the channel has room (default).
	FullPolicyBlock FullPolicy = iota
	// FullPolicyDropNewest discards the incoming id when the channel is full.
	// The Message envelope is acked and returned to the pool immediately so
	// no slot is wasted in the in-flight map.
	FullPolicyDropNewest
	// FullPolicyDropOldest discards the oldest queued id and enqueues the
	// new one. The dropped Message is acked and returned to the pool.
	FullPolicyDropOldest
)

// defaultBufferSize matches the historical hardcoded channel capacity so
// callers that switch to NewSubscriptionWithOptions without WithBufferSize
// observe identical buffering.
const defaultBufferSize = 65536

// SubscriptionOption configures a Subscription created via
// Topic.NewSubscriptionWithOptions. Options compose: later options overwrite
// earlier ones.
type SubscriptionOption func(*subscriptionConfig)

type subscriptionConfig struct {
	concurrency int
	interval    time.Duration
	ttl         time.Duration
	bufferSize  int
	fullPolicy  FullPolicy
	observer    Observer
}

func defaultSubscriptionConfig() subscriptionConfig {
	return subscriptionConfig{
		concurrency: 1,
		interval:    time.Minute,
		ttl:         time.Minute,
		bufferSize:  defaultBufferSize,
		fullPolicy:  FullPolicyBlock,
		observer:    noopObserver{},
	}
}

// WithConcurrency sets the number of worker goroutines that Subscribe spawns
// to drain the delivery channel. Values < 1 are clamped to 1 at Subscribe
// time. The historical positional constructor maps its concurrency argument
// to this option.
func WithConcurrency(n int) SubscriptionOption {
	return func(c *subscriptionConfig) { c.concurrency = n }
}

// WithSalvageInterval sets the period of the watch ticker that drives expiry
// eviction and re-delivery reminders.
func WithSalvageInterval(d time.Duration) SubscriptionOption {
	return func(c *subscriptionConfig) { c.interval = d }
}

// WithTTL sets the maximum age of an in-flight Message before salvage evicts
// it (acks on the consumer's behalf and returns the envelope to the pool).
func WithTTL(d time.Duration) SubscriptionOption {
	return func(c *subscriptionConfig) { c.ttl = d }
}

// WithBufferSize sets the capacity of the delivery channel. Values < 1 are
// clamped to 1 to keep the channel buffered (an unbuffered channel would
// serialise every Publish against every worker).
func WithBufferSize(n int) SubscriptionOption {
	return func(c *subscriptionConfig) {
		if n < 1 {
			n = 1
		}
		c.bufferSize = n
	}
}

// WithFullPolicy selects the behavior of Publish when the delivery channel is
// full. See FullPolicy for the available modes.
func WithFullPolicy(p FullPolicy) SubscriptionOption {
	return func(c *subscriptionConfig) { c.fullPolicy = p }
}

// WithObserver installs an Observer that receives enqueue-depth samples,
// drop counters, and dispatch-duration observations for this subscription.
// A nil observer reverts to the no-op (no telemetry). core/ never imports
// server/metrics directly; the server wires a Prometheus-backed Observer
// here (#240).
func WithObserver(o Observer) SubscriptionOption {
	return func(c *subscriptionConfig) {
		if o == nil {
			c.observer = noopObserver{}
			return
		}
		c.observer = o
	}
}
