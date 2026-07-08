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

// TestEdgeParam_TrailingToken pins the #932 regression: Duration must advance
// the source exactly once, so a token after ttl_seconds is rejected by the
// final EOF check instead of being silently discarded. Mirrors the
// TestCountVerticesParam "trailing token is error" case.
func TestEdgeParam_TrailingToken(t *testing.T) {
	t.Run("put edge", func(t *testing.T) {
		t.Run("valid ttl parses", func(t *testing.T) {
			s, _ := NewSource("a b 1.5 60")
			m, err := PutEdgeParam(s)
			if err != nil {
				t.Fatalf("PutEdgeParam(a b 1.5 60): %v", err)
			}
			if m.TTL != 60*time.Second {
				t.Errorf("TTL = %v, want 1m0s", m.TTL)
			}
		})
		t.Run("trailing token is error", func(t *testing.T) {
			s, _ := NewSource("a b 1.5 60 extra")
			if _, err := PutEdgeParam(s); err == nil {
				t.Errorf("PutEdgeParam(a b 1.5 60 extra) = nil, want error")
			}
		})
	})

	t.Run("add edge", func(t *testing.T) {
		t.Run("valid ttl parses", func(t *testing.T) {
			s, _ := NewSource("a b 1.5 60")
			m, err := AddEdgeParam(s)
			if err != nil {
				t.Fatalf("AddEdgeParam(a b 1.5 60): %v", err)
			}
			if m.TTL != 60*time.Second {
				t.Errorf("TTL = %v, want 1m0s", m.TTL)
			}
		})
		t.Run("trailing token is error", func(t *testing.T) {
			s, _ := NewSource("a b 1.5 60 extra")
			if _, err := AddEdgeParam(s); err == nil {
				t.Errorf("AddEdgeParam(a b 1.5 60 extra) = nil, want error")
			}
		})
	})
}

// TestAddDecayingEdgeParam pins the six-positional decay grammar (#952):
// tail, head, initial_weight, ratio, steps, interval_seconds — all required —
// plus the trailing-token EOF guard. Numeric-range validation (ratio in
// (0,1), steps ≤ MaxDecaySteps) is the SDK's job, so out-of-range values parse
// cleanly here.
func TestAddDecayingEdgeParam(t *testing.T) {
	t.Run("full form parses", func(t *testing.T) {
		s, _ := NewSource("a b 16 0.5 5 1")
		m, err := AddDecayingEdgeParam(s)
		if err != nil {
			t.Fatalf("AddDecayingEdgeParam(a b 16 0.5 5 1): %v", err)
		}
		if m.Tail != "a" || m.Head != "b" {
			t.Fatalf("endpoints = (%q,%q), want (a,b)", m.Tail, m.Head)
		}
		if m.InitialWeight != 16 || m.Ratio != 0.5 || m.Steps != 5 {
			t.Fatalf("got initial=%v ratio=%v steps=%d, want 16 0.5 5", m.InitialWeight, m.Ratio, m.Steps)
		}
		if m.Interval != time.Second {
			t.Fatalf("interval = %v, want 1s", m.Interval)
		}
	})

	t.Run("out-of-range values still parse (SDK validates)", func(t *testing.T) {
		s, _ := NewSource("a b 16 2 99 1")
		if _, err := AddDecayingEdgeParam(s); err != nil {
			t.Fatalf("parser must defer range checks to the SDK, got %v", err)
		}
	})

	t.Run("missing or trailing tokens are errors", func(t *testing.T) {
		for _, in := range []string{
			"a b 16 0.5 5",         // missing interval
			"a b 16 0.5",           // missing steps + interval
			"a b",                  // missing all numerics
			"a b 16 0.5 5 1 extra", // trailing token
		} {
			s, _ := NewSource(in)
			if _, err := AddDecayingEdgeParam(s); err == nil {
				t.Errorf("AddDecayingEdgeParam(%q) = nil, want error", in)
			}
		}
	})
}

