package pubsub

import (
	"maps"
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

func (t *Topic[T]) Publish(body T) {
	t.mu.RLock()
	subs := make([]*Subscription[T], 0, len(t.subscriptions))
	for _, s := range t.subscriptions {
		subs = append(subs, s)
	}
	t.mu.RUnlock()

	for _, s := range subs {
		message := s.newMessage(body)
		s.publish(message)
	}
}

func (t *Topic[T]) NewSubscription(name string, concurrency int, interval time.Duration, ttl time.Duration) *Subscription[T] {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.subscriptions[name]; !ok {
		t.subscriptions[name] = &Subscription[T]{
			name:        name,
			topic:       t,
			ch:          make(chan string, 65536),
			messages:    make(map[string]*Message[T]),
			concurrency: concurrency,
			interval:    interval,
			ttl:         ttl,
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
