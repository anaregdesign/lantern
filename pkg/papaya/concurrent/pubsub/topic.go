package pubsub

import (
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

func (t *Topic[T]) Subscriptions() map[string]*Subscription[T] {
	return t.subscriptions
}

func (t *Topic[T]) Publish(body T) {
	for _, s := range t.subscriptions {
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

	if _, ok := t.subscriptions[subscription.name]; ok {
		delete(t.subscriptions, subscription.name)
	}
}
