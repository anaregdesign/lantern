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
	return productionIndexWith()
}

// productionIndexWith builds the production pipeline with extra index options
// appended after the fixed WithPositions — the seam the #910 proximity harness
// uses to sweep WithProximityWeight without duplicating the analyzer/scorer
// wiring. With no options it is byte-for-byte productionIndex (default
// proximity weight), so the floors and Lucene gates keep measuring the shipped
// pipeline.
func productionIndexWith(opts ...search.IndexOption) *search.InvertedIndex[string, search.Text] {
	return search.NewInvertedIndex[string, search.Text](
		search.NewScriptAwareAnalyzer(),
		search.ClassWeighted{
			Base:       search.BM25{K1: search.DefaultBM25K1, B: search.DefaultBM25B},
			GramWeight: search.DefaultGramWeight,
		},
		append([]search.IndexOption{search.WithPositions()}, opts...)...,
	)
}

// productionFloors are the ratcheted minima per corpus, pinned from a measured
// run of the current production pipeline (values truncated down to 3 decimals
// so the assertion has headroom of at most one thousandth). RankSearcher
// breaks score ties deterministically, so these numbers are exact and stable;
// any drop below a floor is a real ranking change, not jitter.
var productionFloors = map[string]Metrics{
	// Re-pinned for #910's proximity qrels: the en and mixed corpora each gained
	// a proximity-sensitive query (q27 "zephyr quokka", mq16 "marimba gable")
	// whose two documents are exact word-multiset permutations — identical BM25,
	// so the proximity boost is the sole tie-breaker. Under the boost the tight
	// document ranks first (nDCG 1.0), lifting en nDCG@10 0.927 -> 0.930 and
	// Recall@50 0.951 -> 0.953, and mixed nDCG@10 0.953 -> 0.956 and Recall@50
	// 0.777 -> 0.791. ja is untouched by the new fixtures.
	"en":    {NDCG10: 0.930, MRR: 1.000, Recall50: 0.953},
	"ja":    {NDCG10: 0.911, MRR: 1.000, Recall50: 0.728},
	"mixed": {NDCG10: 0.956, MRR: 1.000, Recall50: 0.791},
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
	en := enCorpus(t)
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

// typoQueries maps a misspelled query (one edit from a real query word) to the
// en query ID whose judgments it shares — same intent, one typo away.
var typoQueries = map[string]string{
	"expresso":   "q01", // espresso
	"kubernets":  "q02", // kubernetes
	"carbonarra": "q07", // carbonara
}

// TestFuzzyRecoversTypos is the #891 yardstick. It indexes the en corpus with a
// word-only analyzer — deliberately without the bigram channel that already
// gives the production pipeline some typo tolerance — so the measurement
// isolates fuzzy term expansion: an exact search for a misspelled query recovers
// little, and Fuzziness=1 recovers the relevant documents, so recall rises. It
// also checks a clean query keeps its top hit under fuzzy, i.e. no precision
// cost when there is no typo.
func TestFuzzyRecoversTypos(t *testing.T) {
	en := enCorpus(t)
	idx := search.NewInvertedIndex[string, search.Text](
		search.NewAnalyzer([]search.Normalizer{search.LowercaseNormalizer{}}, search.UnicodeTokenizer{}, nil),
		nil,
	)
	en.IndexDocs(idx)
	byID := queryByID(en)

	improved := false
	for typo, qid := range typoQueries {
		q, ok := byID[qid]
		if !ok {
			t.Fatalf("typo subset references unknown query %q", qid)
		}
		exact := RecallAt(EvalDepth, rankResults(idx.Search(typo)), q.Qrels)
		fuzzy := RecallAt(EvalDepth, rankResults(idx.SearchMatch(typo, search.MatchOptions{Fuzziness: 1})), q.Qrels)
		if fuzzy < exact {
			t.Errorf("typo %q (%s): fuzzy recall %.3f below exact %.3f", typo, qid, fuzzy, exact)
		}
		if fuzzy > exact {
			improved = true
		}
		t.Logf("typo %q (%s): exact recall %.3f -> fuzzy %.3f", typo, qid, exact, fuzzy)
	}
	if !improved {
		t.Error("fuzzy expansion did not improve recall on any typo query")
	}

	// No precision cost on a clean query: fuzzy must not displace its top hit.
	clean := byID["q01"]
	exactTop := rankResults(idx.Search(clean.Text))
	fuzzyTop := rankResults(idx.SearchMatch(clean.Text, search.MatchOptions{Fuzziness: 1}))
	if len(exactTop) == 0 || len(fuzzyTop) == 0 || exactTop[0] != fuzzyTop[0] {
		t.Errorf("clean query %q: fuzzy changed the top hit", clean.Text)
	}
}

// enCorpus returns the en golden corpus, failing the test if it is missing.
func enCorpus(t *testing.T) Corpus {
	t.Helper()
	corpora, err := Corpora()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range corpora {
		if c.Name == "en" {
			return c
		}
	}
	t.Fatal("en corpus not found")
	return Corpus{}
}

// corporaByName loads and indexes the golden corpora by name.
func corporaByName(t *testing.T) map[string]Corpus {
	t.Helper()
	corpora, err := Corpora()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Corpus, len(corpora))
	for _, c := range corpora {
		byName[c.Name] = c
	}
	return byName
}

