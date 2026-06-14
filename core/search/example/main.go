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
	// the *highest-quality general-purpose setup for n-gram search* — the lineup
	// of normalizers and the single filter that, with NGramTokenizer{N: 2}, give
	// the best recall and precision when one index must serve *many languages*
	// and still match *infixes* (a query found inside a longer word, which a
	// word-splitting tokenizer can never surface). Four stages:
	//   1. Normalizers clean and fold the raw text before tokenization, and their
	//      order matters — each step assumes the earlier ones ran. This weighs on
	//      n-grams more than on word tokens: NGramTokenizer slides its window over
	//      every rune — punctuation and spaces included — so any mark or double
	//      space leaks straight into the grams. WidthNormalizer folds full- and
	//      half-width characters to normal width (so "ＴＯＫＹＯ" matches "TOKYO")
	//      and runs first, before the punctuation step, so a full-width "！" turns
	//      into an ASCII "!" that step can act on. DiacriticNormalizer strips
	//      accents (so "café" matches "cafe"); LowercaseNormalizer folds case;
	//      PunctuationNormalizer turns every mark into a space, making it a word
	//      boundary the grams will not bridge; SpaceNormalizer then collapses the
	//      runs and trims, leaving clean single-space-delimited text.
	//   2. The tokenizer emits every 2-rune window (NGramTokenizer{N: 2}). Bigrams
	//      are the language-independent sweet spot: they keep two-character words
	//      indexable — essential for CJK, where "東京" or "中国" is a whole word —
	//      while still matching infixes, so "arch" and "search" share the grams
	//      "ar", "rc", "ch" and a search for "arch" finds "search".
	//   3. WhitespaceFilter is the one filter that pays off for n-grams. Once the
	//      normalizers leave words separated by single spaces, the only grams that
	//      can hold a space are those straddling a word boundary; dropping them
	//      indexes each document under its real word content, which raises
	//      precision (no matching on the noise between two words) and keeps BM25
	//      document lengths honest (the "data" demo below shows the ranking this
	//      buys). It is a no-op for space-free scripts (CJK, Thai), so the pipeline
	//      stays correct across languages. The package's other filters target
	//      *word* tokens and are deliberately left out: every gram is exactly N
	//      runes, so LengthFilter and EmptyTokenFilter never fire; StopWordFilter
	//      matches whole words, not fragments; and PunctuationFilter and
	//      LowercaseFilter would only repeat work the normalizers already did.
	// The same analyzer runs over documents and queries, so both sides share the
	// same n; a query shorter than 2 runes shares no gram and matches nothing.
	normalizers := []search.Normalizer{
		search.WidthNormalizer{},
		search.DiacriticNormalizer{},
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

	// The folding normalizers also raise recall across scripts: the same index
	// finds a document when the query differs only by accent or character width.
	// A second index keeps this demo separate from the infix one above.
	fold := search.NewInvertedIndex[string, search.Text](analyzer, scorer)
	fold.Index("cafe", search.Text("Café"))   // accented Latin
	fold.Index("tokyo", search.Text("ＴＯＫＹＯ")) // full-width Latin
	fold.Index("naive", search.Text("naïve"))

	// "cafe" with no accent still finds "Café": DiacriticNormalizer folded the
	// document's é to e on the way into the index and the query needs no accent.
	log.Println(`fold.Search("cafe"):`)
	for _, hit := range fold.Search("cafe") {
		log.Printf("  %-8s %.3f", hit.ID, hit.Score)
	}
	// cafe     3.179

	// "tokyo" in plain ASCII finds the full-width "ＴＯＫＹＯ": WidthNormalizer
	// folded both sides to the same normal-width letters.
	log.Println(`fold.Search("tokyo"):`)
	for _, hit := range fold.Search("tokyo") {
		log.Printf("  %-8s %.3f", hit.ID, hit.Score)
	}
	// tokyo    3.783

	// Recall is only half of "best": the boundary normalizers and WhitespaceFilter
	// also give the best *ranking*. Compare the pipeline against NewNGramAnalyzer(2)
	// — the batteries-included baseline that only lowercases before bigramming,
	// with no punctuation boundary and no whitespace filter — over the same three
	// documents, querying "data".
	tuned := search.NewInvertedIndex[string, search.Text](analyzer, scorer)
	tuned.Index("database", search.Text("database"))
	tuned.Index("data set", search.Text("data set"))
	tuned.Index("data science", search.Text("data science and machine learning"))

	naive := search.NewInvertedIndex[string, search.Text](search.NewNGramAnalyzer(2), scorer)
	naive.Index("database", search.Text("database"))
	naive.Index("data set", search.Text("data set"))
	naive.Index("data science", search.Text("data science and machine learning"))

	// The tuned pipeline ranks "data set" above "database": "data" is a whole word
	// there, and dropping the cross-word grams gives it the shorter, honest length
	// BM25 rewards.
	log.Println(`tuned.Search("data"):`)
	for _, hit := range tuned.Search("data") {
		log.Printf("  %-12s %.3f", hit.ID, hit.Score)
	}
	// data set      0.526
	// database      0.483
	// data science  0.284

	// The baseline counts "data set"'s boundary grams toward its length, so it
	// looks as long as "database" and the two tie — naive bigrams cannot tell a
	// whole-word match from a prefix fragment.
	log.Println(`naive.Search("data"):`)
	for _, hit := range naive.Search("data") {
		log.Printf("  %-12s %.3f", hit.ID, hit.Score)
	}
	// database      0.515  (ties with "data set"; Searcher leaves ties unordered,
	// data set      0.515   so these two may print in either order)
	// data science  0.277
}
