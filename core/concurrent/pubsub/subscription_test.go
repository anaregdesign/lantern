package pubsub

import (
	"context"
	"github.com/anaregdesign/lantern/core/model/function"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscription_Name(t *testing.T) {
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		want string
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_Name",
			s: Subscription[int]{
				name: "test",
			},
			want: "test",
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Name(); got != tt.want {
				t.Errorf("Name() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubscription_Subscribe(t *testing.T) {
	// Use a short, test-scoped context so Subscribe returns promptly,
	// and a WaitGroup so the test waits for the goroutine to exit
	// before t.Run returns. The previous fire-and-forget pattern
	// (`go tt.s.Subscribe(...)` with no synchronisation) leaked the
	// Subscribe goroutine past the test boundary, where its internal
	// worker pool's accesses raced with the testing framework's
	// teardown reads under -race (#397).
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	topic := NewTopic[int]("test")
	sub := topic.NewSubscription("test", 1, time.Minute, time.Minute)
	type args[T any] struct {
		ctx      context.Context
		consumer function.Consumer[*Message[int]]
	}
	type testCase[T any] struct {
		name string
		s    *Subscription[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_Subscribe",
			s:    sub,
			args: args[int]{
				ctx: ctx,
				// Consumer must not call t.Log: the test goroutine
				// may have ticked past tt.s.Subscribe(...) and the
				// testing.T may be in the middle of teardown when a
				// late message arrives.
				consumer: func(*Message[int]) {},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				tt.s.Subscribe(tt.args.ctx, tt.args.consumer)
			}()
			wg.Wait()
		})
	}

	// Regression for #806: watch's ctx.Done path used to call s.wg.Wait(),
	// which has no happens-before to Subscribe's per-worker s.wg.Add(1). An
	// already-cancelled context lands watch on that path while Subscribe is
	// still registering workers, so the Add raced the Wait under the race
	// detector. With the vestigial Wait removed, Subscribe must still return
	// promptly and cleanly. concurrency > 1 widens the worker-registration
	// window the cancelled watch used to race against. Run under -race.
	t.Run("CancelBeforeWorkerRegistration", func(t *testing.T) {
		topic := NewTopic[int]("cancel-race")
		sub := topic.NewSubscription("s", 8, time.Minute, time.Minute)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Done before Subscribe runs: watch takes its ctx.Done path immediately.

		done := make(chan struct{})
		go func() {
			defer close(done)
			sub.Subscribe(ctx, func(*Message[int]) {})
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Subscribe did not return after an already-cancelled ctx")
		}
	})
}

func TestSubscription_Topic(t *testing.T) {
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		want *Topic[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_Topic",
			s: Subscription[int]{
				name:  "test",
				topic: &Topic[int]{name: "test"},
			},
			want: &Topic[int]{name: "test"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Topic(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Topic() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubscription_ack(t *testing.T) {
	type args[T any] struct {
		message *Message[T]
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_ack",
			s: Subscription[int]{
				name: "test",
				messages: map[uint64]*Message[int]{
					1: {id: 1, body: 1},
				},
			},
			args: args[int]{message: &Message[int]{id: 1, body: 1}},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.ack(tt.args.message)
		})
	}
}

func TestSubscription_nack(t *testing.T) {
	type args[T any] struct {
		message *Message[T]
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_nack",
			s: Subscription[int]{
				name: "test",
				ch:   make(chan uint64, 1),
				messages: map[uint64]*Message[int]{
					1: {id: 1, body: 1},
				},
			},
			args: args[int]{message: &Message[int]{id: 1, body: 1}},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.nack(tt.args.message)
			select {
			case id := <-tt.s.ch:
				if id != tt.args.message.id {
					t.Fatalf("nack should requeue id %d, got %d", tt.args.message.id, id)
				}
			default:
				t.Fatal("nack should requeue the message on s.ch")
			}
		})
	}
}

func TestSubscription_newMessage(t *testing.T) {
	type args[T any] struct {
		body T
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args[T]
		want *Message[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_newMessage",
			s: Subscription[int]{
				name: "test",
			},
			args: args[int]{body: 1},
			want: &Message[int]{body: 1},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.newMessage(tt.args.body); got.body != tt.want.body {
				t.Errorf("newMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubscription_publish(t *testing.T) {
	type args[T any] struct {
		message *Message[T]
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_publish",
			s: Subscription[int]{
				name:     "test",
				ch:       make(chan uint64, 10),
				messages: map[uint64]*Message[int]{},
			},
			args: args[int]{message: &Message[int]{id: 1, body: 1}},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.publish(tt.args.message)
		})
	}
}

func TestSubscription_register(t *testing.T) {
	type testCase[T any] struct {
		name string
		s    Subscription[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_register",
			s: Subscription[int]{
				name: "test",
				topic: &Topic[int]{
					name:          "test",
					subscriptions: map[string]*Subscription[int]{},
				},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.register()
		})
	}
}

func TestSubscription_remind(t *testing.T) {
	type args[T any] struct {
		message *Message[T]
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_remind",
			s: Subscription[int]{
				name: "test",
				ch:   make(chan uint64, 10),
				messages: map[uint64]*Message[int]{
					1: {id: 1, body: 1},
				},
			},
			args: args[int]{message: &Message[int]{id: 1, body: 1}},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.remind(tt.args.message)
		})
	}
}

func TestSubscription_salvage(t *testing.T) {
	type args struct {
		interval time.Duration
		ttl      time.Duration
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_salvage",
			s: Subscription[int]{
				name: "test",
				ch:   make(chan uint64, 10),
				messages: map[uint64]*Message[int]{
					1: {id: 1, body: 1},
				},
			},
			args: args{interval: time.Second, ttl: time.Second},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.salvage(tt.args.interval, tt.args.ttl)
		})
	}
}