// proximityQueries are the queries #910 added to give the proximity boost a
// yardstick. Each judges two documents that are exact word-multiset
// permutations of each other — identical term frequencies and length, so BM25
// ties them exactly — differing only in how close the query's two words sit.
// The grade-3 document places them adjacent, the grade-1 one scatters them to
// opposite ends; the scattered document sorts first by ID, so without the boost
// the deterministic tie-break ranks it above the tight match (nDCG 0.71), and
// only the proximity boost lifts the tight match to the top (nDCG 1.0). The
// text is pinned so a fixture rename fails loudly.
var proximityQueries = []struct{ corpus, id, text string }{
	{"en", "q27", "zephyr quokka"},
	{"mixed", "mq16", "marimba gable"},
}

// proximityNDCG indexes corpus c into a production pipeline whose proximity
// weight is w and returns nDCG@10 for query q.
func proximityNDCG(c Corpus, q Query, w float64) float64 {
	idx := productionIndexWith(search.WithProximityWeight(w))
	c.IndexDocs(idx)
	return NDCGAt(NDCGDepth, RankSearcher(idx)(q), q.Qrels)
}

// TestProximityBoostImprovesRanking is the #910 yardstick that answers the
// prove-or-retire question: on the permutation-pair queries the proximity boost
// is the only signal that can separate the two documents, so turning it off
// (WithProximityWeight(0)) drops the tight match below the scattered one and
// turning it on at the shipped default lifts it to the ideal ranking. It also
// pins that the boosted result is deterministic.
func TestProximityBoostImprovesRanking(t *testing.T) {
	byName := corporaByName(t)
	for _, pq := range proximityQueries {
		t.Run(pq.corpus+"/"+pq.id, func(t *testing.T) {
			c, ok := byName[pq.corpus]
			if !ok {
				t.Fatalf("corpus %q not found", pq.corpus)
			}
			q, ok := queryByID(c)[pq.id]
			if !ok {
				t.Fatalf("proximity query %q missing from %s — fixtures stale", pq.id, pq.corpus)
			}
			if q.Text != pq.text {
				t.Fatalf("proximity query %q text = %q, want %q — fixtures stale", pq.id, q.Text, pq.text)
			}
			if len(q.Qrels) != 2 {
				t.Fatalf("proximity query %q should judge exactly 2 permutation docs, got %d", pq.id, len(q.Qrels))
			}

			off := proximityNDCG(c, q, 0)                       // boost disabled: permutations tie, ID order wins
			on := NDCGAt(NDCGDepth, defaultRank(c, q), q.Qrels) // shipped default weight

			if !(on > off) {
				t.Errorf("proximity boost did not improve %s/%s: nDCG off=%.4f on=%.4f", pq.corpus, pq.id, off, on)
			}
			if on < 1.0-1e-9 {
				t.Errorf("%s/%s: boosted nDCG@10 = %.4f, want ideal 1.0 (tight match first)", pq.corpus, pq.id, on)
			}
			if again := NDCGAt(NDCGDepth, defaultRank(c, q), q.Qrels); again != on {
				t.Errorf("%s/%s: boosted nDCG not deterministic: %.6f vs %.6f", pq.corpus, pq.id, on, again)
			}
			t.Logf("%s/%s: nDCG@10 boost-off=%.4f -> default=%.4f", pq.corpus, pq.id, off, on)
		})
	}
}

// defaultRank ranks q through the shipped production pipeline (default proximity
// weight), the exact index the floors and Lucene gates measure.
func defaultRank(c Corpus, q Query) []string {
	idx := productionIndex()
	c.IndexDocs(idx)
	return RankSearcher(idx)(q)
}

