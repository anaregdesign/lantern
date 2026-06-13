package search

import "testing"

func TestLowercaseFilter(t *testing.T) {
	got := termsOf(LowercaseFilter{}.Filter([]Token{{"Foo"}, {"BAR"}, {"baz"}}))
	if !equalStrings(got, []string{"foo", "bar", "baz"}) {
		t.Fatalf("Filter = %v", got)
	}
}

func TestPunctuationFilter(t *testing.T) {
	in := []Token{{"hello,"}, {"!!!"}, {"node-1."}, {"(world)"}}
	got := termsOf(PunctuationFilter{}.Filter(in))
	want := []string{"hello", "node-1", "world"}
	if !equalStrings(got, want) {
		t.Fatalf("Filter = %v, want %v", got, want)
	}
}

func TestEmptyTokenFilter(t *testing.T) {
	got := termsOf(EmptyTokenFilter{}.Filter([]Token{{"a"}, {""}, {"  "}, {"b"}}))
	if !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("Filter = %v, want [a b]", got)
	}
}

func TestLengthFilter(t *testing.T) {
	in := []Token{{"a"}, {"ab"}, {"abcd"}, {"abcde"}}
	got := termsOf(LengthFilter{Min: 2, Max: 4}.Filter(in))
	if !equalStrings(got, []string{"ab", "abcd"}) {
		t.Fatalf("Filter = %v, want [ab abcd]", got)
	}
	// Max 0 means no upper bound.
	got = termsOf(LengthFilter{Min: 3}.Filter([]Token{{"hi"}, {"hey"}, {"hello"}}))
	if !equalStrings(got, []string{"hey", "hello"}) {
		t.Fatalf("Filter = %v, want [hey hello]", got)
	}
}

func TestStopWordFilter(t *testing.T) {
	f := NewStopWordFilter("the", "is", "A")
	got := termsOf(f.Filter([]Token{{"THE"}, {"cat"}, {"is"}, {"a"}, {"hat"}}))
	if !equalStrings(got, []string{"cat", "hat"}) {
		t.Fatalf("Filter = %v, want [cat hat]", got)
	}
}

func TestTokenFiltersChain(t *testing.T) {
	chain := TokenFilters(LowercaseFilter{}, NewStopWordFilter("the"))
	got := termsOf(chain.Filter([]Token{{"The"}, {"Cat"}}))
	if !equalStrings(got, []string{"cat"}) {
		t.Fatalf("Filter = %v, want [cat]", got)
	}
	// An empty chain is the identity transform.
	if got := termsOf(TokenFilters().Filter([]Token{{"x"}})); !equalStrings(got, []string{"x"}) {
		t.Fatalf("empty chain = %v, want [x]", got)
	}
}

func TestTokenFilterFunc(t *testing.T) {
	var f TokenFilter = TokenFilterFunc(func(ts []Token) []Token { return ts[:0] })
	if got := f.Filter([]Token{{"a"}}); len(got) != 0 {
		t.Fatalf("Filter = %v, want empty", got)
	}
}
