package parser

import (
	"testing"
	"time"
)

// TestKeysParam pins `keys <prefix> [limit]` (#674): a prefix is required and
// the trailing limit is optional (0 when omitted), mirroring ScanVerticesParam.
func TestKeysParam(t *testing.T) {
	t.Run("PrefixOnly", func(t *testing.T) {
		s, err := NewSource("user:")
		if err != nil {
			t.Fatalf("NewSource: %v", err)
		}
		m, err := KeysParam(s)
		if err != nil {
			t.Fatalf("KeysParam: %v", err)
		}
		if m.Prefix != "user:" || m.Limit != 0 {
			t.Errorf("got {%q, %d}, want {user:, 0}", m.Prefix, m.Limit)
		}
	})
	t.Run("PrefixAndLimit", func(t *testing.T) {
		s, _ := NewSource("user: 50")
		m, err := KeysParam(s)
		if err != nil {
			t.Fatalf("KeysParam: %v", err)
		}
		if m.Prefix != "user:" || m.Limit != 50 {
			t.Errorf("got {%q, %d}, want {user:, 50}", m.Prefix, m.Limit)
		}
	})
	t.Run("MissingPrefixIsError", func(t *testing.T) {
		s, _ := NewSource("")
		if _, err := KeysParam(s); err == nil {
			t.Errorf("KeysParam(empty) = nil, want error (prefix required)")
		}
	})
	t.Run("NonNumericLimitIsError", func(t *testing.T) {
		s, _ := NewSource("user: notanint")
		if _, err := KeysParam(s); err == nil {
			t.Errorf("KeysParam(bad limit) = nil, want error")
		}
	})
}

// TestDeleteParam_Batch pins the #679 variadic batch-delete grammar: one or
// more keys for `delete vertex`, and one or more (tail,head) pairs for
// `delete edge` (an even, non-zero token count). Zero targets and odd edge
// token counts are errors.
func TestDeleteParam_Batch(t *testing.T) {
	t.Run("vertex single", func(t *testing.T) {
		s, _ := NewSource("alice")
		m, err := DeleteVertexParam(s)
		if err != nil {
			t.Fatalf("DeleteVertexParam: %v", err)
		}
		if len(m.Keys) != 1 || m.Keys[0] != "alice" {
			t.Errorf("Keys = %v, want [alice]", m.Keys)
		}
	})
	t.Run("vertex batch", func(t *testing.T) {
		s, _ := NewSource("alice bob carol")
		m, err := DeleteVertexParam(s)
		if err != nil {
			t.Fatalf("DeleteVertexParam: %v", err)
		}
		if len(m.Keys) != 3 {
			t.Errorf("Keys = %v, want 3 keys", m.Keys)
		}
	})
	t.Run("vertex zero is error", func(t *testing.T) {
		s, _ := NewSource("")
		if _, err := DeleteVertexParam(s); err == nil {
			t.Errorf("DeleteVertexParam(empty) = nil, want error")
		}
	})
	t.Run("edge single pair", func(t *testing.T) {
		s, _ := NewSource("a b")
		m, err := DeleteEdgeParam(s)
		if err != nil {
			t.Fatalf("DeleteEdgeParam: %v", err)
		}
		if len(m.Pairs) != 1 || m.Pairs[0].Tail != "a" || m.Pairs[0].Head != "b" {
			t.Errorf("Pairs = %v, want [{a b}]", m.Pairs)
		}
	})
	t.Run("edge batch pairs", func(t *testing.T) {
		s, _ := NewSource("a b c d")
		m, err := DeleteEdgeParam(s)
		if err != nil {
			t.Fatalf("DeleteEdgeParam: %v", err)
		}
		if len(m.Pairs) != 2 || m.Pairs[1].Tail != "c" || m.Pairs[1].Head != "d" {
			t.Errorf("Pairs = %v, want 2 pairs", m.Pairs)
		}
	})
	t.Run("edge odd token count is error", func(t *testing.T) {
		s, _ := NewSource("a b c")
		if _, err := DeleteEdgeParam(s); err == nil {
			t.Errorf("DeleteEdgeParam(odd) = nil, want error")
		}
	})
}

// TestScanParam_Kwargs pins the #679 scan paging kwargs: an optional
// positional limit followed by all=<bool> (vertices) and head=<prefix> +
// all=<bool> (edges).
func TestScanParam_Kwargs(t *testing.T) {
	t.Run("vertices limit and all", func(t *testing.T) {
		s, _ := NewSource("users/ 100 all=true")
		m, err := ScanVerticesParam(s)
		if err != nil {
			t.Fatalf("ScanVerticesParam: %v", err)
		}
		if m.Prefix != "users/" || m.Limit != 100 || !m.All {
			t.Errorf("got {%q, %d, %v}", m.Prefix, m.Limit, m.All)
		}
	})
	t.Run("vertices unknown kwarg is error", func(t *testing.T) {
		s, _ := NewSource("users/ bogus=1")
		if _, err := ScanVerticesParam(s); err == nil {
			t.Errorf("ScanVerticesParam(bogus) = nil, want error")
		}
	})
	t.Run("vertices non-bool all is error", func(t *testing.T) {
		s, _ := NewSource("users/ all=maybe")
		if _, err := ScanVerticesParam(s); err == nil {
			t.Errorf("ScanVerticesParam(all=maybe) = nil, want error")
		}
	})
	t.Run("edges head and all", func(t *testing.T) {
		s, _ := NewSource("alice 50 head=post: all=true")
		m, err := ScanEdgesParam(s)
		if err != nil {
			t.Fatalf("ScanEdgesParam: %v", err)
		}
		if m.TailPrefix != "alice" || m.Limit != 50 || m.HeadPrefix != "post:" || !m.All {
			t.Errorf("got {%q, %d, %q, %v}", m.TailPrefix, m.Limit, m.HeadPrefix, m.All)
		}
	})
	t.Run("edges empty head is error", func(t *testing.T) {
		s, _ := NewSource("alice head=")
		if _, err := ScanEdgesParam(s); err == nil {
			t.Errorf("ScanEdgesParam(head=) = nil, want error")
		}
	})
}

