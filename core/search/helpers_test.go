package search

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
