package pubsub

import (
	"reflect"
	"testing"
	"time"
)

func TestMessage_Body(t *testing.T) {
	type testCase[T any] struct {
		name string
		m    Message[T]
		want T
	}
	tests := []testCase[int]{
		{
			name: "TestMessage_Body",
			m: Message[int]{
				body: 1,
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Body(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Body() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessage_CreatedAt(t *testing.T) {
	now := time.Now()
	type testCase[T any] struct {
		name string
		m    Message[T]
		want time.Time
	}
	tests := []testCase[int]{
		{
			name: "TestMessage_CreatedAt",
			m: Message[int]{
				createdAt: now,
			},
			want: now,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.CreatedAt(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreatedAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessage_ID(t *testing.T) {
	type testCase[T any] struct {
		name string
		m    Message[T]
		want string
	}
	tests := []testCase[int]{
		{
			name: "TestMessage_ID",
			m: Message[int]{
				id: "uuid",
			},
			want: "uuid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.ID(); got != tt.want {
				t.Errorf("ID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMessage_LastViewedAt(t *testing.T) {
	now := time.Now()
	type testCase[T any] struct {
		name string
		m    Message[T]
		want time.Time
	}
	tests := []testCase[int]{
		{
			name: "TestMessage_LastViewedAt",
			m: Message[int]{
				lastViewedAt: now,
			},
			want: now,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.LastViewedAt(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LastViewedAt() = %v, want %v", got, tt.want)
			}
		})
	}
}