func TestSubscription_unregister(t *testing.T) {
	topic := NewTopic[int]("test_topic")
	sub := topic.NewSubscription("test_sub", 8, time.Minute, time.Minute)

	type testCase[T any] struct {
		name string
		s    *Subscription[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_unregister",
			s:    sub,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.s.unregister()
		})
	}
}

func TestSubscription_watch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	topic := NewTopic[int]("test_topic")
	sub := topic.NewSubscription("test_sub", 8, time.Minute, time.Minute)

	type args struct {
		ctx      context.Context
		interval time.Duration
		ttl      time.Duration
	}
	type testCase[T any] struct {
		name string
		s    *Subscription[T]
		args args
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_watch",
			s:    sub,
			args: args{
				ctx:      ctx,
				interval: time.Second,
				ttl:      time.Second,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			go tt.s.watch(tt.args.ctx, tt.args.interval, tt.args.ttl)
		})
	}
}

// TestSubscription_SalvageRespectsLastViewedAt is a regression test for #229.
// salvage previously re-published every in-flight message every tick because
// Message.lastViewedAt was never written, so now.Sub(zeroTime) > interval was
// always true. This test exercises the read path (dispatch) directly and then
// invokes salvage() so the assertion is independent of goroutine scheduling.
func TestSubscription_SalvageRespectsLastViewedAt(t *testing.T) {
	topic := NewTopic[int]("t")
	sub := topic.NewSubscription("s", 1, 50*time.Millisecond, time.Hour)

	// Publish without a Subscribe loop: the message is parked in s.messages
	// and its id sits on s.ch. Drain s.ch manually so a later remind() can
	// observe whether salvage re-queued it.
	topic.Publish(7)
	id := <-sub.ch

	// Simulate a consumer pickup: dispatch must stamp lastViewedAt.
	m := sub.dispatch(id)
	if m == nil {
		t.Fatalf("dispatch returned nil for id %d (message missing from map)", id)
	}
	if m.lastViewedAt.IsZero() {
		t.Fatal("dispatch must stamp lastViewedAt; got zero (regression #229)")
	}

	// salvage interval is 50ms; we just dispatched so the message is fresh.
	// Bug: lastViewedAt would be zero, predicate true, message re-queued.
	// Fix: predicate false, ch stays empty.
	sub.salvage(50*time.Millisecond, time.Hour)

	select {
	case stray := <-sub.ch:
		t.Fatalf("salvage re-queued a freshly-viewed message (id=%d); lastViewedAt stamping broken", stray)
	default:
	}
}

// TestSubscription_NackTriggersImmediateRedelivery is a regression test for
// #229. Before the fix, Nack() was a silent no-op so the message would only
// be retried after the salvage interval elapsed (and even then, only because
// of the lastViewedAt bug). The new contract: Nack re-queues immediately.
func TestSubscription_NackTriggersImmediateRedelivery(t *testing.T) {
	topic := NewTopic[int]("t")
	// Long interval — we want to assert that redelivery is driven by Nack,
	// not by the salvage timer.
	sub := topic.NewSubscription("s", 1, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempt atomic.Int64
	done := make(chan struct{})
	go sub.Subscribe(ctx, func(m *Message[int]) {
		n := attempt.Add(1)
		switch n {
		case 1:
			m.Nack()
		case 2:
			m.Ack()
			close(done)
		}
	})

	// Allow Subscribe to start before publishing.
	time.Sleep(20 * time.Millisecond)
	topic.Publish(1)

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Nack did not redeliver within 500ms (attempts=%d)", attempt.Load())
	}
}

