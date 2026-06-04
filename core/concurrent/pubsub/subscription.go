package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/model/function"
	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"
)

type Subscription[T any] struct {
	mu          sync.RWMutex
	wg          sync.WaitGroup
	started     atomic.Bool
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

// Subscribe drains the subscription queue and dispatches messages to consumer
// until ctx is cancelled. It is single-flight per *Subscription: concurrent or
// reentrant calls return immediately without dispatching. After Subscribe
// returns (ctx done) the subscription may be Subscribe'd to again.
func (s *Subscription[T]) Subscribe(ctx context.Context, consumer function.Consumer[*Message[T]]) {
	if !s.started.CompareAndSwap(false, true) {
		// Another goroutine already owns the dispatcher for this
		// subscription; the second caller's ch reads would steal messages
		// from the first. Refuse silently and let the first finish.
		return
	}
	defer s.started.Store(false)

	sem := semaphore.NewWeighted(int64(s.concurrency))
	s.register()
	go s.watch(ctx, s.interval, s.ttl)

	for {
		select {
		case id := <-s.ch:
			message := s.dispatch(id)
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

// dispatch atomically looks up id and stamps lastViewedAt so the salvage path
// can tell whether the message has been recently handed to a consumer. It is
// the only correct way for the Subscribe loop to take a message off s.ch; a
// plain message() read leaves lastViewedAt at the zero value forever, which
// makes salvage re-push the same id every interval tick (#229).
func (s *Subscription[T]) dispatch(id string) *Message[T] {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.messages[id]
	if !ok {
		return nil
	}
	m.lastViewedAt = time.Now()
	return m
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
	// Re-queue immediately so the consumer can retry without waiting for the
	// salvage interval. The message stays in s.messages (no ack), so a
	// subsequent salvage tick will also still see it.
	s.remind(message)
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
