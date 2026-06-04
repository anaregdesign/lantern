package pubsub

import (
	"maps"
	"slices"
	"sync"
	"time"
)

type Topic[T any] struct {
	mu            sync.RWMutex
	name          string
	subscriptions map[string]*Subscription[T]
}

func NewTopic[T any](name string) *Topic[T] {
	return &Topic[T]{
		name:          name,
		subscriptions: make(map[string]*Subscription[T]),
	}
}

func (t *Topic[T]) Name() string {
	return t.name
}

// Subscriptions returns a snapshot of the topic's subscriptions. The returned
// map is a clone, safe for callers to iterate without holding the topic lock.
func (t *Topic[T]) Subscriptions() map[string]*Subscription[T] {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return maps.Clone(t.subscriptions)
}

// Publish performs best-effort concurrent fan-out: each subscription receives
// the message on its own goroutine, so a single slow or saturated subscriber
// cannot block delivery to its peers (#230). Delivery order across
// subscriptions is unspecified, matching the prior map-iteration behavior.
//
// Publish returns immediately after scheduling the fan-out goroutines; it does
// not wait for each subscription's channel send to complete. This trades
// synchronous backpressure (which no caller currently observes) for
// independence between subscribers. The pattern is intended for topics with
// tens of subscribers; if you need to fan out to thousands, prefer a
// long-lived per-subscription delivery goroutine instead.
func (t *Topic[T]) Publish(body T) {
	t.mu.RLock()
	subs := slices.Collect(maps.Values(t.subscriptions))
	t.mu.RUnlock()

	for _, s := range subs {
		s := s
		go func() {
			m := s.newMessage(body)
			s.publish(m)
		}()
	}
}

// NewSubscription creates (or returns the existing) Subscription with the
// historical positional arguments. New code should prefer
// NewSubscriptionWithOptions which exposes buffer size and full-policy knobs;
// this wrapper keeps existing call sites compiling.
func (t *Topic[T]) NewSubscription(name string, concurrency int, interval time.Duration, ttl time.Duration) *Subscription[T] {
	return t.NewSubscriptionWithOptions(
		name,
		WithConcurrency(concurrency),
		WithSalvageInterval(interval),
		WithTTL(ttl),
	)
}

// NewSubscriptionWithOptions creates (or returns the existing) Subscription
// configured via functional options. Defaults match the positional
// NewSubscription wrapper: concurrency 1, interval/ttl 1 minute, buffer 65536,
// FullPolicyBlock. If a Subscription with name already exists it is returned
// unchanged; options on a second call are ignored.
func (t *Topic[T]) NewSubscriptionWithOptions(name string, opts ...SubscriptionOption) *Subscription[T] {
	cfg := defaultSubscriptionConfig()
	for _, o := range opts {
		o(&cfg)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.subscriptions[name]; !ok {
		t.subscriptions[name] = &Subscription[T]{
			name:        name,
			topic:       t,
			ch:          make(chan uint64, cfg.bufferSize),
			messages:    make(map[uint64]*Message[T]),
			concurrency: cfg.concurrency,
			interval:    cfg.interval,
			ttl:         cfg.ttl,
			bufferSize:  cfg.bufferSize,
			fullPolicy:  cfg.fullPolicy,
		}
	}
	return t.subscriptions[name]
}

func (t *Topic[T]) register(subscription *Subscription[T]) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.subscriptions[subscription.name]; !ok {
		t.subscriptions[subscription.name] = subscription
	}
}

func (t *Topic[T]) unregister(subscription *Subscription[T]) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.subscriptions, subscription.name)
}
