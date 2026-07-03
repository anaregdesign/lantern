package search

import "testing"

func TestLowercaseFilter(t *testing.T) {
	got := termsOf(LowercaseFilter{}.Filter([]Token{{Term: "Foo"}, {Term: "BAR"}, {Term: "baz"}}))
	if !equalStrings(got, []string{"foo", "bar", "baz"}) {
		t.Fatalf("Filter = %v", got)
	}
}

func TestPunctuationFilter(t *testing.T) {
	in := []Token{{Term: "hello,"}, {Term: "!!!"}, {Term: "node-1."}, {Term: "(world)"}}
	got := termsOf(PunctuationFilter{}.Filter(in))
	want := []string{"hello", "node-1", "world"}
	if !equalStrings(got, want) {
		t.Fatalf("Filter = %v, want %v", got, want)
	}
}

func TestEmptyTokenFilter(t *testing.T) {
	got := termsOf(EmptyTokenFilter{}.Filter([]Token{{Term: "a"}, {Term: ""}, {Term: "  "}, {Term: "b"}}))
	if !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("Filter = %v, want [a b]", got)
	}
}

func TestLengthFilter(t *testing.T) {
	in := []Token{{Term: "a"}, {Term: "ab"}, {Term: "abcd"}, {Term: "abcde"}}
	got := termsOf(LengthFilter{Min: 2, Max: 4}.Filter(in))
	if !equalStrings(got, []string{"ab", "abcd"}) {
		t.Fatalf("Filter = %v, want [ab abcd]", got)
	}
	// Max 0 means no upper bound.
	got = termsOf(LengthFilter{Min: 3}.Filter([]Token{{Term: "hi"}, {Term: "hey"}, {Term: "hello"}}))
	if !equalStrings(got, []string{"hey", "hello"}) {
		t.Fatalf("Filter = %v, want [hey hello]", got)
	}
}

func TestStopWordFilter(t *testing.T) {
	f := NewStopWordFilter("the", "is", "A")
	got := termsOf(f.Filter([]Token{{Term: "THE"}, {Term: "cat"}, {Term: "is"}, {Term: "a"}, {Term: "hat"}}))
	if !equalStrings(got, []string{"cat", "hat"}) {
		t.Fatalf("Filter = %v, want [cat hat]", got)
	}
}

func TestWhitespaceFilter(t *testing.T) {
	// Cross-boundary grams (those holding a space) are dropped; intra-word grams
	// and space-free tokens survive, regardless of which whitespace rune appears.
	in := []Token{{Term: "sea"}, {Term: "a g"}, {Term: "gia"}, {Term: "t\tp"}, {Term: "ねこ"}, {Term: " x"}, {Term: "y "}, {Term: "ok"}}
	got := termsOf(WhitespaceFilter{}.Filter(in))
	want := []string{"sea", "gia", "ねこ", "ok"}
	if !equalStrings(got, want) {
		t.Fatalf("Filter = %v, want %v", got, want)
	}
	// A token written without spaces (CJK) is never touched: no-op for scripts
	// that do not separate words with whitespace.
	got = termsOf(WhitespaceFilter{}.Filter([]Token{{Term: "東京"}, {Term: "京都"}}))
	if !equalStrings(got, []string{"東京", "京都"}) {
		t.Fatalf("Filter = %v, want [東京 京都]", got)
	}
}

func TestTokenFilterFunc(t *testing.T) {
	var f TokenFilter = TokenFilterFunc(func(ts []Token) []Token { return ts[:0] })
	if got := f.Filter([]Token{{Term: "a"}}); len(got) != 0 {
		t.Fatalf("Filter = %v, want empty", got)
	}
}
