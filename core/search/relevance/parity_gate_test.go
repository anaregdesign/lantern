package relevance

// This is a cross-cutting quality gate (see the pairing rules in AGENTS.md):
// it drives the full production search pipeline — the analyzer stack that
// core/graphcache.newSearchIndex installs, the inverted index, and BM25 —
// through the golden corpora and ratchets the resulting relevance metrics.
// The floors are the measured reality of the current pipeline; a change that
// pushes a metric below its floor is a relevance regression and must not
// merge, while a change that durably raises one should raise the floor in the
// same PR (CONTRIBUTING.md "Coverage floor" describes the same ratchet idea
// for test coverage; #887 applies it to search quality, #886 tracks the gaps
// the epic wants ratcheted upward).

import (
	"os"
	"sort"
	"testing"

	"github.com/anaregdesign/lantern/core/search"
)

// productionIndex replicates the exact index core/graphcache.newSearchIndex
// builds for SearchVertices — since #888 the script-aware dual-channel
// analyzer with class-weighted BM25, and since #889 with positional postings
// (WithPositions) for phrase and proximity ranking. It is intentionally a
// replica, not an import: graphcache depends on search, so this package (a
// sibling under search) states the pipeline explicitly, and this comment plus
// the one on newSearchIndex keep the two in sync.
func productionIndex() *search.InvertedIndex[string, search.Text] {
	return search.NewInvertedIndex[string, search.Text](
		search.NewScriptAwareAnalyzer(),
		search.ClassWeighted{
			Base:       search.BM25{K1: search.DefaultBM25K1, B: search.DefaultBM25B},
			GramWeight: search.DefaultGramWeight,
		},
		search.WithPositions(),
	)
}

// productionFloors are the ratcheted minima per corpus, pinned from a measured
// run of the current production pipeline (values truncated down to 3 decimals
// so the assertion has headroom of at most one thousandth). RankSearcher
// breaks score ties deterministically, so these numbers are exact and stable;
// any drop below a floor is a real ranking change, not jitter.
var productionFloors = map[string]Metrics{
	// Re-pinned for #888's script-aware pipeline. Versus the pure-bigram
	// floors it replaced: en nDCG@10 0.894 -> 0.927 and MRR 0.980 -> 1.0
	// (whole-word evidence now dominates fragments), en Recall@50 gave back
	// 0.958 -> 0.951 (an accepted trade: still far above Lucene's 0.828),
	// ja unchanged (the CJK path is the same bigram strategy), mixed nDCG@10
	// 0.947 -> 0.953.
	"en":    {NDCG10: 0.927, MRR: 1.000, Recall50: 0.951},
	"ja":    {NDCG10: 0.911, MRR: 1.000, Recall50: 0.728},
	"mixed": {NDCG10: 0.953, MRR: 1.000, Recall50: 0.777},
}

func TestProductionPipelineRelevanceFloors(t *testing.T) {
	corpora, err := Corpora()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range corpora {
		t.Run(c.Name, func(t *testing.T) {
			idx := productionIndex()
			c.IndexDocs(idx)
			m := Evaluate(c, RankSearcher(idx))
			t.Logf("%s: nDCG@10=%.4f MRR=%.4f Recall@50=%.4f", c.Name, m.NDCG10, m.MRR, m.Recall50)

			floor, ok := productionFloors[c.Name]
			if !ok {
				t.Fatalf("no ratchet floor pinned for corpus %q — add it to productionFloors", c.Name)
			}
			if m.NDCG10 < floor.NDCG10 {
				t.Errorf("nDCG@10 = %.4f fell below floor %.4f", m.NDCG10, floor.NDCG10)
			}
			if m.MRR < floor.MRR {
				t.Errorf("MRR = %.4f fell below floor %.4f", m.MRR, floor.MRR)
			}
			if m.Recall50 < floor.Recall50 {
				t.Errorf("Recall@50 = %.4f fell below floor %.4f", m.Recall50, floor.Recall50)
			}
		})
	}
}

