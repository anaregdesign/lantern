package pubsub

import (
	"time"
)

type Message[T any] struct {
	id           string
	body         T
	subscription *Subscription[T]
	lastViewedAt time.Time
	createdAt    time.Time
}

func (m *Message[T]) ID() string {
	return m.id
}

func (m *Message[T]) Body() T {
	return m.body
}

func (m *Message[T]) LastViewedAt() time.Time {
	return m.lastViewedAt
}

func (m *Message[T]) CreatedAt() time.Time {
	return m.createdAt
}

// Ack marks the message as successfully processed and removes it from the
// subscription's in-flight set, so the salvage path will no longer try to
// redeliver it.
func (m *Message[T]) Ack() {
	m.subscription.ack(m)
}

// Nack signals that processing failed and the message should be retried.
// The message is re-queued immediately on the subscription channel; the
// salvage timer is not involved.
func (m *Message[T]) Nack() {
	m.subscription.nack(m)
}
