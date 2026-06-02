package pubsub

import (
	"reflect"
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
		t    Topic[T]
		args args
		want *Subscription[T]
	}
	tests := []testCase[int]{
		{
			name: "TestTopic_NewSubscription",
			t:    *topic,
			args: args{
				name:        "test",
				concurrency: 8,
				interval:    time.Minute,
				ttl:         time.Minute,
			},
			want: &Subscription[int]{
				name:        "test",
				concurrency: 8,
				ch:          make(chan string, 65536),
				messages:    make(map[string]*Message[int]),
				interval:    time.Minute,
				ttl:         time.Minute,
				topic:       topic,
			},
		},
	}
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			tt.t.unregister(tt.args.subscription)
		})
	}
}
