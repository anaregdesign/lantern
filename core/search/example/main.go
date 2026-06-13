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
	// the combination you would reach for when one index has to serve *many
	// languages* and still support *infix* matching — finding a query inside
	// longer words, which a word-splitting tokenizer cannot do. Its four stages:
	//   1. Normalizers clean the raw text before tokenization. This matters more
	//      for n-grams than for word tokens: NGramTokenizer slides its window over
	//      every rune — punctuation and spaces included — so stray marks or double
	//      spaces would leak straight into the grams. So we fold case
	//      (LowercaseNormalizer), turn every mark into a space
	//      (PunctuationNormalizer), then collapse the runs and trim
	//      (SpaceNormalizer), leaving clean single-space-delimited text.
	//   2. The tokenizer emits every 2-rune window (NGramTokenizer{N: 2}). Bigrams
	//      are the language-independent sweet spot: they keep two-character words
	//      indexable — essential for CJK, where "東京" or "中国" is a whole word —
	//      while still matching infixes, so "arch" and "search" share the grams
	//      "ar", "rc", "ch" and a search for "arch" finds "search".
	//   3. One token filter, WhitespaceFilter, is the *quality filter*: a bigram
	//      that holds a space can only be one that straddles a word boundary, so
	//      dropping it keeps just the intra-word grams. That stops a query from
	//      matching on the noise between two words and keeps BM25 document lengths
	//      honest. It is a no-op for space-free scripts (CJK, Thai), so the same
	//      pipeline stays correct across languages.
	// The same analyzer runs over documents and queries, so both sides share the
	// same n; a query shorter than 2 runes shares no gram and matches nothing.
	normalizers := []search.Normalizer{
		search.LowercaseNormalizer{},
		search.PunctuationNormalizer{},
		search.SpaceNormalizer{},
	}
	tokenizer := search.NGramTokenizer{N: 2}
	analyzer := search.NewAnalyzer(normalizers, tokenizer, search.WhitespaceFilter{})

	// Rank matches with Okapi BM25, stated explicitly rather than left to the
	// nil default: K1 = 1.2 tunes term-frequency saturation (the 2nd occurrence
	// of a term counts far more than the 10th) and B = 0.75 tunes how strongly a
	// longer-than-average document is penalized for diluting a term.
	scorer := search.BM25{K1: 1.2, B: 0.75}
	idx := search.NewInvertedIndex[string, search.Text](analyzer, scorer)

	// Index documents under any comparable ID. Text adapts a plain string to a
	// Document; the messy punctuation and spacing here is exactly what the
	// normalizers clean up before n-gramming.
	idx.Index("search", search.Text("Full-text search."))
	idx.Index("research", search.Text("Academic  research"))
	idx.Index("arch", search.Text("A stone arch"))
	idx.Index("panda", search.Text("A giant panda"))

	// Search returns the matching documents ranked by relevance, most relevant
	// first. "arch" matches as an infix: it is a substring of both "search" and
	// "research", which a word tokenizer would never surface.
	log.Println(`search("arch"):`)
	for _, hit := range idx.Search("arch") {
		log.Printf("  %-8s %.3f", hit.ID, hit.Score)
	}
	// arch     1.220
	// search   1.028
	// research 0.920

	// Delete removes a document from every posting list it appears in. Dropping
	// the literal "arch" leaves the infix matches in "search" and "research".
	idx.Delete("arch")

	log.Println(`search("arch") after deleting "arch":`)
	for _, hit := range idx.Search("arch") {
		log.Printf("  %-8s %.3f", hit.ID, hit.Score)
	}
	// search   1.410
	// research 1.268
}