// TestSubscription_SubscribeIsSingleFlight is a regression test for #229.
// Two concurrent Subscribe calls on the same *Subscription previously shared
// s.ch / s.wg silently, producing nondeterministic delivery split across two
// consumer functions. The new contract: the second concurrent call is a
// no-op (returns immediately).
func TestSubscription_SubscribeIsSingleFlight(t *testing.T) {
	topic := NewTopic[int]("t")
	sub := topic.NewSubscription("s", 1, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu          sync.Mutex
		consumerHit = map[string]int{"primary": 0, "secondary": 0}
	)
	consumer := func(label string) function.Consumer[*Message[int]] {
		return func(m *Message[int]) {
			mu.Lock()
			consumerHit[label]++
			mu.Unlock()
			m.Ack()
		}
	}

	go sub.Subscribe(ctx, consumer("primary"))
	go sub.Subscribe(ctx, consumer("secondary"))

	// Let both Subscribe calls race to register before publishing.
	time.Sleep(20 * time.Millisecond)

	const N = 20
	for i := 0; i < N; i++ {
		topic.Publish(i)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	total := consumerHit["primary"] + consumerHit["secondary"]
	if total != N {
		t.Fatalf("expected %d total deliveries, got %d (primary=%d secondary=%d)",
			N, total, consumerHit["primary"], consumerHit["secondary"])
	}
	// The second Subscribe call must have observed exactly zero messages.
	if consumerHit["primary"] != 0 && consumerHit["secondary"] != 0 {
		t.Fatalf("expected single-flight Subscribe; both consumers received messages (primary=%d secondary=%d)",
			consumerHit["primary"], consumerHit["secondary"])
	}
}

// --- #232: salvage heap + functional options + full-policy ---

// waitFor polls cond up to d, returning true if it ever becomes true. It
// avoids time.Sleep tuning per test by letting the test fail fast when the
// invariant is violated.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func TestNewSubscriptionWithOptions_Defaults(t *testing.T) {
	topic := NewTopic[int]("opts-defaults")
	sub := topic.NewSubscriptionWithOptions("s")
	if sub.concurrency != 1 {
		t.Errorf("default concurrency = %d, want 1", sub.concurrency)
	}
	if sub.interval != time.Minute {
		t.Errorf("default interval = %v, want 1m", sub.interval)
	}
	if sub.ttl != time.Minute {
		t.Errorf("default ttl = %v, want 1m", sub.ttl)
	}
	if sub.bufferSize != defaultBufferSize {
		t.Errorf("default bufferSize = %d, want %d", sub.bufferSize, defaultBufferSize)
	}
	if sub.fullPolicy != FullPolicyBlock {
		t.Errorf("default fullPolicy = %v, want FullPolicyBlock", sub.fullPolicy)
	}
	if cap(sub.ch) != defaultBufferSize {
		t.Errorf("default channel cap = %d, want %d", cap(sub.ch), defaultBufferSize)
	}
}

func TestNewSubscriptionWithOptions_AllSetters(t *testing.T) {
	topic := NewTopic[int]("opts-all")
	sub := topic.NewSubscriptionWithOptions("s",
		WithConcurrency(4),
		WithSalvageInterval(7*time.Millisecond),
		WithTTL(99*time.Millisecond),
		WithBufferSize(8),
		WithFullPolicy(FullPolicyDropNewest),
	)
	if sub.concurrency != 4 {
		t.Errorf("concurrency = %d, want 4", sub.concurrency)
	}
	if sub.interval != 7*time.Millisecond {
		t.Errorf("interval = %v, want 7ms", sub.interval)
	}
	if sub.ttl != 99*time.Millisecond {
		t.Errorf("ttl = %v, want 99ms", sub.ttl)
	}
	if sub.bufferSize != 8 {
		t.Errorf("bufferSize = %d, want 8", sub.bufferSize)
	}
	if cap(sub.ch) != 8 {
		t.Errorf("channel cap = %d, want 8", cap(sub.ch))
	}
	if sub.fullPolicy != FullPolicyDropNewest {
		t.Errorf("fullPolicy = %v, want FullPolicyDropNewest", sub.fullPolicy)
	}
}

