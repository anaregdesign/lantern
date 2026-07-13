package search

import (
	"container/heap"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/RoaringBitmap/roaring/v2"
)

// MaxTermExpansions caps how many dictionary terms a single word-channel query
// term expands to under prefix/fuzzy matching, so a hot prefix or a small
// fuzziness over a large dictionary cannot blow up a query. It mirrors Lucene's
// default multi-term rewrite cap. Selection is semantic and independent of
// dictionary ids: exact first, then prefix quality, then fuzzy edit distance.
const MaxTermExpansions = 50

// expandsTerms reports whether the options request prefix or fuzzy term
// expansion (as opposed to exact-term matching).
func (o MatchOptions) expandsTerms() bool {
	return o.PrefixTerms || o.Fuzziness > 0
}

// queryClause is one distinct analyzed query token together with the dictionary
// term ids it matches after expansion. Without expansion the ids are just the
// token's own interned id (or none, if the term is absent); with prefix/fuzzy
// expansion on a word-class token the ids include the reachable terms, capped
// at MaxTermExpansions. Coverage (match modes) treats a clause as satisfied when
// a document carries ANY of its ids, so an expanded term still counts toward its
// original query term rather than as a separate required term.
type queryClause struct {
	class   TokenClass
	termIDs []uint32
}

// buildClausesLocked turns the distinct query tokens into scoring clauses,
// expanding word-channel terms by prefix and/or edit distance when opts request
// it. Auxiliary gram tokens and CJK grams (isExpandable is false for a run of
// unbounded-script runes) are never expanded — they are fixed-width windows, so
// prefix/fuzzy widening is meaningless. Callers hold idx.mu.
func (idx *InvertedIndex[S, D]) buildClausesLocked(queryTerms []Token, opts MatchOptions) []queryClause {
	clauses, _ := idx.buildClausesTrackedLocked(queryTerms, opts, newWorkTracker(nil, Budget{}))
	return clauses
}

func (idx *InvertedIndex[S, D]) buildClausesTrackedLocked(queryTerms []Token, opts MatchOptions, work *workTracker) ([]queryClause, error) {
	clauses := make([]queryClause, 0, len(queryTerms))
	for _, token := range queryTerms {
		if err := work.check(); err != nil {
			return nil, err
		}
		if int(token.Class) >= numTokenClasses {
			continue
		}
		cp := &idx.classes[token.Class]
		var ids []uint32
		if token.Class == ClassWord && opts.expandsTerms() && isExpandable(token.Term) {
			var err error
			ids, err = cp.dict.expandTracked(token.Term, opts.PrefixTerms, opts.Fuzziness, MaxTermExpansions, work)
			if err != nil {
				return nil, err
			}
		} else if tid, ok := cp.dict.lookup(token.Term); ok {
			ids = []uint32{tid}
		}
		clauses = append(clauses, queryClause{class: token.Class, termIDs: ids})
	}
	return clauses, nil
}

// scoreClausesLocked scores documents over expanded query clauses: each clause
// contributes the BM25 sum of its matching terms, and — when a match mode is
// active — a document's coverage counts each satisfied word clause once (via the
// clause's posting union), so prefix/fuzzy expansions of the same query word
// still count as one term. Callers hold idx.mu. It is the expansion-aware
// counterpart of scoreLocked + filterByCoverageLocked; the non-expansion path
// keeps using those so the default query is byte-for-byte unchanged.
func (idx *InvertedIndex[S, D]) scoreClausesLocked(clauses []queryClause, opts MatchOptions) map[uint32]float64 {
	scores, _ := idx.scoreClausesTrackedLocked(clauses, opts, newWorkTracker(nil, Budget{}))
	return scores
}

func (idx *InvertedIndex[S, D]) scoreClausesTrackedLocked(clauses []queryClause, opts MatchOptions, work *workTracker) (map[uint32]float64, error) {
	scores := make(map[uint32]float64)
	var coverage map[uint32]int
	numWords := 0
	if opts.Mode != MatchAny {
		coverage = make(map[uint32]int)
	}
	for _, cl := range clauses {
		if err := work.check(); err != nil {
			return nil, err
		}
		if coverage != nil && cl.class == ClassWord {
			numWords++ // count every word clause, even an unmatched typo, toward the bar
		}
		cp := &idx.classes[cl.class]
		if cp.docCount == 0 || len(cl.termIDs) == 0 {
			continue
		}
		var union *roaring.Bitmap
		if coverage != nil && cl.class == ClassWord {
			union = roaring.New()
		}
		for _, tid := range cl.termIDs {
			pl := cp.postings[tid]
			if pl == nil {
				continue
			}
			for it := pl.docs.Iterator(); it.HasNext(); {
				if err := work.visit(WorkPostingVisits, 1); err != nil {
					return nil, err
				}
				ord := it.Next()
				if union != nil {
					union.Add(ord)
				}
				addScore(scores, ord, idx.termScoreLocked(ord, cl.class, pl))
			}
		}
		if union != nil {
			for it := union.Iterator(); it.HasNext(); {
				if err := work.visit(WorkPostingVisits, 1); err != nil {
					return nil, err
				}
				coverage[it.Next()]++
			}
		}
	}
	dropNonFiniteScores(scores)
	if coverage != nil {
		if threshold := coverageThreshold(opts, numWords); threshold > 0 {
			for ord := range scores {
				if err := work.check(); err != nil {
					return nil, err
				}
				if coverage[ord] < threshold {
					delete(scores, ord)
				}
			}
		}
	}
	return scores, nil
}

