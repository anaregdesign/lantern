package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/model/function"
)

type Subscription[T any] struct {
	mu          sync.RWMutex
	wg          sync.WaitGroup
	started     atomic.Bool
	nextID      atomic.Uint64
	name        string
	topic       *Topic[T]
	ch          chan uint64
	messages    map[uint64]*Message[T]
	pool        sync.Pool
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
//
// Concurrency is enforced by a fixed worker pool of size s.concurrency that
// drains s.ch directly (#231). The prior model of one goroutine per message,
// gated by a sync semaphore, allocated a goroutine and paid a sync.Cond
// wake-up on every Publish; the worker pool amortises both costs to zero.
func (s *Subscription[T]) Subscribe(ctx context.Context, consumer function.Consumer[*Message[T]]) {
	if !s.started.CompareAndSwap(false, true) {
		// Another goroutine already owns the dispatcher for this
		// subscription; the second caller's ch reads would steal messages
		// from the first. Refuse silently and let the first finish.
		return
	}
	defer s.started.Store(false)

	s.register()
	go s.watch(ctx, s.interval, s.ttl)

	workers := s.concurrency
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				select {
				case id := <-s.ch:
					m := s.dispatch(id)
					if m == nil {
						// Already acked (e.g. salvaged past TTL) between publish and lookup.
						continue
					}
					consumer(m)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	<-ctx.Done()
	s.wg.Wait()
	s.unregister()
}

func (s *Subscription[T]) register() {
	s.topic.register(s)
}

func (s *Subscription[T]) unregister() {
	s.topic.unregister(s)
}

// newMessage allocates (or recycles) a *Message[T] for body. Envelopes come
// from s.pool to keep the hot path allocation-free (#231); ack returns them
// after the map delete. The id is a per-Subscription monotonic counter, which
// is sufficient because the messages map is per-subscription.
func (s *Subscription[T]) newMessage(body T) *Message[T] {
	m, _ := s.pool.Get().(*Message[T])
	if m == nil {
		m = &Message[T]{}
	}
	m.id = s.nextID.Add(1)
	m.body = body
	m.subscription = s
	m.createdAt = time.Now()
	m.lastViewedAt = time.Time{}
	return m
}

func (s *Subscription[T]) message(id uint64) *Message[T] {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.messages[id]
}

// dispatch atomically looks up id and stamps lastViewedAt so the salvage path
// can tell whether the message has been recently handed to a consumer. It is
// the only correct way for the Subscribe loop to take a message off s.ch; a
// plain message() read leaves lastViewedAt at the zero value forever, which
// makes salvage re-push the same id every interval tick (#229).
func (s *Subscription[T]) dispatch(id uint64) *Message[T] {
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
	delete(s.messages, message.id)
	s.mu.Unlock()

	// Return the envelope to the pool. The consumer contract on Message.Ack
	// is "do not retain after Ack" (#231); zero the body so a stale
	// reference cannot keep a large T alive.
	var zero T
	message.body = zero
	s.pool.Put(message)
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
