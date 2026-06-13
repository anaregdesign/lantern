// Command example shows the public API of the core/search package: build an
// inverted index, index documents, run ranked queries, and delete a document.
//
// Run it from the core module with:
//
//	go run ./search/example
package main

import (
	"log"

	"github.com/anaregdesign/lantern/core/search"
)

func main() {
	// Drop the timestamp prefix so the output matches the comments below.
	log.SetFlags(0)

	// An InvertedIndex is the default Indexer + Searcher. The Analyzer below is
	// the combination you would actually reach for on English prose: every one
	// of its three stages earns its place instead of leaving the term stream raw.
	//   1. Normalizers rewrite the text before tokenization. LowercaseNormalizer
	//      folds case so a query for "fox" also matches "Fox".
	//   2. The tokenizer splits the normalized text into terms. UnicodeTokenizer
	//      breaks on every non-letter/digit rune, so it strips punctuation in the
	//      same pass and needs no separate PunctuationFilter.
	//   3. Token filters prune the term stream in order. LengthFilter drops
	//      1-rune noise, and StopWordFilter removes the high-frequency function
	//      words ("the", "a", "and", …) that match almost everything and so add
	//      no signal — only the content words ("fox", "panda", …) survive.
	// Running this very analyzer over both documents and queries is what keeps
	// index-time and query-time terms symmetric.
	normalizers := []search.Normalizer{search.LowercaseNormalizer{}}
	tokenizer := search.UnicodeTokenizer{}
	tokenFilters := []search.TokenFilter{
		search.LengthFilter{Min: 2},
		search.NewStopWordFilter(
			"a", "an", "and", "are", "as", "at", "by", "for", "from",
			"in", "into", "is", "of", "on", "or", "over", "the", "to", "with",
		),
	}
	analyzer := search.NewAnalyzer(normalizers, tokenizer, tokenFilters...)

	// Rank matches with Okapi BM25, stated explicitly rather than left to the
	// nil default: K1 = 1.2 tunes term-frequency saturation (the 2nd occurrence
	// of a term counts far more than the 10th) and B = 0.75 tunes how strongly a
	// longer-than-average document is penalized for diluting a term.
	scorer := search.BM25{K1: 1.2, B: 0.75}
	idx := search.NewInvertedIndex[string, search.Text](analyzer, scorer)

	// Index documents under any comparable ID. Text adapts a plain string to a
	// Document; indexing an existing ID again replaces it.
	idx.Index("fox", search.Text("The quick brown fox jumps over the lazy dog"))
	idx.Index("panda", search.Text("A giant panda eats bamboo in the forest"))
	idx.Index("both", search.Text("The fox and the panda are friends"))

	// Search returns the matching documents ranked by relevance, most relevant
	// first. The query's terms are matched as an OR union.
	log.Println(`search("fox panda"):`)
	for _, hit := range idx.Search("fox panda") {
		log.Printf("  %-6s %.3f", hit.ID, hit.Score)
	}
	// both   1.101
	// panda  0.457
	// fox    0.421

	// Delete removes a document from every posting list it appears in.
	idx.Delete("both")

	log.Println(`search("fox panda") after deleting "both":`)
	for _, hit := range idx.Search("fox panda") {
		log.Printf("  %-6s %.3f", hit.ID, hit.Score)
	}
	// panda  0.720
	// fox    0.668
}