// proximitySweepWeights is the weight ladder TestProximityWeightSweep walks. It
// spans zero (off) through the shipped 0.3 to deliberately excessive values, so
// the recorded curve shows both that the boost earns its place and that pushing
// the weight higher buys no further ranking gain.
var proximitySweepWeights = []float64{0, 0.1, 0.3, 0.5, 1.0, 3.0}

// TestProximityWeightSweep sweeps the proximity weight and records the curve,
// the measurement that justifies the shipped 0.3 (#910). Per proximity query,
// nDCG@10 climbs from the boost-off tie-break (0.71) to the ideal (1.0) as soon
// as the weight is positive and then plateaus, so 0.3 sits safely on the
// plateau while staying modest enough that the BM25-dominated floors gate (which
// runs at 0.3) still holds. It asserts the plateau shape and logs the numbers
// for the issue record; it does not re-pin anything.
func TestProximityWeightSweep(t *testing.T) {
	byName := corporaByName(t)
	for _, pq := range proximityQueries {
		t.Run(pq.corpus+"/"+pq.id, func(t *testing.T) {
			c := byName[pq.corpus]
			q := queryByID(c)[pq.id]

			got := make([]float64, len(proximitySweepWeights))
			prev := -1.0
			for i, w := range proximitySweepWeights {
				got[i] = proximityNDCG(c, q, w)
				if got[i] < prev-1e-9 {
					t.Errorf("nDCG@10 not monotonic in weight: w=%.2f nDCG=%.4f < previous %.4f", w, got[i], prev)
				}
				prev = got[i]
			}
			t.Logf("%s/%s weight sweep %v -> nDCG@10 %v", pq.corpus, pq.id, proximitySweepWeights, got)

			off := got[0]
			for i, w := range proximitySweepWeights {
				if w == 0 {
					continue
				}
				if !(got[i] > off) {
					t.Errorf("weight %.2f did not beat boost-off nDCG %.4f (got %.4f)", w, off, got[i])
				}
				if got[i] < 1.0-1e-9 {
					t.Errorf("weight %.2f nDCG@10 %.4f below the 1.0 plateau", w, got[i])
				}
			}
		})
	}
}

// proximityCorpusWeights is the wider ladder TestProximityWeightEarnsItsPlace
// walks at the corpus level, reaching a deliberately excessive 10.0 so the
// recorded curve shows the boost turning from help to harm.
var proximityCorpusWeights = []float64{0, 0.3, 1.0, 3.0, 10.0}

// TestProximityWeightEarnsItsPlace is the corpus-level half of the #910
// prove-or-retire answer: it sweeps the whole en and mixed corpora (not just the
// permutation query) and asserts the boost is a net win at the shipped 0.3 —
// corpus nDCG@10 strictly above the boost-off baseline — which is what "earns
// its weight" means. It also records the far end of the ladder, where an
// oversized weight starts overturning BM25 (mixed MRR falls from 1.0 at 10.0),
// the evidence that the constant must stay modest rather than be maximized.
func TestProximityWeightEarnsItsPlace(t *testing.T) {
	byName := corporaByName(t)
	for _, name := range []string{"en", "mixed"} {
		t.Run(name, func(t *testing.T) {
			c := byName[name]
			metrics := make([]Metrics, len(proximityCorpusWeights))
			for i, w := range proximityCorpusWeights {
				idx := productionIndexWith(search.WithProximityWeight(w))
				c.IndexDocs(idx)
				metrics[i] = Evaluate(c, RankSearcher(idx))
				t.Logf("%-5s w=%5.2f nDCG@10=%.5f MRR=%.5f Recall@50=%.5f", name, w, metrics[i].NDCG10, metrics[i].MRR, metrics[i].Recall50)
			}
			off := metrics[0]     // weight 0: boost disabled
			shipped := metrics[1] // weight 0.3: the shipped default
			if !(shipped.NDCG10 > off.NDCG10) {
				t.Errorf("%s: proximity boost is not a net win at 0.3: nDCG@10 off=%.5f shipped=%.5f", name, off.NDCG10, shipped.NDCG10)
			}
			if shipped.MRR < off.MRR-1e-9 {
				t.Errorf("%s: proximity boost hurt MRR at 0.3: off=%.5f shipped=%.5f", name, off.MRR, shipped.MRR)
			}
		})
	}
}
