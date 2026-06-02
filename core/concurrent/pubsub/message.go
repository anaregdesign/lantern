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

func (m *Message[T]) Ack() {
	m.subscription.ack(m)
}

func (m *Message[T]) Nack() {
	m.subscription.nack(m)
}

func (m *Message[T]) touch() {
	m.lastViewedAt = time.Now()
}
