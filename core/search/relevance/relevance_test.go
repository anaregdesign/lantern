package relevance

import (
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/core/search"
)

func TestCorpora(t *testing.T) {
	corpora, err := Corpora()
	if err != nil {
		t.Fatalf("Corpora() = %v", err)
	}
	if len(corpora) != 3 {
		t.Fatalf("got %d corpora, want 3", len(corpora))
	}
	wantNames := []string{"en", "ja", "mixed"}
	for i, c := range corpora {
		if c.Name != wantNames[i] {
			t.Errorf("corpus %d name = %q, want %q", i, c.Name, wantNames[i])
		}
		// Loose lower bounds: the point is a corpus that shrank to nothing
		// fails loudly, not pinning exact fixture sizes into two places.
		if len(c.Docs) < 40 {
			t.Errorf("corpus %q has %d docs, want >= 40", c.Name, len(c.Docs))
		}
		if len(c.Queries) < 15 {
			t.Errorf("corpus %q has %d queries, want >= 15", c.Name, len(c.Queries))
		}
	}
}

func TestValidate(t *testing.T) {
	valid := Corpus{
		Name:    "unit",
		Docs:    []Doc{{ID: "a", Text: "alpha"}, {ID: "b", Text: "beta"}},
		Queries: []Query{{ID: "q", Text: "alpha", Qrels: map[string]int{"a": 3}}},
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid corpus rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(c *Corpus)
	}{
		{"NoName", func(c *Corpus) { c.Name = "" }},
		{"NoDocs", func(c *Corpus) { c.Docs = nil }},
		{"NoQueries", func(c *Corpus) { c.Queries = nil }},
		{"EmptyDocText", func(c *Corpus) { c.Docs[0].Text = "" }},
		{"DuplicateDocID", func(c *Corpus) { c.Docs[1].ID = "a" }},
		{"DuplicateQueryID", func(c *Corpus) {
			c.Queries = append(c.Queries, Query{ID: "q", Text: "beta", Qrels: map[string]int{"b": 1}})
		}},
		{"QueryWithoutJudgments", func(c *Corpus) { c.Queries[0].Qrels = nil }},
		{"QrelUnknownDoc", func(c *Corpus) { c.Queries[0].Qrels = map[string]int{"ghost": 2} }},
		{"GradeTooHigh", func(c *Corpus) { c.Queries[0].Qrels = map[string]int{"a": 4} }},
		{"GradeZeroMustBeAbsent", func(c *Corpus) { c.Queries[0].Qrels = map[string]int{"a": 0} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Corpus{
				Name:    valid.Name,
				Docs:    append([]Doc(nil), valid.Docs...),
				Queries: []Query{{ID: "q", Text: "alpha", Qrels: map[string]int{"a": 3}}},
			}
			tc.mutate(&c)
			if err := c.validate(); err == nil {
				t.Fatal("invalid corpus accepted")
			}
		})
	}
}

func TestIndexDocs(t *testing.T) {
	c := Corpus{
		Name: "unit",
		Docs: []Doc{{ID: "a", Text: "full text search"}, {ID: "b", Text: "graph traversal"}},
	}
	idx := search.NewInvertedIndex[string, search.Text](search.NewNGramAnalyzer(2), nil)
	c.IndexDocs(idx)
	results := idx.Search("search")
	if len(results) != 1 || results[0].ID != "a" {
		t.Fatalf("Search after IndexDocs = %v, want just doc a", results)
	}
}

func TestRankSearcher(t *testing.T) {
	idx := search.NewInvertedIndex[string, search.Text](search.NewNGramAnalyzer(2), nil)
	// Identical documents score identically; RankSearcher must break the tie
	// by ID so evaluation is deterministic run to run.
	idx.Index("zeta", search.Text("same text"))
	idx.Index("alpha", search.Text("same text"))
	idx.Index("miss", search.Text("unrelated"))

	ranked := RankSearcher(idx)(Query{ID: "q", Text: "same"})
	if want := []string{"alpha", "zeta"}; strings.Join(ranked, ",") != strings.Join(want, ",") {
		t.Fatalf("ranked = %v, want %v", ranked, want)
	}
}