// TestLuceneBaselineComparison replays the pinned Lucene rankings (produced
// offline by testbed/lucene-baseline; CI never runs a JVM) through the same
// metric functions and ASSERTS the epic's definition of done (#886): the
// production pipeline scores at least as high as every pinned Lucene run on
// every metric of every corpus. #888 (script-aware analyzer) is what made
// this hold; keeping it as an assertion means later ranking work (#889,
// #890, #891) may reshuffle results but never below Lucene.
func TestLuceneBaselineComparison(t *testing.T) {
	runs, err := LoadBaselineRuns(BaselineRunsFile)
	if os.IsNotExist(err) {
		t.Skipf("no pinned baseline at %s — generate it with testbed/lucene-baseline (see testbed/SKILL.md)", BaselineRunsFile)
	}
	if err != nil {
		t.Fatal(err)
	}

	corpora, err := Corpora()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Corpus, len(corpora))
	for _, c := range corpora {
		byName[c.Name] = c
	}

	for name, run := range runs.Runs {
		t.Run(name, func(t *testing.T) {
			c, ok := byName[run.Corpus]
			if !ok {
				t.Fatalf("run %q references unknown corpus %q", name, run.Corpus)
			}
			for qid := range run.Results {
				if !corpusHasQuery(c, qid) {
					t.Fatalf("run %q ranks unknown query %q — regenerate the baseline after editing fixtures", name, qid)
				}
			}
			lucene := run.Evaluate(c)

			idx := productionIndex()
			c.IndexDocs(idx)
			lantern := Evaluate(c, RankSearcher(idx))

			t.Logf("%s [%s, %s]: lucene nDCG@10=%.4f MRR=%.4f Recall@50=%.4f | lantern nDCG@10=%.4f MRR=%.4f Recall@50=%.4f",
				run.Corpus, runs.Engine, run.Analyzer,
				lucene.NDCG10, lucene.MRR, lucene.Recall50,
				lantern.NDCG10, lantern.MRR, lantern.Recall50)

			// Both sides are deterministic; the epsilon only forgives the
			// float-summation ULPs of a legitimate exact tie (ja matches
			// Lucene's CJK bigrams ranking-for-ranking).
			const eps = 1e-9
			if lantern.NDCG10 < lucene.NDCG10-eps {
				t.Errorf("nDCG@10 %.4f below Lucene %s %.4f", lantern.NDCG10, run.Analyzer, lucene.NDCG10)
			}
			if lantern.MRR < lucene.MRR-eps {
				t.Errorf("MRR %.4f below Lucene %s %.4f", lantern.MRR, run.Analyzer, lucene.MRR)
			}
			if lantern.Recall50 < lucene.Recall50-eps {
				t.Errorf("Recall@50 %.4f below Lucene %s %.4f", lantern.Recall50, run.Analyzer, lucene.Recall50)
			}
		})
	}
}

// corpusHasQuery reports whether the corpus defines the query ID.
func corpusHasQuery(c Corpus, id string) bool {
	for _, q := range c.Queries {
		if q.ID == id {
			return true
		}
	}
	return false
}

// indexedEnCorpus loads the en corpus and indexes it into a fresh production
// pipeline — the shared setup for the phrase and match-mode precision gates.
func indexedEnCorpus(t *testing.T) (Corpus, *search.InvertedIndex[string, search.Text]) {
	t.Helper()
	corpora, err := Corpora()
	if err != nil {
		t.Fatal(err)
	}
	var en Corpus
	for _, c := range corpora {
		if c.Name == "en" {
			en = c
		}
	}
	if en.Name == "" {
		t.Fatal("en corpus not found")
	}
	idx := productionIndex()
	en.IndexDocs(idx)
	return en, idx
}

// queryByID indexes the corpus queries by ID.
func queryByID(c Corpus) map[string]Query {
	byID := make(map[string]Query, len(c.Queries))
	for _, q := range c.Queries {
		byID[q.ID] = q
	}
	return byID
}