// isExpandable reports whether a word-channel term should be widened by
// prefix/fuzzy expansion. A term made entirely of unbounded-script runes is a
// CJK gram — a fixed-width window, not a word — so it is exempt (#891); any term
// carrying at least one space-delimited-script rune is a word and expandable.
func isExpandable(term string) bool {
	if term == "" {
		return false
	}
	for _, r := range term {
		if !isUnboundedScript(r) {
			return true
		}
	}
	return false
}

// expansionKind orders non-exact candidates when prefix and fuzzy expansion are
// combined. A literal continuation is stronger than a typo candidate; a term
// matching both is classified once as prefix.
type expansionKind uint8

const (
	expansionPrefix expansionKind = iota
	expansionFuzzy
)

type expansionCandidate struct {
	id      uint32
	term    string
	kind    expansionKind
	quality int // prefix rune-extension length or Levenshtein distance
}

// betterExpansion defines the history-independent expansion order after the
// exact term: kind, quality, then normalized UTF-8 term ascending.
func betterExpansion(a, b expansionCandidate) bool {
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	if a.quality != b.quality {
		return a.quality < b.quality
	}
	return a.term < b.term
}

// expansionTopHeap keeps its weakest retained candidate at the root.
type expansionTopHeap []expansionCandidate

func (h expansionTopHeap) Len() int { return len(h) }
func (h expansionTopHeap) Less(i, j int) bool {
	return betterExpansion(h[j], h[i])
}
func (h expansionTopHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *expansionTopHeap) Push(x any)   { *h = append(*h, x.(expansionCandidate)) }
func (h *expansionTopHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func retainExpansion(h *expansionTopHeap, candidate expansionCandidate, limit int) {
	if len(*h) < limit {
		if *h == nil {
			*h = make(expansionTopHeap, 0, limit)
		}
		*h = append(*h, candidate)
		if len(*h) == limit {
			heap.Init(h)
		}
		return
	}
	if betterExpansion(candidate, (*h)[0]) {
		(*h)[0] = candidate
		heap.Fix(h, 0)
	}
}

// expand returns interned term ids capped at limit in a semantic order
// independent of term ids and mutation history: exact first; prefix matches by
// (rune extension length, term); then fuzzy-only matches by (edit distance,
// term). Callers hold the index lock. Intended for word terms; the caller skips
// CJK grams.
func (d *termDict) expand(term string, prefix bool, maxEdits, limit int) []uint32 {
	out, _ := d.expandTracked(term, prefix, maxEdits, limit, newWorkTracker(nil, Budget{}))
	return out
}

func (d *termDict) expandTracked(term string, prefix bool, maxEdits, limit int, work *workTracker) ([]uint32, error) {
	if limit <= 0 {
		return nil, nil
	}
	out := make([]uint32, 0, min(limit, 8))
	if id, ok := d.ids[term]; ok {
		out = append(out, id)
		if len(out) == limit {
			return out, nil
		}
	}
	if !prefix && maxEdits <= 0 {
		return out, nil
	}

	// Fuzzy matching converts each length-compatible candidate to runes and runs
	// a two-row Levenshtein DP. ASCII words up to 64 bytes use Myers' bit-vector
	// algorithm instead, reducing each candidate to one pass of word-sized
	// operations. The DP scratch is allocated lazily only if a Unicode candidate
	// needs it. A bounded heap retains only the best remaining terms, so semantic
	// selection uses O(limit) transient memory.
	var asciiMasks [utf8.RuneSelf]uint64
	useASCII := len(term) > 0 && len(term) <= 64 && isASCII(term)
	queryLen := len(term)
	var qr []rune
	if useASCII {
		for i := range term {
			asciiMasks[term[i]] |= uint64(1) << i
		}
	} else {
		qr = []rune(term)
		queryLen = len(qr)
	}
	var candRunes []rune
	var dpPrev, dpCurr []int
	ensureDPScratch := func() {
		if dpPrev != nil {
			return
		}
		if qr == nil {
			qr = []rune(term)
		}
		candRunes = make([]rune, 0, queryLen+maxEdits)
		dpPrev = make([]int, queryLen+maxEdits+1)
		dpCurr = make([]int, queryLen+maxEdits+1)
	}
	remaining := limit - len(out)
	var best expansionTopHeap
	for id, cand := range d.terms {
		if err := work.visit(WorkDictionaryVisits, 1); err != nil {
			return nil, err
		}
		if cand == "" || cand == term {
			continue
		}
		if prefix && strings.HasPrefix(cand, term) {
			retainExpansion(&best, expansionCandidate{
				id:      uint32(id),
				term:    cand,
				kind:    expansionPrefix,
				quality: utf8.RuneCountInString(cand) - queryLen,
			}, remaining)
			continue
		}
		if maxEdits <= 0 {
			continue
		}
		candidateASCII := useASCII && isASCII(cand)
		cl := len(cand)
		if !candidateASCII {
			cl = utf8.RuneCountInString(cand)
		}
		if cl-queryLen > maxEdits || queryLen-cl > maxEdits {
			continue
		}
		if candidateASCII {
			if distance, ok := asciiLevenshteinWithin(len(term), cand, maxEdits, &asciiMasks); ok {
				retainExpansion(&best, expansionCandidate{
					id:      uint32(id),
					term:    cand,
					kind:    expansionFuzzy,
					quality: distance,
				}, remaining)
			}
			continue
		}
		ensureDPScratch()
		candRunes = candRunes[:0]
		for _, r := range cand {
			candRunes = append(candRunes, r)
		}
		distance, ok, err := editDistanceWithinRowsTracked(qr, candRunes, maxEdits, dpPrev, dpCurr, work)
		if err != nil {
			return nil, err
		}
		if ok {
			retainExpansion(&best, expansionCandidate{
				id:      uint32(id),
				term:    cand,
				kind:    expansionFuzzy,
				quality: distance,
			}, remaining)
		}
	}
	sort.Slice(best, func(i, j int) bool { return betterExpansion(best[i], best[j]) })
	if err := work.visit(WorkExpansionRetained, int64(len(best))); err != nil {
		return nil, err
	}
	for _, candidate := range best {
		out = append(out, candidate.id)
	}
	return out, nil
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiLevenshteinWithin computes the exact distance from the ASCII pattern
// represented by masks to candidate when it is at most maxEdits, using Myers'
// bit-vector algorithm. The caller guarantees 1 <= patternLen <= 64 and an
// ASCII candidate. A partial score may still fall as remaining candidate bytes
// arrive, so rejection uses the safe lower bound score - remaining.
func asciiLevenshteinWithin(patternLen int, candidate string, maxEdits int, masks *[utf8.RuneSelf]uint64) (int, bool) {
	positive := ^uint64(0)
	negative := uint64(0)
	score := patternLen
	highBit := uint64(1) << (patternLen - 1)
	for i := range candidate {
		equal := masks[candidate[i]]
		vertical := equal | negative
		horizontal := (((equal & positive) + positive) ^ positive) | equal
		positiveHorizontal := negative | ^(horizontal | positive)
		negativeHorizontal := positive & horizontal
		if positiveHorizontal&highBit != 0 {
			score++
		} else if negativeHorizontal&highBit != 0 {
			score--
		}
		positiveHorizontal = (positiveHorizontal << 1) | 1
		negativeHorizontal <<= 1
		positive = negativeHorizontal | ^(vertical | positiveHorizontal)
		negative = positiveHorizontal & vertical
		if score-(len(candidate)-i-1) > maxEdits {
			return 0, false
		}
	}
	return score, score <= maxEdits
}

// withinEdits reports whether the Levenshtein edit distance between rune slices
// a and b is at most maxEdits. It allocates a fresh DP row pair; the hot
// dictionary scan uses withinEditsRows with reusable buffers instead.
func withinEdits(a, b []rune, maxEdits int) bool {
	if len(a)-len(b) > maxEdits || len(b)-len(a) > maxEdits {
		return false
	}
	n := len(b) + 1
	_, ok := editDistanceWithinRows(a, b, maxEdits, make([]int, n), make([]int, n))
	return ok
}

// withinEditsRows is withinEdits over caller-provided scratch rows so a large
// fuzzy scan reuses two buffers instead of allocating a DP pair per candidate.
// prev and curr must each be at least len(b)+1 long (the fuzzy scan sizes them
// to len(query)+maxEdits+1, which the rune-length gate guarantees is enough).
// It runs the classic two-row DP and bails as soon as every cell in a row
// exceeds maxEdits, so a bounded distance check over a large dictionary stays
// cheap. Contents are fully overwritten each call, so stale scratch is fine.
func withinEditsRows(a, b []rune, maxEdits int, prev, curr []int) bool {
	_, ok := editDistanceWithinRows(a, b, maxEdits, prev, curr)
	return ok
}

// editDistanceWithinRows returns the exact Levenshtein distance when it is at
// most maxEdits, using caller-provided scratch rows.
func editDistanceWithinRows(a, b []rune, maxEdits int, prev, curr []int) (int, bool) {
	distance, ok, _ := editDistanceWithinRowsTracked(a, b, maxEdits, prev, curr, newWorkTracker(nil, Budget{}))
	return distance, ok
}

func editDistanceWithinRowsTracked(a, b []rune, maxEdits int, prev, curr []int, work *workTracker) (int, bool, error) {
	la, lb := len(a), len(b)
	if la-lb > maxEdits || lb-la > maxEdits {
		return 0, false, nil
	}
	prev, curr = prev[:lb+1], curr[:lb+1]
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		if err := work.check(); err != nil {
			return 0, false, err
		}
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > maxEdits {
			return 0, false, nil
		}
		prev, curr = curr, prev
	}
	distance := prev[lb]
	return distance, distance <= maxEdits, nil
}