// TestBfsParam covers the bfs family grammar (#975): only <seed> is required
// (defaults step=5, fan_out=3, reduction=none, objective=max, weighting=raw),
// step/fan_out are optional positional ints that may also be given as kwargs,
// the closed-set axes and the free-text prefix compose in any order, and
// unknown keys / a third bare positional / an empty prefix= are rejected.
func TestBfsParam(t *testing.T) {
	t.Run("bare seed uses defaults", func(t *testing.T) {
		s, _ := NewSource("alice")
		m, err := BfsParam(s)
		if err != nil {
			t.Fatalf("BfsParam: %v", err)
		}
		if m.Seed != "alice" || m.Step != 5 || m.FanOut != 3 {
			t.Fatalf("got seed=%q step=%d fan_out=%d, want alice/5/3", m.Seed, m.Step, m.FanOut)
		}
		if m.Reduction != "none" || m.Objective != "max" || m.Weighting != "raw" {
			t.Fatalf("got reduction=%q objective=%q weighting=%q, want none/max/raw", m.Reduction, m.Objective, m.Weighting)
		}
	})

	t.Run("positional step and fan_out", func(t *testing.T) {
		s, _ := NewSource("alice 2 7")
		m, err := BfsParam(s)
		if err != nil {
			t.Fatalf("BfsParam: %v", err)
		}
		if m.Step != 2 || m.FanOut != 7 {
			t.Fatalf("got step=%d fan_out=%d, want 2/7", m.Step, m.FanOut)
		}
	})

	t.Run("step and fan_out as kwargs", func(t *testing.T) {
		s, _ := NewSource("alice fan_out=9 step=4")
		m, err := BfsParam(s)
		if err != nil {
			t.Fatalf("BfsParam: %v", err)
		}
		if m.Step != 4 || m.FanOut != 9 {
			t.Fatalf("got step=%d fan_out=%d, want 4/9", m.Step, m.FanOut)
		}
	})

	t.Run("reduction/objective/weighting compose in any order", func(t *testing.T) {
		s, _ := NewSource("alice 3 5 weighting=tfidf objective=min reduction=spt")
		m, err := BfsParam(s)
		if err != nil {
			t.Fatalf("BfsParam: %v", err)
		}
		if m.Reduction != "spt" || m.Objective != "min" || m.Weighting != "tfidf" {
			t.Fatalf("got reduction=%q objective=%q weighting=%q, want spt/min/tfidf", m.Reduction, m.Objective, m.Weighting)
		}
	})

	t.Run("prefix key case-insensitive, value verbatim", func(t *testing.T) {
		s, _ := NewSource("alice PREFIX=Users/Alice")
		m, err := BfsParam(s)
		if err != nil {
			t.Fatalf("BfsParam: %v", err)
		}
		if m.Prefix != "Users/Alice" {
			t.Fatalf("Prefix = %q, want %q", m.Prefix, "Users/Alice")
		}
	})

	t.Run("rejects unknown keys, empty prefix, third positional, non-int", func(t *testing.T) {
		for _, in := range []string{
			"alice algorithm=ppr", // the family is the verb now; no algorithm kwarg
			"alice reduction=bogus",
			"alice objective=bogus",
			"alice weighting=bogus",
			"alice prefix=",
			"alice 1 2 3",    // third bare positional
			"alice notanint", // non-integer step
			"alice top_n=5",  // pagerank kwarg not valid on bfs
		} {
			s, _ := NewSource(in)
			if _, err := BfsParam(s); err == nil {
				t.Errorf("BfsParam(%q) = nil, want error", in)
			}
		}
	})
}

