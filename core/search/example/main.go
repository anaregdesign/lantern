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

	// An InvertedIndex is the default Indexer + Searcher. Its Analyzer runs three
	// stages in order; naming each one keeps the pipeline explicit:
	//   1. normalizers rewrite the raw text (here: fold to lower case),
	//   2. the tokenizer splits it into terms (UnicodeTokenizer treats whitespace
	//      and punctuation as delimiters), and
	//   3. token filters post-process the terms (none here—UnicodeTokenizer has
	//      already dropped the punctuation a filter would otherwise trim).
	// Passing a nil Scorer ranks matches with BM25 using its default parameters.
	normalizers := []search.Normalizer{search.LowercaseNormalizer{}}
	tokenizer := search.UnicodeTokenizer{}
	var tokenFilters []search.TokenFilter
	analyzer := search.NewAnalyzer(normalizers, tokenizer, tokenFilters...)
	idx := search.NewInvertedIndex[string, search.Text](analyzer, nil)

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
	// both   0.991
	// panda  0.470
	// fox    0.447

	// Delete removes a document from every posting list it appears in.
	idx.Delete("both")

	log.Println(`search("fox panda") after deleting "both":`)
	for _, hit := range idx.Search("fox panda") {
		log.Printf("  %-6s %.3f", hit.ID, hit.Score)
	}
	// panda  0.710
	// fox    0.677
}
