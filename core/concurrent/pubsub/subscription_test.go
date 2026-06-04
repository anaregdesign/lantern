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
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
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
				ctx:      ctx,
				consumer: func(x *Message[int]) { t.Log(x) },
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			go tt.s.Subscribe(tt.args.ctx, tt.args.consumer)
		})
	}
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

// BenchmarkSubscription_PublishConsumeUint64 measures the Publish→consume hot
// path after the #231 throughput refactor (uint64 IDs, sync.Pool envelopes,
// fixed worker pool). Compare against the pre-#231 baseline captured in the
// PR body using benchstat.
func BenchmarkSubscription_PublishConsumeUint64(b *testing.B) {
	topic := NewTopic[int]("bench")
	sub := topic.NewSubscription("s", 4, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var done sync.WaitGroup
	done.Add(b.N)
	go sub.Subscribe(ctx, func(m *Message[int]) {
		m.Ack()
		done.Done()
	})

	// Let workers start before timing.
	time.Sleep(10 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		topic.Publish(i)
	}
	done.Wait()
	b.StopTimer()
}

// BenchmarkSubscription_PublishConsumeUint64Parallel amortizes per-publish
// goroutine spawn (introduced for fan-out safety in #230) across producer
// goroutines, isolating the consumer-side wins from #231 (pool + worker
// pool). This is the bench whose ratio the PR body cites for the ≥ 2x goal.
func BenchmarkSubscription_PublishConsumeUint64Parallel(b *testing.B) {
	topic := NewTopic[int]("bench")
	sub := topic.NewSubscription("s", 4, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var done sync.WaitGroup
	done.Add(b.N)
	go sub.Subscribe(ctx, func(m *Message[int]) {
		m.Ack()
		done.Done()
	})

	time.Sleep(10 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			topic.Publish(i)
			i++
		}
	})
	done.Wait()
	b.StopTimer()
}
