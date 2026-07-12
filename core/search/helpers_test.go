package search

import (
	"cmp"
	"strings"
)

func compareStringID(a, b string) int { return strings.Compare(a, b) }
func compareIntID(a, b int) int       { return cmp.Compare(a, b) }

// equalStrings reports whether a and b contain the same strings in the same
// order. Shared by the search package's white-box tests.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// termsOf extracts each token's Term, preserving order, so analyzer,
// tokenizer, and filter tests can compare against a plain []string.
func termsOf(tokens []Token) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = t.Term
	}
	return out
}

// idsOf extracts the document IDs from ranked search results, preserving the
// result order, so index tests can assert on matches and ranking separately.
func idsOf[S comparable](results []Result[S]) []S {
	out := make([]S, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

// postingsCount sums the live posting lists across the index's token classes,
// so reclamation tests can assert "everything released" without caring which
// channel a term lived on.
func postingsCount[S comparable, D Document](idx *InvertedIndex[S, D]) int {
	n := 0
	for class := range idx.classes {
		n += len(idx.classes[class].postings)
	}
	return n
}

// totalLenSum sums the per-class document-length totals, the cross-class
// equivalent of the old single totalLen counter.
func totalLenSum[S comparable, D Document](idx *InvertedIndex[S, D]) int {
	n := 0
	for class := range idx.classes {
		n += idx.classes[class].totalLen
	}
	return n
}
