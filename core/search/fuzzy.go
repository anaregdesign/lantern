package search

import (
	"strings"

	"github.com/RoaringBitmap/roaring/v2"
)

// MaxTermExpansions caps how many dictionary terms a single word-channel query
// term expands to under prefix/fuzzy matching, so a hot prefix or a small
// fuzziness over a large dictionary cannot blow up a query. It mirrors Lucene's
// default multi-term rewrite cap; the exact term is always kept, then the scan
// fills the remaining budget in dictionary id order (deterministic).
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
func (idx *InvertedIndex[S, D]) buildClausesLocked(queryTerms map[Token]struct{}, opts MatchOptions) []queryClause {
	clauses := make([]queryClause, 0, len(queryTerms))
	for token := range queryTerms {
		if int(token.Class) >= numTokenClasses {
			continue
		}
		cp := &idx.classes[token.Class]
		var ids []uint32
		if token.Class == ClassWord && opts.expandsTerms() && isExpandable(token.Term) {
			ids = cp.dict.expand(token.Term, opts.PrefixTerms, opts.Fuzziness, MaxTermExpansions)
		} else if tid, ok := cp.dict.lookup(token.Term); ok {
			ids = []uint32{tid}
		}
		clauses = append(clauses, queryClause{class: token.Class, termIDs: ids})
	}
	return clauses
}

// scoreClausesLocked scores documents over expanded query clauses: each clause
// contributes the BM25 sum of its matching terms, and — when a match mode is
// active — a document's coverage counts each satisfied word clause once (via the
// clause's posting union), so prefix/fuzzy expansions of the same query word
// still count as one term. Callers hold idx.mu. It is the expansion-aware
// counterpart of scoreLocked + filterByCoverageLocked; the non-expansion path
// keeps using those so the default query is byte-for-byte unchanged.
func (idx *InvertedIndex[S, D]) scoreClausesLocked(clauses []queryClause, opts MatchOptions) map[uint32]float64 {
	scores := make(map[uint32]float64)
	var coverage map[uint32]int
	numWords := 0
	if opts.Mode != MatchAny {
		coverage = make(map[uint32]int)
	}
	for _, cl := range clauses {
		if coverage != nil && cl.class == ClassWord {
			numWords++ // count every word clause, even an unmatched typo, toward the bar
		}
		cp := &idx.classes[cl.class]
		if cp.docCount == 0 || len(cl.termIDs) == 0 {
			continue
		}
		avgLen := float64(cp.totalLen) / float64(cp.docCount)
		var union *roaring.Bitmap
		if coverage != nil && cl.class == ClassWord {
			union = roaring.New()
		}
		for _, tid := range cl.termIDs {
			pl := cp.postings[tid]
			if pl == nil {
				continue
			}
			df := pl.cardinality()
			for it := pl.docs.Iterator(); it.HasNext(); {
				ord := it.Next()
				scores[ord] += idx.scorer.Score(TermStats{
					TF:     pl.tf(ord),
					DF:     df,
					N:      cp.docCount,
					DocLen: idx.docs[ord].lengths[cl.class],
					AvgLen: avgLen,
					Class:  cl.class,
				})
			}
			if union != nil {
				union.Or(pl.docs)
			}
		}
		if union != nil {
			for it := union.Iterator(); it.HasNext(); {
				coverage[it.Next()]++
			}
		}
	}
	if coverage != nil {
		if threshold := coverageThreshold(opts, numWords); threshold > 0 {
			for ord := range scores {
				if coverage[ord] < threshold {
					delete(scores, ord)
				}
			}
		}
	}
	return scores
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

// expand returns the interned term ids matching term under the expansion flags,
// capped at limit: the exact term (if interned) first, then terms that have term
// as a prefix (when prefix is set), and terms within maxEdits edit distance
// (when maxEdits > 0). It scans the live term slice in id order, so the result
// — including which terms survive the cap — is deterministic. Callers hold the
// index lock. Intended for word terms; the caller skips CJK grams.
func (d *termDict) expand(term string, prefix bool, maxEdits, limit int) []uint32 {
	if limit <= 0 {
		return nil
	}
	out := make([]uint32, 0, min(limit, 8))
	seen := make(map[uint32]struct{})
	appendID := func(id uint32) bool {
		if _, dup := seen[id]; dup {
			return len(out) < limit
		}
		seen[id] = struct{}{}
		out = append(out, id)
		return len(out) < limit
	}
	if id, ok := d.ids[term]; ok {
		if !appendID(id) {
			return out
		}
	}
	if !prefix && maxEdits <= 0 {
		return out
	}
	qr := []rune(term)
	for id, cand := range d.terms {
		if cand == "" || cand == term {
			continue // released slot, or the exact term already added
		}
		matched := prefix && strings.HasPrefix(cand, term)
		if !matched && maxEdits > 0 {
			cr := []rune(cand)
			if dl := len(cr) - len(qr); dl <= maxEdits && -dl <= maxEdits && withinEdits(qr, cr, maxEdits) {
				matched = true
			}
		}
		if matched {
			if !appendID(uint32(id)) {
				return out
			}
		}
	}
	return out
}

// withinEdits reports whether the Levenshtein edit distance between rune slices
// a and b is at most maxEdits. It runs the classic two-row DP and bails as soon
// as every cell in a row exceeds maxEdits, so a bounded distance check over a
// large dictionary stays cheap.
func withinEdits(a, b []rune, maxEdits int) bool {
	la, lb := len(a), len(b)
	if la-lb > maxEdits || lb-la > maxEdits {
		return false
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
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
			return false
		}
		prev, curr = curr, prev
	}
	return prev[lb] <= maxEdits
}
