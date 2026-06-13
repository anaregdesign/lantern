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

// TestIlluminateParam_Prefix pins the #604 vertex-prefix kwarg. Unlike the
// closed-set axes, prefix= is free-text: the key is case-insensitive but the
// value is preserved verbatim (it matches vertex keys), it composes with the
// axis kwargs in any order, an omitted prefix leaves the field empty (no
// filter), and an explicit prefix= with no value is rejected.
func TestIlluminateParam_Prefix(t *testing.T) {
	t.Run("parses value verbatim", func(t *testing.T) {
		s, err := NewSource("alice 2 5 prefix=team:")
		if err != nil {
			t.Fatalf("NewSource: %v", err)
		}
		m, err := IlluminateParam(s)
		if err != nil {
			t.Fatalf("IlluminateParam: %v", err)
		}
		if m.Prefix != "team:" {
			t.Fatalf("Prefix = %q, want %q", m.Prefix, "team:")
		}
	})

	t.Run("omitted leaves prefix empty", func(t *testing.T) {
		s, err := NewSource("alice 2 5 algorithm=mst")
		if err != nil {
			t.Fatalf("NewSource: %v", err)
		}
		m, err := IlluminateParam(s)
		if err != nil {
			t.Fatalf("IlluminateParam: %v", err)
		}
		if m.Prefix != "" {
			t.Fatalf("Prefix = %q, want empty", m.Prefix)
		}
	})

	t.Run("key case-insensitive, value case-sensitive", func(t *testing.T) {
		s, err := NewSource("alice 2 5 PREFIX=Users/Alice")
		if err != nil {
			t.Fatalf("NewSource: %v", err)
		}
		m, err := IlluminateParam(s)
		if err != nil {
			t.Fatalf("IlluminateParam: %v", err)
		}
		if m.Prefix != "Users/Alice" {
			t.Fatalf("Prefix = %q, want %q", m.Prefix, "Users/Alice")
		}
	})

	t.Run("composes with axis kwargs in any order", func(t *testing.T) {
		s, err := NewSource("alice 2 5 prefix=users/ algorithm=spt objective=min")
		if err != nil {
			t.Fatalf("NewSource: %v", err)
		}
		m, err := IlluminateParam(s)
		if err != nil {
			t.Fatalf("IlluminateParam: %v", err)
		}
		if m.Prefix != "users/" || m.Algorithm != "spt" || m.Objective != "min" {
			t.Fatalf("got prefix=%q algorithm=%q objective=%q", m.Prefix, m.Algorithm, m.Objective)
		}
	})

	t.Run("empty value rejected", func(t *testing.T) {
		s, err := NewSource("alice 2 5 prefix=")
		if err != nil {
			t.Fatalf("NewSource: %v", err)
		}
		if _, err := IlluminateParam(s); err == nil {
			t.Fatal("IlluminateParam accepted empty prefix=; want error")
		}
	})
}
