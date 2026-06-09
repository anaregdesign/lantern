package parser

import (
	"testing"
	"time"
)

// TestParam_OmittedTTLIsPermanent pins the opt-in decay contract (#523)
// for the REPL grammar: a write that omits ttl_seconds leaves TTL at its
// zero value (forwarded to the SDK as "permanent"), while an explicit
// ttl_seconds is parsed as whole seconds. The param parsers run after the
// verb/object tokens have been consumed, so each Source carries only the
// argument portion.
func TestParam_OmittedTTLIsPermanent(t *testing.T) {
	t.Run("put vertex", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want time.Duration
		}{
			{name: "omitted is permanent", in: "key value", want: 0},
			{name: "explicit seconds", in: "key value 60", want: 60 * time.Second},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, err := NewSource(tc.in)
				if err != nil {
					t.Fatalf("NewSource(%q): %v", tc.in, err)
				}
				m, err := PutVertexParam(s)
				if err != nil {
					t.Fatalf("PutVertexParam(%q): %v", tc.in, err)
				}
				if m.TTL != tc.want {
					t.Fatalf("PutVertexParam(%q).TTL = %v, want %v", tc.in, m.TTL, tc.want)
				}
			})
		}
	})

	t.Run("put edge", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want time.Duration
		}{
			{name: "omitted is permanent", in: "a b 1.5", want: 0},
			{name: "explicit seconds", in: "a b 1.5 60", want: 60 * time.Second},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, err := NewSource(tc.in)
				if err != nil {
					t.Fatalf("NewSource(%q): %v", tc.in, err)
				}
				m, err := PutEdgeParam(s)
				if err != nil {
					t.Fatalf("PutEdgeParam(%q): %v", tc.in, err)
				}
				if m.TTL != tc.want {
					t.Fatalf("PutEdgeParam(%q).TTL = %v, want %v", tc.in, m.TTL, tc.want)
				}
			})
		}
	})

	t.Run("add edge", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want time.Duration
		}{
			{name: "omitted is permanent", in: "a b 1.5", want: 0},
			{name: "explicit seconds", in: "a b 1.5 60", want: 60 * time.Second},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, err := NewSource(tc.in)
				if err != nil {
					t.Fatalf("NewSource(%q): %v", tc.in, err)
				}
				m, err := AddEdgeParam(s)
				if err != nil {
					t.Fatalf("AddEdgeParam(%q): %v", tc.in, err)
				}
				if m.TTL != tc.want {
					t.Fatalf("AddEdgeParam(%q).TTL = %v, want %v", tc.in, m.TTL, tc.want)
				}
			})
		}
	})
}