func TestNewSubscriptionWithOptions_BufferSizeClampedToOne(t *testing.T) {
	topic := NewTopic[int]("opts-clamp")
	sub := topic.NewSubscriptionWithOptions("s", WithBufferSize(0))
	if sub.bufferSize != 1 {
		t.Errorf("bufferSize = %d, want 1 (clamped)", sub.bufferSize)
	}
	if cap(sub.ch) != 1 {
		t.Errorf("channel cap = %d, want 1", cap(sub.ch))
	}
}

func TestNewSubscription_PositionalStillWorks(t *testing.T) {
	// The positional NewSubscription must continue to compile and route
	// through the options API with matching defaults so existing callers
	// (notably core/concurrent/pubsub/example) keep working.
	topic := NewTopic[int]("opts-positional")
	sub := topic.NewSubscription("s", 3, 11*time.Millisecond, 22*time.Millisecond)
	if sub.concurrency != 3 || sub.interval != 11*time.Millisecond || sub.ttl != 22*time.Millisecond {
		t.Errorf("positional wrapper did not propagate args: got concurrency=%d interval=%v ttl=%v",
			sub.concurrency, sub.interval, sub.ttl)
	}
	if sub.bufferSize != defaultBufferSize || sub.fullPolicy != FullPolicyBlock {
		t.Errorf("positional wrapper changed defaults: bufferSize=%d fullPolicy=%v",
			sub.bufferSize, sub.fullPolicy)
	}
}

func TestSubscription_SalvageHeapEvictsExpired(t *testing.T) {
	// Publish with short TTL and no consumer; salvage must ack-evict the
	// message and drop it from s.messages (#232 heap path).
	topic := NewTopic[int]("salvage-heap-evict")
	sub := topic.NewSubscriptionWithOptions("s",
		WithSalvageInterval(5*time.Millisecond),
		WithTTL(10*time.Millisecond),
		WithBufferSize(16),
	)
	// Drive publish directly so the message lands in s.messages without
	// needing a Subscribe loop (which would drain it).
	m := sub.newMessage(42)
	sub.publish(m)

	// Trigger salvage manually after TTL elapses; this exercises the heap
	// pop path without racing the watch ticker.
	time.Sleep(15 * time.Millisecond)
	sub.salvage(sub.interval, sub.ttl)

	sub.mu.RLock()
	n := len(sub.messages)
	sub.mu.RUnlock()
	if n != 0 {
		t.Errorf("after salvage: %d messages remain, want 0 (heap eviction failed)", n)
	}
	sub.expMu.Lock()
	heapLen := sub.exp.Len()
	sub.expMu.Unlock()
	if heapLen != 0 {
		t.Errorf("after salvage: heap len %d, want 0 (entry should be popped)", heapLen)
	}
}

func TestSubscription_SalvageHeapTombstonesAckedMessages(t *testing.T) {
	// Publish, ack via the message API, then run salvage. The heap entry is
	// a tombstone (id no longer in s.messages); salvage must skip it
	// cleanly without panicking and must still pop it once its deadline
	// passes (#232 tombstone strategy).
	topic := NewTopic[int]("salvage-heap-tombstone")
	sub := topic.NewSubscriptionWithOptions("s",
		WithSalvageInterval(5*time.Millisecond),
		WithTTL(10*time.Millisecond),
		WithBufferSize(16),
	)
	m := sub.newMessage(1)
	sub.publish(m)
	// Drain the id from the channel so the consumer-side test doesn't see it.
	<-sub.ch
	// Ack from "outside" via the public Message API.
	m.Ack()

	sub.mu.RLock()
	if len(sub.messages) != 0 {
		t.Fatalf("after Ack: %d messages remain, want 0", len(sub.messages))
	}
	sub.mu.RUnlock()
	sub.expMu.Lock()
	if sub.exp.Len() != 1 {
		t.Fatalf("after Ack: heap len %d, want 1 (tombstone)", sub.exp.Len())
	}
	sub.expMu.Unlock()

	// Past the deadline salvage must pop the tombstone.
	time.Sleep(15 * time.Millisecond)
	sub.salvage(sub.interval, sub.ttl)

	sub.expMu.Lock()
	if sub.exp.Len() != 0 {
		t.Errorf("after salvage: heap len %d, want 0 (tombstone not popped)", sub.exp.Len())
	}
	sub.expMu.Unlock()
}

