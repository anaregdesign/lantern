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
	"testing"

	"github.com/anaregdesign/lantern/core/search"
)

// productionIndex replicates the exact index core/graphcache.newSearchIndex
// builds for SearchVertices — the multilingual bigram pipeline plus BM25. It
// is intentionally a replica, not an import: graphcache depends on search, so
// this package (a sibling under search) states the pipeline explicitly, and
// this comment plus the one on newSearchIndex keep the two in sync.
func productionIndex() *search.InvertedIndex[string, search.Text] {
	normalizers := []search.Normalizer{
		search.WidthNormalizer{},
		search.DiacriticNormalizer{},
		search.LowercaseNormalizer{},
		search.PunctuationNormalizer{},
		search.SpaceNormalizer{},
	}
	analyzer := search.NewAnalyzer(
		normalizers,
		search.NGramTokenizer{N: 2},
		[]search.TokenFilter{search.WhitespaceFilter{}},
	)
	return search.NewInvertedIndex[string, search.Text](analyzer, search.BM25{K1: search.DefaultBM25K1, B: search.DefaultBM25B})
}

// productionFloors are the ratcheted minima per corpus, pinned from a measured
// run of the current production pipeline (values truncated down to 3 decimals
// so the assertion has headroom of at most one thousandth). RankSearcher
// breaks score ties deterministically, so these numbers are exact and stable;
// any drop below a floor is a real ranking change, not jitter.
var productionFloors = map[string]Metrics{
	"en":    {NDCG10: 0.894, MRR: 0.980, Recall50: 0.958},
	"ja":    {NDCG10: 0.911, MRR: 1.000, Recall50: 0.728},
	"mixed": {NDCG10: 0.947, MRR: 1.000, Recall50: 0.777},
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
// metric functions and logs Lantern's delta per corpus. It reports rather than
// asserts: the epic's definition of done (#886) — Lantern >= Lucene on every
// corpus — becomes an assertion only once the gap-closing issues land.
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
