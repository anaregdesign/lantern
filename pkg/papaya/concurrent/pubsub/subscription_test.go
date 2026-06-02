package pubsub

import (
	"context"
	"github.com/anaregdesign/lantern/pkg/papaya/model/function"
	"reflect"
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Name(); got != tt.want {
				t.Errorf("Name() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubscription_Subscribe(t *testing.T) {
	ctx, _ := context.WithTimeout(context.Background(), 1*time.Second)
	topic := NewTopic[int]("test")
	sub := topic.NewSubscription("test", 1, time.Minute, time.Minute)
	type args[T any] struct {
		ctx      context.Context
		consumer function.Consumer[*Message[int]]
	}
	type testCase[T any] struct {
		name string
		s    Subscription[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_Subscribe",
			s:    *sub,
			args: args[int]{
				ctx:      ctx,
				consumer: func(x *Message[int]) { t.Log(x) },
			},
		},
	}
	for _, tt := range tests {
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
	for _, tt := range tests {
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
				messages: map[string]*Message[int]{
					"uuid": {id: "uuid", body: 1},
				},
			},
			args: args[int]{message: &Message[int]{id: "uuid", body: 1}},
		},
	}
	for _, tt := range tests {
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
				messages: map[string]*Message[int]{
					"uuid": {id: "uuid", body: 1},
				},
			},
			args: args[int]{message: &Message[int]{id: "uuid", body: 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.nack(tt.args.message)
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
	for _, tt := range tests {
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
				ch:       make(chan string, 10),
				messages: map[string]*Message[int]{},
			},
			args: args[int]{message: &Message[int]{id: "uuid", body: 1}},
		},
	}
	for _, tt := range tests {
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
	for _, tt := range tests {
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
				ch:   make(chan string, 10),
				messages: map[string]*Message[int]{
					"uuid": {id: "uuid", body: 1},
				},
			},
			args: args[int]{message: &Message[int]{id: "uuid", body: 1}},
		},
	}
	for _, tt := range tests {
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
				ch:   make(chan string, 10),
				messages: map[string]*Message[int]{
					"uuid": {id: "uuid", body: 1},
				},
			},
			args: args{interval: time.Second, ttl: time.Second},
		},
	}
	for _, tt := range tests {
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
		s    Subscription[T]
	}
	tests := []testCase[int]{
		{
			name: "TestSubscription_unregister",
			s:    *sub,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.unregister()
		})
	}
}

func TestSubscription_watch(t *testing.T) {
	ctx, _ := context.WithTimeout(context.Background(), time.Second)
	topic := NewTopic[int]("test_topic")
	sub := topic.NewSubscription("test_sub", 8, time.Minute, time.Minute)

	type args struct {
		ctx      context.Context
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
			name: "TestSubscription_watch",
			s:    *sub,
			args: args{
				ctx:      ctx,
				interval: time.Second,
				ttl:      time.Second,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			go tt.s.watch(tt.args.ctx, tt.args.interval, tt.args.ttl)
		})
	}
}
