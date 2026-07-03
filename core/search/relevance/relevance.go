// Package relevance is the search-quality yardstick for core/search (#887):
// golden corpora with graded relevance judgments, the standard IR metrics
// (nDCG@10, MRR, Recall@50), and a runner that evaluates any ranking function
// against them. It exists so an analyzer or scoring change is judged by a
// number, not a hunch, and so Lantern's ranking can be compared like-for-like
// with a pinned Lucene baseline (see testbed/lucene-baseline, which replays
// the same corpora through stock Lucene and records its rankings).
//
// The corpora are deliberately small enough to review by hand and shaped like
// real Lantern vertex values: short prose fragments, key-like strings, and
// JSON-ish payloads, in English (en), Japanese (ja), and mixed CJK/Latin
// (mixed). Fixtures are embedded, so any package can A/B two pipelines in a
// test without path plumbing.
package relevance

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"

	"github.com/anaregdesign/lantern/core/search"
)

// Doc is one indexable document of a golden corpus.
type Doc struct {
	// ID is the document's identifier, unique within its corpus — the value a
	// Searcher returns and qrels grade.
	ID string `json:"id"`
	// Text is the raw text handed to the pipeline under evaluation.
	Text string `json:"text"`
}

// Query is one judged query of a golden corpus.
type Query struct {
	// ID names the query, so baseline runs can record rankings against it.
	ID string `json:"id"`
	// Text is the raw query string handed to the pipeline under evaluation.
	Text string `json:"text"`
	// Qrels are the graded relevance judgments: document ID → grade, where 3
	// is a direct hit, 2 clearly relevant, 1 marginally relevant. Documents
	// absent from the map are judged irrelevant (grade 0).
	Qrels map[string]int `json:"qrels"`
}

// Corpus is a golden corpus: a document set plus judged queries over it.
type Corpus struct {
	// Name identifies the corpus (e.g. "en", "ja", "mixed").
	Name string `json:"name"`
	// Docs is the full document set to index before evaluating.
	Docs []Doc `json:"docs"`
	// Queries are the judged queries Evaluate runs.
	Queries []Query `json:"queries"`
}

// IndexDocs indexes every corpus document into idx as search.Text, keyed by
// its document ID — the one-liner between loading a corpus and evaluating a
// pipeline against it.
func (c Corpus) IndexDocs(idx search.Indexer[string, search.Text]) {
	for _, d := range c.Docs {
		idx.Index(d.ID, search.Text(d.Text))
	}
}

//go:embed testdata/en.json testdata/ja.json testdata/mixed.json
var corporaFS embed.FS

// corporaFiles lists the embedded fixtures in the order Corpora returns them.
var corporaFiles = []string{"testdata/en.json", "testdata/ja.json", "testdata/mixed.json"}

// Corpora returns the embedded golden corpora (en, ja, mixed — in that order).
// The fixtures are validated on load, so a malformed edit fails loudly here
// rather than as a silently weaker evaluation.
func Corpora() ([]Corpus, error) {
	out := make([]Corpus, 0, len(corporaFiles))
	for _, name := range corporaFiles {
		c, err := loadCorpus(corporaFS, name)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// loadCorpus parses and validates one fixture file.
func loadCorpus(fsys fs.FS, name string) (Corpus, error) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Corpus{}, fmt.Errorf("relevance: reading %s: %w", name, err)
	}
	var c Corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		return Corpus{}, fmt.Errorf("relevance: parsing %s: %w", name, err)
	}
	if err := c.validate(); err != nil {
		return Corpus{}, fmt.Errorf("relevance: %s: %w", name, err)
	}
	return c, nil
}

// validate enforces the invariants the metrics rely on: unique document IDs,
// unique query IDs, and every qrel referencing an existing document with a
// grade in [1, 3] (grade 0 is expressed by absence, so it cannot drift out of
// sync with the document set).
func (c Corpus) validate() error {
	if c.Name == "" {
		return fmt.Errorf("corpus has no name")
	}
	if len(c.Docs) == 0 || len(c.Queries) == 0 {
		return fmt.Errorf("corpus %q needs docs and queries", c.Name)
	}
	docs := make(map[string]struct{}, len(c.Docs))
	for _, d := range c.Docs {
		if d.ID == "" || d.Text == "" {
			return fmt.Errorf("doc %q needs id and text", d.ID)
		}
		if _, dup := docs[d.ID]; dup {
			return fmt.Errorf("duplicate doc id %q", d.ID)
		}
		docs[d.ID] = struct{}{}
	}
	queries := make(map[string]struct{}, len(c.Queries))
	for _, q := range c.Queries {
		if q.ID == "" || q.Text == "" {
			return fmt.Errorf("query %q needs id and text", q.ID)
		}
		if _, dup := queries[q.ID]; dup {
			return fmt.Errorf("duplicate query id %q", q.ID)
		}
		queries[q.ID] = struct{}{}
		if len(q.Qrels) == 0 {
			return fmt.Errorf("query %q has no judgments", q.ID)
		}
		for id, grade := range q.Qrels {
			if _, ok := docs[id]; !ok {
				return fmt.Errorf("query %q judges unknown doc %q", q.ID, id)
			}
			if grade < 1 || grade > 3 {
				return fmt.Errorf("query %q grades doc %q outside [1,3]: %d", q.ID, id, grade)
			}
		}
	}
	return nil
}

// RankSearcher adapts a Searcher into the ranking function Evaluate consumes,
// breaking score ties by document ID. Search documents ties as unspecified
// order; without a deterministic tie-break the metrics would jitter from run
// to run whenever tied documents straddle a relevance grade, and a ratcheted
// floor cannot tolerate jitter.
func RankSearcher(s search.Searcher[string]) func(q Query) []string {
	return func(q Query) []string {
		results := s.Search(q.Text)
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].Score != results[j].Score {
				return results[i].Score > results[j].Score
			}
			return results[i].ID < results[j].ID
		})
		ids := make([]string, len(results))
		for i, r := range results {
			ids[i] = r.ID
		}
		return ids
	}
}
