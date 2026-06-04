package pubsub

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewTopic(t *testing.T) {
	type args struct {
		name string
	}
	type testCase[T any] struct {
		name string
		args args
		want *Topic[T]
	}
	tests := []testCase[int]{
		{
			name: "TestNewTopic",
			args: args{
				name: "test",
			},
			want: &Topic[int]{
				name:          "test",
				subscriptions: make(map[string]*Subscription[int]),
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := NewTopic[int](tt.args.name); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewTopic() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTopic_Name(t1 *testing.T) {
	type testCase[T any] struct {
		name string
		t    Topic[T]
		want string
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_Name",
			t:    *NewTopic[int]("test"),
			want: "test",
		},
	}
	for i := range tests {
		tt := &tests[i]
		t1.Run(tt.name, func(t1 *testing.T) {
			if got := tt.t.Name(); got != tt.want {
				t1.Errorf("Name() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTopic_NewSubscription(t1 *testing.T) {
	topic := NewTopic[int]("test")
	type args struct {
		name        string
		concurrency int
		interval    time.Duration
		ttl         time.Duration
	}
	type testCase[T any] struct {
		name string
		t    *Topic[T]
		args args
		want *Subscription[T]
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_NewSubscription",
			t:    topic,
			args: args{
				name:        "test",
				concurrency: 8,
				interval:    time.Minute,
				ttl:         time.Minute,
			},
			want: &Subscription[int]{
				name:        "test",
				concurrency: 8,
				ch:          make(chan uint64, 65536),
				messages:    make(map[uint64]*Message[int]),
				interval:    time.Minute,
				ttl:         time.Minute,
				topic:       topic,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t1.Run(tt.name, func(t1 *testing.T) {
			if got := tt.t.NewSubscription(tt.args.name, tt.args.concurrency, tt.args.interval, tt.args.ttl); got.name != tt.want.name {
				t1.Errorf("NewSubscription() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTopic_Publish(t1 *testing.T) {
	type args[T any] struct {
		body T
	}
	type testCase[T any] struct {
		name string
		t    Topic[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_Publish",
			t:    *NewTopic[int]("test"),
			args: args[int]{
				body: 1,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t1.Run(tt.name, func(t1 *testing.T) {
			tt.t.Publish(tt.args.body)
		})
	}
}

func TestTopic_Subscriptions(t1 *testing.T) {
	type testCase[T any] struct {
		name string
		t    Topic[T]
		want map[string]*Subscription[T]
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_Subscriptions",
			t:    *NewTopic[int]("test"),
			want: map[string]*Subscription[int]{},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t1.Run(tt.name, func(t1 *testing.T) {
			if got := tt.t.Subscriptions(); !reflect.DeepEqual(got, tt.want) {
				t1.Errorf("Subscriptions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTopic_register(t1 *testing.T) {
	type args[T any] struct {
		subscription *Subscription[T]
	}
	type testCase[T any] struct {
		name string
		t    Topic[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_register",
			t:    *NewTopic[int]("test"),
			args: args[int]{
				subscription: &Subscription[int]{
					name:        "test",
					concurrency: 8,
					interval:    time.Minute,
					ttl:         time.Minute,
					topic:       &Topic[int]{name: "test"},
				},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t1.Run(tt.name, func(t1 *testing.T) {
			tt.t.register(tt.args.subscription)
		})
	}
}

func TestTopic_unregister(t1 *testing.T) {
	type args[T any] struct {
		subscription *Subscription[T]
	}
	type testCase[T any] struct {
		name string
		t    Topic[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_unregister",
			t:    *NewTopic[int]("test"),
			args: args[int]{
				subscription: &Subscription[int]{
					name:        "test",
					concurrency: 8,
					interval:    time.Minute,
					ttl:         time.Minute,
					topic:       &Topic[int]{name: "test"},
				},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t1.Run(tt.name, func(t1 *testing.T) {
			tt.t.unregister(tt.args.subscription)
		})
	}
}

// TestTopic_PublishFansOutWithoutBlocking pins #230: a slow / saturated
// subscriber must not block delivery to its peers. We construct two
// subscriptions with a single-slot channel each, fill subA's channel so any
// further send to it would block, then assert subB still receives within a
// short deadline. On main (sequential Publish) subA's blocked send hangs
// Publish forever and this test times out.
func TestTopic_PublishFansOutWithoutBlocking(t *testing.T) {
	topic := NewTopic[int]("fanout")

	subA := &Subscription[int]{
		name:     "a",
		topic:    topic,
		ch:       make(chan uint64, 1),
		messages: make(map[uint64]*Message[int]),
	}
	subB := &Subscription[int]{
		name:     "b",
		topic:    topic,
		ch:       make(chan uint64, 1),
		messages: make(map[uint64]*Message[int]),
	}
	topic.register(subA)
	topic.register(subB)

	// Saturate subA so any further send would block forever.
	subA.ch <- 0

	topic.Publish(42)

	select {
	case <-subB.ch:
		// expected: subB received despite subA being blocked.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked on saturated subA; subB did not receive within 500ms")
	}
}

// TestTopic_PublishConcurrentSafe drives N concurrent Publish callers against
// a single subscriber and asserts every message is delivered exactly once with
// no deadlock. Guards against races introduced by the goroutine-per-Publish
// fan-out (#230).
func TestTopic_PublishConcurrentSafe(t *testing.T) {
	const N = 100

	topic := NewTopic[int]("concurrent")
	sub := topic.NewSubscription("c", 8, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var received atomic.Int64
	done := make(chan struct{})
	go sub.Subscribe(ctx, func(m *Message[int]) {
		m.Ack()
		if received.Add(1) == int64(N) {
			close(done)
		}
	})

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			topic.Publish(v)
		}(i)
	}
	wg.Wait()

	select {
	case <-done:
		// good: N messages delivered.
	case <-time.After(2 * time.Second):
		t.Fatalf("only %d/%d messages delivered before timeout", received.Load(), N)
	}
}
