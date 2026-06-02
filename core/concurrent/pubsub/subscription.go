package pubsub

import (
	"context"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/model/function"
	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"
)

type Subscription[T any] struct {
	mu          sync.RWMutex
	wg          sync.WaitGroup
	name        string
	topic       *Topic[T]
	ch          chan string
	messages    map[string]*Message[T]
	concurrency int
	interval    time.Duration
	ttl         time.Duration
}

func (s *Subscription[T]) Name() string {
	return s.name
}

func (s *Subscription[T]) Topic() *Topic[T] {
	return s.topic
}

func (s *Subscription[T]) Subscribe(ctx context.Context, consumer function.Consumer[*Message[T]]) {
	sem := semaphore.NewWeighted(int64(s.concurrency))
	s.register()
	go s.watch(ctx, s.interval, s.ttl)

	for {
		select {
		case id := <-s.ch:
			message := s.message(id)
			if message == nil {
				// Already acked (e.g. salvaged past TTL) between publish and lookup.
				continue
			}

			s.wg.Add(1)
			if err := sem.Acquire(ctx, 1); err != nil {
				s.wg.Done()
				continue
			}
			go func(m *Message[T]) {
				defer sem.Release(1)
				defer s.wg.Done()
				consumer(m)
			}(message)

		case <-ctx.Done():
			s.wg.Wait()
			s.unregister()
			return
		}
	}
}

func (s *Subscription[T]) register() {
	s.topic.register(s)
}

func (s *Subscription[T]) unregister() {
	s.topic.unregister(s)
}

func (s *Subscription[T]) newMessage(body T) *Message[T] {
	return &Message[T]{
		id:           uuid.New().String(),
		body:         body,
		subscription: s,
		createdAt:    time.Now(),
	}
}

func (s *Subscription[T]) message(id string) *Message[T] {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.messages[id]
}

func (s *Subscription[T]) publish(message *Message[T]) {
	s.mu.Lock()
	s.messages[message.id] = message
	s.mu.Unlock()

	// Send outside the lock: a full channel would otherwise block while the
	// lock is held, deadlocking any concurrent ack/message lookup.
	s.ch <- message.id
}

func (s *Subscription[T]) ack(message *Message[T]) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.messages, message.id)
}

func (s *Subscription[T]) nack(message *Message[T]) {
}

func (s *Subscription[T]) remind(message *Message[T]) {
	s.ch <- message.id
}

func (s *Subscription[T]) salvage(interval time.Duration, ttl time.Duration) {
	// Snapshot under the lock so we can iterate without racing publish/ack.
	s.mu.RLock()
	snapshot := make([]*Message[T], 0, len(s.messages))
	for _, m := range s.messages {
		snapshot = append(snapshot, m)
	}
	s.mu.RUnlock()

	now := time.Now()
	for _, message := range snapshot {
		if now.Sub(message.createdAt) > ttl {
			s.ack(message)
			continue
		}
		if now.Sub(message.lastViewedAt) > interval {
			s.remind(message)
		}
	}
}

func (s *Subscription[T]) watch(ctx context.Context, interval time.Duration, ttl time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.salvage(interval, ttl)
		case <-ctx.Done():
			s.wg.Wait()
			return
		}
	}
}
