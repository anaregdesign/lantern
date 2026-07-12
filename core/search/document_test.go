package search

import (
	"testing"
	"time"
)

func TestDocumentAdapters(t *testing.T) {
	ts := time.Date(2026, 6, 14, 1, 2, 3, 0, time.UTC)
	cases := []struct {
		name string
		doc  Document
		want string
	}{
		{"Text", Text("hello world"), "hello world"},
		{"Int", Int(-42), "-42"},
		{"IntNarrowing", Int(int8(7)), "7"},
		{"Uint", Uint(42), "42"},
		{"Float", Float(3.14), "3.14"},
		{"FloatExp", Float(1e20), "1e+20"},
		{"Bool", Bool(true), "true"},
		{"Bytes", Bytes("byte text"), "byte text"},
		{"Time", Time(ts), "2026-06-14T01:02:03Z"},
		{"Duration", Duration(90 * time.Minute), "1h30m0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.doc.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDocumentAdaptersIndexable(t *testing.T) {
	// Every adapter must be usable as a real index payload: indexing a value and
	// querying its rendered text should find it.
	analyzer := NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, UnicodeTokenizer{}, nil)
	idx := NewInvertedIndex[string, Document](analyzer, BM25{K1: 1.2, B: 0.75}, compareStringID)

	idx.Index("int", Int(2026))
	idx.Index("bool", Bool(false))
	idx.Index("dur", Duration(90*time.Minute))

	for _, tc := range []struct{ query, want string }{
		{"2026", "int"},
		{"false", "bool"},
		{"1h30m0s", "dur"},
	} {
		got := idsOf(idx.Search(tc.query))
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("Search(%q) = %v, want [%s]", tc.query, got, tc.want)
		}
	}
}