// phraseQueries are the en-corpus queries whose intent is a contiguous phrase.
// The value is the expected text so a fixture rename fails loudly.
var phraseQueries = map[string]string{
	"q02": "kubernetes rolling update",
	"q05": "data set",
}

// TestPhraseSearchPrecision is the #889 phrase yardstick: on the phrase-query
// subset of the en corpus, phrase search drops the documents that merely
// scatter the query words and keeps only the contiguous match, so every
// returned document is relevant and the top one is a direct hit — a precision
// the recall-oriented OR-union term search cannot match. The metric is
// precision, not nDCG: on a corpus this small requiring adjacency trades recall
// for precision so nDCG@10 is a wash (the proximity boost carries the nDCG
// side, guarded by the floors gate).
func TestPhraseSearchPrecision(t *testing.T) {
	en, idx := indexedEnCorpus(t)
	byID := queryByID(en)
	for id, wantText := range phraseQueries {
		q, ok := byID[id]
		if !ok {
			t.Fatalf("phrase query %q is not in the en corpus — subset is stale", id)
		}
		if q.Text != wantText {
			t.Fatalf("phrase query %q text = %q, want %q — subset is stale", id, q.Text, wantText)
		}
		phrase := rankResults(idx.SearchPhrase(q.Text))
		term := idx.Search(q.Text)
		if len(phrase) == 0 {
			t.Errorf("%s %q: phrase search returned nothing", id, q.Text)
			continue
		}
		if len(phrase) > len(term) {
			t.Errorf("%s %q: phrase results %d exceed term results %d", id, q.Text, len(phrase), len(term))
		}
		for _, docID := range phrase {
			if q.Qrels[docID] == 0 {
				t.Errorf("%s %q: phrase hit %q is not relevant (grade 0)", id, q.Text, docID)
			}
		}
		if g := q.Qrels[phrase[0]]; g != 3 {
			t.Errorf("%s %q: top phrase hit %q graded %d, want 3", id, q.Text, phrase[0], g)
		}
		t.Logf("%s %q: phrase kept %d of %d term hits, all relevant, top=%s", id, q.Text, len(phrase), len(term), phrase[0])
	}
}

// matchAllQueries are the en-corpus multi-word queries used by
// TestMatchAllPrecision.
var matchAllQueries = []string{"q02", "q05"}

// TestMatchAllPrecision is the #890 yardstick: on multi-word queries, MatchAll
// narrows the OR-union to documents covering every query word, raising
// precision (every returned document is relevant) without collapsing recall
// (the grade-3 document survives). It runs on the production pipeline.
func TestMatchAllPrecision(t *testing.T) {
	en, idx := indexedEnCorpus(t)
	byID := queryByID(en)
	for _, id := range matchAllQueries {
		q, ok := byID[id]
		if !ok {
			t.Fatalf("match-mode query %q is not in the en corpus — subset is stale", id)
		}
		any := idx.SearchMatch(q.Text, search.MatchOptions{Mode: search.MatchAny})
		all := idx.SearchMatch(q.Text, search.MatchOptions{Mode: search.MatchAll})
		if len(all) == 0 {
			t.Errorf("%s %q: MatchAll returned nothing (recall collapse)", id, q.Text)
			continue
		}
		if len(all) > len(any) {
			t.Errorf("%s %q: MatchAll %d exceeds MatchAny %d", id, q.Text, len(all), len(any))
		}
		hasTop := false
		for _, r := range all {
			if q.Qrels[r.ID] == 0 {
				t.Errorf("%s %q: MatchAll hit %q is not relevant (grade 0)", id, q.Text, r.ID)
			}
			if q.Qrels[r.ID] == 3 {
				hasTop = true
			}
		}
		if !hasTop {
			t.Errorf("%s %q: MatchAll dropped the grade-3 document (recall collapse)", id, q.Text)
		}
		t.Logf("%s %q: MatchAll kept %d of %d MatchAny hits, all relevant", id, q.Text, len(all), len(any))
	}
}

// rankResults orders search results most-relevant first, breaking score ties by
// ID, matching RankSearcher's deterministic order.
func rankResults(results []search.Result[string]) []string {
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