func TestSubscription_SalvageHeapPreservesLiveMessages(t *testing.T) {
	// Salvage must not touch messages whose deadline has not passed.
	topic := NewTopic[int]("salvage-heap-live")
	sub := topic.NewSubscriptionWithOptions("s",
		WithSalvageInterval(5*time.Millisecond),
		WithTTL(time.Hour), // far in the future
		WithBufferSize(16),
	)
	m := sub.newMessage(7)
	sub.publish(m)

	sub.salvage(sub.interval, sub.ttl)

	sub.mu.RLock()
	n := len(sub.messages)
	sub.mu.RUnlock()
	if n != 1 {
		t.Errorf("salvage evicted a live message: %d remain, want 1", n)
	}
	sub.expMu.Lock()
	heapLen := sub.exp.Len()
	sub.expMu.Unlock()
	if heapLen != 1 {
		t.Errorf("salvage modified the heap unexpectedly: len %d, want 1", heapLen)
	}
}

func TestSubscription_FullPolicyBlockIsDefault(t *testing.T) {
	// With the default Block policy and a buffer of 1, a second publish
	// must block until the first id is drained.
	topic := NewTopic[int]("policy-block")
	sub := topic.NewSubscriptionWithOptions("s", WithBufferSize(1))
	sub.publish(sub.newMessage(1))

	done := make(chan struct{})
	go func() {
		sub.publish(sub.newMessage(2))
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("second publish returned with full buffer; Block policy did not block")
	case <-time.After(20 * time.Millisecond):
		// expected: still blocked.
	}

	// Drain one id; the blocked publish must now complete.
	<-sub.ch
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("blocked publish did not unblock after drain")
	}
}

func TestSubscription_FullPolicyDropNewest(t *testing.T) {
	// With DropNewest and a buffer of 1, the second publish must be dropped
	// (acked and pool-returned), leaving exactly one message in flight.
	topic := NewTopic[int]("policy-drop-newest")
	sub := topic.NewSubscriptionWithOptions("s",
		WithBufferSize(1),
		WithFullPolicy(FullPolicyDropNewest),
	)
	sub.publish(sub.newMessage(1))
	// Channel is full; this publish must drop.
	sub.publish(sub.newMessage(2))

	sub.mu.RLock()
	n := len(sub.messages)
	sub.mu.RUnlock()
	if n != 1 {
		t.Errorf("DropNewest: %d messages in flight, want 1", n)
	}
	if len(sub.ch) != 1 {
		t.Errorf("DropNewest: ch len %d, want 1", len(sub.ch))
	}
}

func TestSubscription_FullPolicyDropOldest(t *testing.T) {
	// With DropOldest and a buffer of 1, the second publish must evict the
	// first id from the channel (acking it) and enqueue itself.
	topic := NewTopic[int]("policy-drop-oldest")
	sub := topic.NewSubscriptionWithOptions("s",
		WithBufferSize(1),
		WithFullPolicy(FullPolicyDropOldest),
	)
	first := sub.newMessage(100)
	firstID := first.id
	sub.publish(first)
	second := sub.newMessage(200)
	secondID := second.id
	sub.publish(second)

	if !waitFor(100*time.Millisecond, func() bool {
		return len(sub.ch) == 1
	}) {
		t.Fatalf("DropOldest: ch len = %d, want 1", len(sub.ch))
	}
	queuedID := <-sub.ch
	if queuedID != secondID {
		t.Errorf("DropOldest: queued id = %d, want %d (newest)", queuedID, secondID)
	}
	sub.mu.RLock()
	_, hasFirst := sub.messages[firstID]
	_, hasSecond := sub.messages[secondID]
	sub.mu.RUnlock()
	if hasFirst {
		t.Errorf("DropOldest: oldest message still in s.messages")
	}
	if !hasSecond {
		t.Errorf("DropOldest: newest message missing from s.messages")
	}
}

// recordingObserver captures Observer invocations for assertion in tests.
// All fields are read after the system under test has quiesced so a plain
// mutex (not atomic) keeps the test code readable.
type recordingObserver struct {
	mu         sync.Mutex
	depths     []int
	drops      []string
	dispatches []time.Duration
}

func (r *recordingObserver) RecordEnqueueDepth(d int) {
	r.mu.Lock()
	r.depths = append(r.depths, d)
	r.mu.Unlock()
}

func (r *recordingObserver) RecordDrop(policy string) {
	r.mu.Lock()
	r.drops = append(r.drops, policy)
	r.mu.Unlock()
}

