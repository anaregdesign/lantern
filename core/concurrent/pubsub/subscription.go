package pubsub

import (
	"container/heap"
	"context"
	"log/slog"
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
	bufferSize  int
	fullPolicy  FullPolicy

	// expMu guards exp. Kept separate from s.mu so the salvage path can
	// peek/pop heap entries without blocking the consumer's dispatch path
	// (which holds s.mu in write mode to stamp lastViewedAt). Always acquire
	// expMu first and release it before touching s.mu to avoid a lock-order
	// inversion.
	expMu sync.Mutex
	exp   expHeap
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

	// Record the expiry deadline so salvage can find the earliest-expiring
	// message in O(log N) instead of scanning all in-flight messages (#232).
	// Ack leaves the heap entry behind as a tombstone — salvage skips ids
	// that are no longer in s.messages.
	s.expMu.Lock()
	heap.Push(&s.exp, expEntry{id: message.id, deadline: message.createdAt.Add(s.ttl).UnixNano()})
	s.expMu.Unlock()

	// Send outside the locks: a full channel would otherwise block while a
	// lock is held, deadlocking any concurrent ack/message lookup.
	s.enqueue(message)
}

// enqueue routes message.id onto s.ch according to s.fullPolicy. The default
// FullPolicyBlock preserves the historical blocking semantics; the drop
// policies trade strict delivery for liveness and ack the dropped envelope
// so its pool slot and map entry are released (#232).
func (s *Subscription[T]) enqueue(message *Message[T]) {
	switch s.fullPolicy {
	case FullPolicyDropNewest:
		select {
		case s.ch <- message.id:
		default:
			slog.Warn("pubsub: drop newest (channel full)",
				"topic", s.topic.name, "subscription", s.name)
			s.ack(message)
		}
	case FullPolicyDropOldest:
		select {
		case s.ch <- message.id:
		default:
			// Single non-blocking drain + retry. Looping unbounded would
			// let a fast publisher starve the consumer indefinitely.
			select {
			case droppedID := <-s.ch:
				slog.Warn("pubsub: drop oldest (channel full)",
					"topic", s.topic.name, "subscription", s.name)
				if dropped := s.message(droppedID); dropped != nil {
					s.ack(dropped)
				}
				select {
				case s.ch <- message.id:
				default:
					// A concurrent publisher refilled the slot; treat
					// this publish as a newest-drop fallback rather
					// than spinning on the channel.
					slog.Warn("pubsub: drop newest after oldest drop (still full)",
						"topic", s.topic.name, "subscription", s.name)
					s.ack(message)
				}
			default:
				// Channel drained between the full-select and the drain
				// attempt; just send.
				s.ch <- message.id
			}
		}
	default: // FullPolicyBlock
		s.ch <- message.id
	}
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
	if s.fullPolicy == FullPolicyBlock {
		s.ch <- message.id
		return
	}
	// Under a drop policy, never block the salvage goroutine on a full
	// channel: the message stays in s.messages and the next interval tick
	// will retry the remind.
	select {
	case s.ch <- message.id:
	default:
	}
}

func (s *Subscription[T]) salvage(interval time.Duration, ttl time.Duration) {
	now := time.Now()
	nowNano := now.UnixNano()

	// Expiry path: pop every heap entry whose deadline has passed and ack
	// the corresponding live message. Tombstones (entries for already-acked
	// messages) are popped and skipped. Cost is O(K log N) where K is the
	// number of expirations on this tick (#232).
	for {
		s.expMu.Lock()
		if s.exp.Len() == 0 || s.exp[0].deadline > nowNano {
			s.expMu.Unlock()
			break
		}
		entry := heap.Pop(&s.exp).(expEntry)
		s.expMu.Unlock()

		// Look up under s.mu; ack also takes s.mu so we release expMu first
		// to keep lock order (expMu -> s.mu, never the reverse).
		m := s.message(entry.id)
		if m == nil {
			continue
		}
		// Re-verify against the live createdAt: a Nack does not reset the
		// envelope, so the heap entry is still authoritative, but checking
		// keeps us safe if the policy ever changes.
		if now.Sub(m.createdAt) > ttl {
			s.ack(m)
		}
	}

	// Remind path: re-push ids whose consumer has been silent for at least
	// one interval. This still scans the in-flight map because lastViewedAt
	// changes mid-flight and would require an indexable structure to track
	// efficiently; the heap optimisation targets the expiry hot path, which
	// dominates when ttl >> interval (#232).
	s.mu.RLock()
	snapshot := make([]*Message[T], 0, len(s.messages))
	for _, m := range s.messages {
		snapshot = append(snapshot, m)
	}
	s.mu.RUnlock()

	for _, message := range snapshot {
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