// TestPagerankParam covers the pagerank family grammar (#975): only <seed> is
// required (default top_n=10, restart_prob/epsilon 0 → server α=0.15 / ε=1e-4),
// top_n is an optional positional or kwarg, restart_prob/epsilon parse as
// floats (incl. scientific notation), and reduction/objective (which pagerank
// has no meaning for) plus non-numeric knobs are rejected.
func TestPagerankParam(t *testing.T) {
	t.Run("bare seed uses defaults", func(t *testing.T) {
		s, _ := NewSource("alice")
		m, err := PagerankParam(s)
		if err != nil {
			t.Fatalf("PagerankParam: %v", err)
		}
		if m.Seed != "alice" || m.TopN != 10 {
			t.Fatalf("got seed=%q top_n=%d, want alice/10", m.Seed, m.TopN)
		}
		if m.RestartProb != 0 || m.Epsilon != 0 || m.Weighting != "raw" {
			t.Fatalf("got restart_prob=%v epsilon=%v weighting=%q, want 0/0/raw", m.RestartProb, m.Epsilon, m.Weighting)
		}
	})

	t.Run("positional top_n", func(t *testing.T) {
		s, _ := NewSource("alice 25")
		m, err := PagerankParam(s)
		if err != nil {
			t.Fatalf("PagerankParam: %v", err)
		}
		if m.TopN != 25 {
			t.Fatalf("TopN = %d, want 25", m.TopN)
		}
	})

	t.Run("top_n/restart_prob/epsilon in any order", func(t *testing.T) {
		s, _ := NewSource("alice epsilon=0.001 restart_prob=0.25 top_n=15")
		m, err := PagerankParam(s)
		if err != nil {
			t.Fatalf("PagerankParam: %v", err)
		}
		if m.TopN != 15 || m.RestartProb != 0.25 || m.Epsilon != 0.001 {
			t.Fatalf("got top_n=%d restart_prob=%v epsilon=%v, want 15/0.25/0.001", m.TopN, m.RestartProb, m.Epsilon)
		}
	})

	t.Run("scientific-notation epsilon", func(t *testing.T) {
		s, _ := NewSource("alice epsilon=1e-4")
		m, err := PagerankParam(s)
		if err != nil {
			t.Fatalf("PagerankParam: %v", err)
		}
		if m.Epsilon != 1e-4 {
			t.Fatalf("Epsilon = %v, want 1e-4", m.Epsilon)
		}
	})

	t.Run("rejects reduction/objective and non-numeric knobs", func(t *testing.T) {
		for _, in := range []string{
			"alice reduction=mst", // pagerank has no reduction
			"alice objective=max", // pagerank has no objective
			"alice restart_prob=high",
			"alice epsilon=tiny",
			"alice prefix=",
			"alice 1 2", // second bare positional
		} {
			s, _ := NewSource(in)
			if _, err := PagerankParam(s); err == nil {
				t.Errorf("PagerankParam(%q) = nil, want error", in)
			}
		}
	})
}

// TestCommunityParam covers the community family grammar (#975): only <seed> is
// required (default max_size=0 = the sweep decides, reduction=none,
// objective=max), max_size is an optional positional or kwarg, restart_prob/
// epsilon and the reduction/objective tree-view axes compose in any order, and
// unknown keys / non-numeric knobs / a second bare positional are rejected.
func TestCommunityParam(t *testing.T) {
	t.Run("bare seed uses defaults", func(t *testing.T) {
		s, _ := NewSource("alice")
		m, err := CommunityParam(s)
		if err != nil {
			t.Fatalf("CommunityParam: %v", err)
		}
		if m.Seed != "alice" || m.MaxSize != 0 {
			t.Fatalf("got seed=%q max_size=%d, want alice/0", m.Seed, m.MaxSize)
		}
		if m.Reduction != "none" || m.Objective != "max" || m.Weighting != "raw" {
			t.Fatalf("got reduction=%q objective=%q weighting=%q, want none/max/raw", m.Reduction, m.Objective, m.Weighting)
		}
	})

	t.Run("positional max_size", func(t *testing.T) {
		s, _ := NewSource("alice 20")
		m, err := CommunityParam(s)
		if err != nil {
			t.Fatalf("CommunityParam: %v", err)
		}
		if m.MaxSize != 20 {
			t.Fatalf("MaxSize = %d, want 20", m.MaxSize)
		}
	})

	t.Run("reduction/objective/knobs compose in any order", func(t *testing.T) {
		s, _ := NewSource("alice max_size=20 reduction=mst objective=min restart_prob=0.25 epsilon=1e-3")
		m, err := CommunityParam(s)
		if err != nil {
			t.Fatalf("CommunityParam: %v", err)
		}
		if m.MaxSize != 20 || m.Reduction != "mst" || m.Objective != "min" {
			t.Fatalf("got max_size=%d reduction=%q objective=%q, want 20/mst/min", m.MaxSize, m.Reduction, m.Objective)
		}
		if m.RestartProb != 0.25 || m.Epsilon != 1e-3 {
			t.Fatalf("got restart_prob=%v epsilon=%v, want 0.25/1e-3", m.RestartProb, m.Epsilon)
		}
	})

	t.Run("rejects unknown key, non-numeric knobs, second positional", func(t *testing.T) {
		for _, in := range []string{
			"alice reduction=bogus",
			"alice objective=bogus",
			"alice top_n=5", // pagerank kwarg not valid on community
			"alice restart_prob=high",
			"alice prefix=",
			"alice 1 2", // second bare positional
		} {
			s, _ := NewSource(in)
			if _, err := CommunityParam(s); err == nil {
				t.Errorf("CommunityParam(%q) = nil, want error", in)
			}
		}
	})
}
