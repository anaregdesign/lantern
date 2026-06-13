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
	// the combination you would reach for when you need *infix* matching — finding
	// a query inside longer words, which a word-splitting tokenizer cannot do.
	// Its three stages:
	//   1. Normalizers clean the raw text before tokenization. This matters more
	//      for n-grams than for word tokens: NGramTokenizer slides its window over
	//      every rune — punctuation and spaces included — so stray marks or double
	//      spaces would leak straight into the grams. So we fold case
	//      (LowercaseNormalizer), turn every mark into a space
	//      (PunctuationNormalizer), then collapse the runs and trim
	//      (SpaceNormalizer), leaving clean single-space-delimited text.
	//   2. The tokenizer emits every 3-rune window (NGramTokenizer{N: 3}). Sharing
	//      a gram is what makes a query match: "arch" and "search" both contain
	//      the grams "arc" and "rch", so a search for "arch" finds "search".
	//   3. No token filters. A StopWordFilter matches whole words ("the", "and"),
	//      but n-gram terms are 3-rune fragments that never line up with a
	//      whole-word stop list — it is simply the wrong tool for n-grams.
	// The same analyzer runs over documents and queries, so both sides share the
	// same n; a query shorter than 3 runes shares no gram and matches nothing.
	normalizers := []search.Normalizer{
		search.LowercaseNormalizer{},
		search.PunctuationNormalizer{},
		search.SpaceNormalizer{},
	}
	tokenizer := search.NGramTokenizer{N: 3}
	analyzer := search.NewAnalyzer(normalizers, tokenizer)

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
	// arch     0.777
	// search   0.680
	// research 0.659

	// Delete removes a document from every posting list it appears in. Dropping
	// the literal "arch" leaves the infix matches in "search" and "research".
	idx.Delete("arch")

	log.Println(`search("arch") after deleting "arch":`)
	for _, hit := range idx.Search("arch") {
		log.Printf("  %-8s %.3f", hit.ID, hit.Score)
	}
	// search   0.921
	// research 0.894
}
