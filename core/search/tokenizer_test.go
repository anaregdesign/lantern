package search

import "testing"

func TestWhitespaceTokenizer(t *testing.T) {
	got := termsOf(WhitespaceTokenizer{}.Tokenize("hello,  world\tfoo\nbar"))
	want := []string{"hello,", "world", "foo", "bar"}
	if !equalStrings(got, want) {
		t.Fatalf("Tokenize = %v, want %v", got, want)
	}
	if got := (WhitespaceTokenizer{}).Tokenize("   \t\n  "); len(got) != 0 {
		t.Fatalf("whitespace-only Tokenize = %v, want empty", got)
	}
}

func TestUnicodeTokenizer(t *testing.T) {
	// Punctuation and whitespace are delimiters; letters/digits survive.
	got := termsOf(UnicodeTokenizer{}.Tokenize("Hello, world! node-1 café 42"))
	want := []string{"Hello", "world", "node", "1", "café", "42"}
	if !equalStrings(got, want) {
		t.Fatalf("Tokenize = %v, want %v", got, want)
	}
	if got := (UnicodeTokenizer{}).Tokenize("…!!! --- ,,,"); len(got) != 0 {
		t.Fatalf("punctuation-only Tokenize = %v, want empty", got)
	}
}

func TestTokenizerFunc(t *testing.T) {
	var tk Tokenizer = TokenizerFunc(func(s string) []Token { return []Token{{Term: s}} })
	if got := termsOf(tk.Tokenize("whole")); !equalStrings(got, []string{"whole"}) {
		t.Fatalf("Tokenize = %v, want [whole]", got)
	}
}

func TestNGramTokenizer(t *testing.T) {
	tests := []struct {
		name string
		n    int
		text string
		want []string
	}{
		{"trigrams", 3, "search", []string{"sea", "ear", "arc", "rch"}},
		{"bigrams", 2, "abc", []string{"ab", "bc"}},
		{"exact length is one gram", 2, "ab", []string{"ab"}},
		{"shorter than n yields nothing", 2, "a", nil},
		{"empty text", 2, "", nil},
		// Runs over runes, not bytes, so multi-byte scripts split cleanly.
		{"cjk bigrams", 2, "日本語", []string{"日本", "本語"}},
		// The window slides over whitespace too; normalize first to avoid it.
		{"window includes spaces", 2, "ab cd", []string{"ab", "b ", " c", "cd"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := termsOf(NGramTokenizer{N: tt.n}.Tokenize(tt.text))
			if !equalStrings(got, tt.want) {
				t.Fatalf("Tokenize(%q) N=%d = %v, want %v", tt.text, tt.n, got, tt.want)
			}
		})
	}

	// The zero value (N=0) and any N<1 degrade to unigrams.
	if got := termsOf(NGramTokenizer{}.Tokenize("ab")); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("zero-value Tokenize = %v, want [a b]", got)
	}
}
