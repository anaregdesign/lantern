package pubsub

import (
	"time"
)

type Message[T any] struct {
	id           uint64
	body         T
	subscription *Subscription[T]
	lastViewedAt time.Time
	createdAt    time.Time
}

// ID returns the message's per-subscription monotonic identifier. IDs are
// unique within a single *Subscription but not across subscriptions or across
// process restarts (#231 — replaced the prior UUID string for hot-path cost).
func (m *Message[T]) ID() uint64 {
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
//
// After Ack (or Nack), the receiver must not be retained: the *Message[T] is
// returned to a sync.Pool and may be reused by a later Publish (#231). Read
// any fields you need before calling Ack/Nack.
func (m *Message[T]) Ack() {
	m.subscription.ack(m)
}

// Nack signals that processing failed and the message should be retried.
// The message is re-queued immediately on the subscription channel; the
// salvage timer is not involved.
//
// After Nack returns the receiver may be re-dispatched concurrently to
// another worker. Do not retain it. See Ack for the pooling contract.
func (m *Message[T]) Nack() {
	m.subscription.nack(m)
}