func (r *recordingObserver) ObserveDispatch(d time.Duration) {
	r.mu.Lock()
	r.dispatches = append(r.dispatches, d)
	r.mu.Unlock()
}

func (r *recordingObserver) snapshot() (depths []int, drops []string, dispatches []time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	depths = append(depths, r.depths...)
	drops = append(drops, r.drops...)
	dispatches = append(dispatches, r.dispatches...)
	return
}

func TestSubscription_Observer_RecordEnqueueDepthOnPublish(t *testing.T) {
	// Each successful publish must produce exactly one depth sample equal
	// to the channel length immediately after the send (#240).
	obs := &recordingObserver{}
	topic := NewTopic[int]("obs-depth")
	sub := topic.NewSubscriptionWithOptions("s",
		WithBufferSize(4),
		WithObserver(obs),
	)
	sub.publish(sub.newMessage(1))
	sub.publish(sub.newMessage(2))
	sub.publish(sub.newMessage(3))

	depths, drops, _ := obs.snapshot()
	if len(drops) != 0 {
		t.Errorf("unexpected drops on uncongested publish: %v", drops)
	}
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(depths, want) {
		t.Errorf("depths = %v, want %v", depths, want)
	}
}

func TestSubscription_Observer_RecordDropNewest(t *testing.T) {
	obs := &recordingObserver{}
	topic := NewTopic[int]("obs-drop-newest")
	sub := topic.NewSubscriptionWithOptions("s",
		WithBufferSize(1),
		WithFullPolicy(FullPolicyDropNewest),
		WithObserver(obs),
	)
	sub.publish(sub.newMessage(1)) // fills channel
	sub.publish(sub.newMessage(2)) // dropped

	_, drops, _ := obs.snapshot()
	if !reflect.DeepEqual(drops, []string{DropPolicyNewest}) {
		t.Errorf("drops = %v, want [%s]", drops, DropPolicyNewest)
	}
}

func TestSubscription_Observer_RecordDropOldest(t *testing.T) {
	obs := &recordingObserver{}
	topic := NewTopic[int]("obs-drop-oldest")
	sub := topic.NewSubscriptionWithOptions("s",
		WithBufferSize(1),
		WithFullPolicy(FullPolicyDropOldest),
		WithObserver(obs),
	)
	sub.publish(sub.newMessage(1)) // fills channel
	sub.publish(sub.newMessage(2)) // evicts id=1, enqueues id=2

	_, drops, _ := obs.snapshot()
	if !reflect.DeepEqual(drops, []string{DropPolicyOldest}) {
		t.Errorf("drops = %v, want [%s]", drops, DropPolicyOldest)
	}
}

func TestSubscription_Observer_ObserveDispatchOnConsumerReturn(t *testing.T) {
	// Subscribe drains the channel and the worker must call
	// ObserveDispatch after the consumer returns, with a duration
	// measured from the originating Publish (#240).
	obs := &recordingObserver{}
	topic := NewTopic[int]("obs-dispatch")
	sub := topic.NewSubscriptionWithOptions("s",
		WithBufferSize(4),
		WithObserver(obs),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)
	go sub.Subscribe(ctx, func(m *Message[int]) {
		defer wg.Done()
		m.Ack()
	})

	sub.publish(sub.newMessage(1))
	sub.publish(sub.newMessage(2))
	sub.publish(sub.newMessage(3))

	wg.Wait()
	// Give the worker a moment to run the post-consumer Observe call.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, _, dispatches := obs.snapshot()
		if len(dispatches) >= 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	_, _, dispatches := obs.snapshot()
	if len(dispatches) != 3 {
		t.Fatalf("dispatch observations = %d, want 3", len(dispatches))
	}
	for i, d := range dispatches {
		if d < 0 {
			t.Errorf("dispatch[%d] = %v, want >= 0", i, d)
		}
	}
}

func TestWithObserver_NilRestoresNoop(t *testing.T) {
	// WithObserver(nil) must not leave the subscription with a nil
	// observer (which would NPE in enqueue). It must restore the
	// internal no-op sentinel.
	topic := NewTopic[int]("obs-nil")
	sub := topic.NewSubscriptionWithOptions("s", WithObserver(nil))
	if sub.observer == nil {
		t.Fatal("observer is nil; WithObserver(nil) should restore noopObserver{}")
	}
	if _, ok := sub.observer.(noopObserver); !ok {
		t.Errorf("observer type = %T, want noopObserver", sub.observer)
	}
}