// TestCountVerticesParam pins `count vertices <prefix>` — exactly one prefix
// and nothing else (#679).
func TestCountVerticesParam(t *testing.T) {
	t.Run("prefix", func(t *testing.T) {
		s, _ := NewSource("users/")
		m, err := CountVerticesParam(s)
		if err != nil {
			t.Fatalf("CountVerticesParam: %v", err)
		}
		if m.Prefix != "users/" {
			t.Errorf("Prefix = %q, want users/", m.Prefix)
		}
	})
	t.Run("missing prefix is error", func(t *testing.T) {
		s, _ := NewSource("")
		if _, err := CountVerticesParam(s); err == nil {
			t.Errorf("CountVerticesParam(empty) = nil, want error")
		}
	})
	t.Run("trailing token is error", func(t *testing.T) {
		s, _ := NewSource("users/ extra")
		if _, err := CountVerticesParam(s); err == nil {
			t.Errorf("CountVerticesParam(extra) = nil, want error")
		}
	})
}

// TestDeletePrefixVerticesParam pins the #679 destructive-op safety gate:
// exactly one of confirm=yes / dry_run=true is required.
func TestDeletePrefixVerticesParam(t *testing.T) {
	t.Run("confirm yes", func(t *testing.T) {
		s, _ := NewSource("tmp/ confirm=yes")
		m, err := DeletePrefixVerticesParam(s)
		if err != nil {
			t.Fatalf("DeletePrefixVerticesParam: %v", err)
		}
		if m.Prefix != "tmp/" || !m.Confirm || m.DryRun {
			t.Errorf("got {%q confirm=%v dry=%v}", m.Prefix, m.Confirm, m.DryRun)
		}
	})
	t.Run("dry_run with limit", func(t *testing.T) {
		s, _ := NewSource("tmp/ dry_run=true limit=500")
		m, err := DeletePrefixVerticesParam(s)
		if err != nil {
			t.Fatalf("DeletePrefixVerticesParam: %v", err)
		}
		if !m.DryRun || m.Limit != 500 {
			t.Errorf("got dry=%v limit=%d", m.DryRun, m.Limit)
		}
	})
	t.Run("no gate is error", func(t *testing.T) {
		s, _ := NewSource("tmp/")
		if _, err := DeletePrefixVerticesParam(s); err == nil {
			t.Errorf("DeletePrefixVerticesParam(no gate) = nil, want error")
		}
	})
	t.Run("both gates is error", func(t *testing.T) {
		s, _ := NewSource("tmp/ confirm=yes dry_run=true")
		if _, err := DeletePrefixVerticesParam(s); err == nil {
			t.Errorf("DeletePrefixVerticesParam(both) = nil, want error")
		}
	})
	t.Run("confirm not yes is error", func(t *testing.T) {
		s, _ := NewSource("tmp/ confirm=no")
		if _, err := DeletePrefixVerticesParam(s); err == nil {
			t.Errorf("DeletePrefixVerticesParam(confirm=no) = nil, want error")
		}
	})
}

// TestPutVertexParam_Type pins the #679 type= value-type override migrated
// from `vertex put --value-type`: auto by default, an explicit type forces
// coercion (and rejects a mismatch), json re-encodes objects to a compact
// string, and ttl_seconds composes with type= in either order.
func TestPutVertexParam_Type(t *testing.T) {
	t.Run("auto by default", func(t *testing.T) {
		s, _ := NewSource("k 123")
		m, err := PutVertexParam(s)
		if err != nil {
			t.Fatalf("PutVertexParam: %v", err)
		}
		if m.Value != 123 {
			t.Errorf("Value = %v (%T), want int 123", m.Value, m.Value)
		}
	})
	t.Run("type=string forces string", func(t *testing.T) {
		s, _ := NewSource("k 123 type=string")
		m, err := PutVertexParam(s)
		if err != nil {
			t.Fatalf("PutVertexParam: %v", err)
		}
		if m.Value != "123" {
			t.Errorf("Value = %v (%T), want string 123", m.Value, m.Value)
		}
	})
	t.Run("type=json re-encodes objects to a compact string", func(t *testing.T) {
		s, _ := NewSource(`k '{"a":1}' type=json`)
		m, err := PutVertexParam(s)
		if err != nil {
			t.Fatalf("PutVertexParam: %v", err)
		}
		if m.Value != `{"a":1}` {
			t.Errorf("Value = %v, want compact json string", m.Value)
		}
	})
	t.Run("ttl and type compose in either order", func(t *testing.T) {
		s, _ := NewSource("k v 60 type=string")
		m, err := PutVertexParam(s)
		if err != nil {
			t.Fatalf("PutVertexParam: %v", err)
		}
		if m.TTL != 60*time.Second || m.Value != "v" {
			t.Errorf("got ttl=%v value=%v", m.TTL, m.Value)
		}
	})
	t.Run("unknown type is error", func(t *testing.T) {
		s, _ := NewSource("k v type=bogus")
		if _, err := PutVertexParam(s); err == nil {
			t.Errorf("type=bogus accepted, want error")
		}
	})
	t.Run("unknown keyword is error", func(t *testing.T) {
		s, _ := NewSource("k v foo=bar")
		if _, err := PutVertexParam(s); err == nil {
			t.Errorf("foo=bar accepted, want error")
		}
	})
}

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
