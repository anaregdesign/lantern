package search

import (
	"bytes"
	"container/heap"
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/anaregdesign/tarp"
)

const (
	diskReadBytes   = 128 << 20
	diskCursorBytes = 8 << 20
	diskQueryBytes  = 16 << 10
	diskQueryTerms  = 1024
	diskResultLimit = 10_000
)

// Search evaluates a query at Source().Watermark with fixed-cut corpus
// statistics. The caller decides whether that base can serve its current graph
// cut. Any cancellation, budget failure, or malformed row discards all results.
func (idx *DiskIndex) Search(ctx context.Context, query string, k int, accept func(string) bool, opts MatchOptions, phrase bool, budget Budget) ([]Result[string], Stats, error) {
	return idx.SearchAt(ctx, query, k, accept, opts, phrase, budget, idx.footer.Source.Watermark)
}

// SearchAt evaluates the immutable base while its liveness statistics remain
// valid. The caller still verifies the graph source cut and authority.
func (idx *DiskIndex) SearchAt(ctx context.Context, query string, k int, accept func(string) bool, opts MatchOptions, phrase bool, budget Budget, now time.Time) ([]Result[string], Stats, error) {
	if now.Before(idx.footer.Source.Watermark) || (!idx.footer.FirstDeadline.IsZero() && !idx.footer.FirstDeadline.After(now)) {
		return nil, Stats{}, ErrIndexIncomplete
	}
	budget.MaxQueryTerms = diskLimit(budget.MaxQueryTerms, diskQueryTerms)
	budget.MaxDictionaryVisits = diskLimit(budget.MaxDictionaryVisits, 1_000_000)
	budget.MaxPostingVisits = diskLimit(budget.MaxPostingVisits, 10_000_000)
	budget.MaxPositionVisits = diskLimit(budget.MaxPositionVisits, 10_000_000)
	budget.MaxDiskReadBytes = diskLimit(budget.MaxDiskReadBytes, diskReadBytes)
	work := newWorkTracker(ctx, budget)
	ctx = work.ctx
	if err := work.check(); err != nil {
		return nil, work.stats, err
	}
	if k <= 0 {
		return nil, work.stats, nil
	}
	if k > diskResultLimit {
		return nil, work.stats, diskInvalid("result limit exceeds disk profile")
	}
	if len(query) > diskQueryBytes {
		return nil, work.stats, &BudgetExceededError{Kind: WorkQueryBytes, Limit: diskQueryBytes}
	}
	select {
	case idx.readers <- struct{}{}:
		defer func() { <-idx.readers }()
	case <-ctx.Done():
		return nil, work.stats, ctx.Err()
	}
	started := time.Now()
	tokens := idx.config.analyzeQuery(query)
	if err := work.visit(WorkQueryBytes, int64(len(query))); err != nil {
		return nil, work.stats, err
	}
	if err := work.visit(WorkQueryTokens, int64(len(tokens))); err != nil {
		return nil, work.stats, err
	}
	if phrase {
		tokens = diskPhraseTokens(tokens)
	} else {
		tokens = queryTermsForMatchOptions(distinctQueryTerms(tokens), opts)
	}
	if err := work.visit(WorkQueryClauses, int64(len(tokens))); err != nil {
		return nil, work.stats, err
	}
	if err := work.visit(WorkQueryTerms, int64(len(tokens))); err != nil {
		return nil, work.stats, err
	}
	work.stats.AnalysisDuration = time.Since(started)
	if len(tokens) == 0 {
		return nil, work.stats, nil
	}

	snap, err := idx.db.Snapshot()
	if err != nil {
		return nil, work.stats, err
	}
	defer snap.Close()
	q := &diskQuery{index: idx, snap: snap, work: work, terms: make(map[Token]*diskQueryTerm)}
	value, err := q.required(diskKey(idx.base, diskFooterRow))
	if err != nil {
		return nil, work.stats, err
	}
	if !bytes.Equal(value, idx.seal) {
		return nil, work.stats, diskInvalid("commit footer changed")
	}
	started = time.Now()
	plan, err := q.plan(tokens, opts, phrase)
	work.stats.ExpansionDuration = time.Since(started)
	if err != nil || plan.impossible {
		return nil, work.stats, err
	}
	started = time.Now()
	results, err := q.execute(plan, k, accept)
	work.stats.SelectionDuration = time.Since(started)
	return results, work.stats, err
}

func diskLimit(limit, upper int64) int64 {
	if limit <= 0 {
		return upper
	}
	return min(limit, upper)
}

func diskPhraseTokens(tokens []Token) []Token {
	terms := phraseTermsFromTokens(tokens)
	phrase := make([]Token, 0, len(terms))
	for _, term := range terms {
		phrase = append(phrase, Token{Class: ClassWord, Term: term})
	}
	return phrase
}

type diskQuery struct {
	index       *DiskIndex
	snap        *tarp.Snapshot
	work        *workTracker
	terms       map[Token]*diskQueryTerm
	cursorBytes int
}

func (q *diskQuery) read(key []byte) ([]byte, error) {
	if err := q.work.visit(WorkDiskReadBytes, int64(len(key))); err != nil {
		return nil, err
	}
	value, err := q.snap.GetContext(q.work.ctx, key)
	if err != nil {
		return nil, err
	}
	if err := q.work.visit(WorkDiskReadBytes, int64(len(value))); err != nil {
		return nil, err
	}
	return value, nil
}

func (q *diskQuery) required(key []byte) ([]byte, error) {
	value, err := q.read(key)
	if errors.Is(err, tarp.ErrNotFound) {
		return nil, diskInvalid("missing published index row")
	}
	return value, err
}

type diskQueryTerm struct {
	token   Token
	meta    diskTermValue
	posting diskPosting
}

type diskPostingCursor struct {
	term     *diskQueryTerm
	prefix   []byte
	lower    []byte
	upper    []byte
	block    diskDecoder
	bytes    int
	count    uint64
	blockID  uint64
	previous uint64
	blocks   uint64
	seen     uint64
	fieldDF  [numDocumentFields]int
}

func (c *diskPostingCursor) advance(q *diskQuery) (bool, error) {
	if c.count == 0 {
		if !c.block.success() {
			return false, diskInvalid("trailing posting block bytes")
		}
		q.cursorBytes -= c.bytes
		c.bytes, c.block = 0, diskDecoder{}
		row, err, ok := c.nextBlock(q)
		if err != nil {
			return false, err
		}
		if !ok {
			c.term.posting = diskPosting{}
			if c.blocks != c.term.meta.blocks || c.seen != c.term.meta.documents || c.fieldDF != c.term.meta.df {
				return false, diskInvalid("incomplete posting stream")
			}
			return false, nil
		}
		if err := c.openBlock(q, row); err != nil {
			return false, err
		}
	}
	if err := q.work.visit(WorkPostingVisits, 1); err != nil {
		return false, err
	}
	gap := c.block.number()
	posting := decodeDiskPosting(&c.block)
	if c.block.bad || gap == 0 || gap > math.MaxUint64-c.previous {
		return false, diskInvalid("malformed posting")
	}
	posting.id = c.previous + gap
	if c.previous == 0 && posting.id != c.blockID || posting.id > q.index.footer.Documents || c.seen == c.term.meta.documents {
		return false, diskInvalid("posting ID out of bounds")
	}
	for field, frequency := range posting.frequency {
		if frequency != 0 {
			c.fieldDF[field]++
		}
	}
	c.count--
	c.seen++
	c.previous = posting.id
	c.term.posting = posting
	return true, nil
}

func (c *diskPostingCursor) nextBlock(q *diskQuery) (tarp.Entry, error, bool) {
	for row, err := range q.snap.ScanRange(q.work.ctx, c.lower, c.upper) {
		if err != nil {
			return tarp.Entry{}, err, false
		}
		_, c.lower = tarp.PrefixRange(row.Key)
		return row, nil, true
	}
	return tarp.Entry{}, q.work.check(), false
}

func (c *diskPostingCursor) openBlock(q *diskQuery, row tarp.Entry) error {
	if err := q.work.visit(WorkDiskReadBytes, int64(len(row.Key)+len(row.Value))); err != nil {
		return err
	}
	if len(row.Value) > diskPostingBlockSize {
		return diskInvalid("posting block exceeds size limit")
	}
	if len(row.Value) > diskCursorBytes-q.cursorBytes {
		return &BudgetExceededError{Kind: WorkDiskReadBytes, Limit: diskCursorBytes}
	}
	id, err := diskNumberKey(row.Key, c.prefix)
	if err != nil {
		return err
	}
	c.block = diskDecoder{data: row.Value}
	count := c.block.number()
	if c.block.bad || count == 0 || count > diskPostingBlockDocs || id <= c.previous || c.blocks == c.term.meta.blocks {
		return diskInvalid("invalid posting block bounds")
	}
	c.blockID, c.previous, c.count, c.bytes = id, 0, count, len(row.Value)
	c.blocks++
	q.cursorBytes += c.bytes
	return nil
}

type diskCursorHeap []*diskPostingCursor

func (h diskCursorHeap) Len() int           { return len(h) }
func (h diskCursorHeap) Less(i, j int) bool { return h[i].term.posting.id < h[j].term.posting.id }
func (h diskCursorHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *diskCursorHeap) Push(x any)        { *h = append(*h, x.(*diskPostingCursor)) }
func (h *diskCursorHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return x
}

func (q *diskQuery) execute(plan diskPlan, k int, accept func(string) bool) ([]Result[string], error) {
	var cursors diskCursorHeap
	for _, term := range q.terms {
		if term == nil {
			continue
		}
		prefix := diskKey(q.index.base, diskPostingRow, uint8(term.token.Class), term.token.Term)
		lower, upper := tarp.PrefixRange(prefix)
		cursor := &diskPostingCursor{term: term, prefix: prefix, lower: lower, upper: upper}
		more, err := cursor.advance(q)
		if err != nil {
			return nil, err
		}
		if more {
			cursors = append(cursors, cursor)
		}
	}
	heap.Init(&cursors)
	results := topKHeap[string]{entries: make([]Result[string], 0, k), better: q.index.config.betterResult}
	for len(cursors) != 0 {
		id := cursors[0].term.posting.id
		if err := q.selectCandidate(plan, &results, k, id, accept); err != nil {
			return nil, err
		}
		for len(cursors) != 0 && cursors[0].term.posting.id == id {
			cursor := heap.Pop(&cursors).(*diskPostingCursor)
			more, err := cursor.advance(q)
			if err != nil {
				return nil, err
			}
			if more {
				heap.Push(&cursors, cursor)
			}
		}
	}
	if len(results.entries) == 0 {
		return nil, nil
	}
	sort.Slice(results.entries, func(i, j int) bool { return results.better(results.entries[i], results.entries[j]) })
	return results.entries, nil
}

func (q *diskQuery) selectCandidate(plan diskPlan, results *topKHeap[string], k int, id uint64, accept func(string) bool) error {
	if err := q.work.candidateVisit(); err != nil {
		return err
	}
	value, err := q.required(diskKey(q.index.base, diskDocIDRow, id))
	if err != nil {
		return err
	}
	doc, err := decodeDiskDoc(value)
	if err != nil {
		return err
	}
	if !expirationLiveAt(doc.expiration, q.index.footer.Source.Watermark) {
		return diskInvalid("expired document in live posting")
	}
	contains := candidateFilterFromContext[string](q.work.ctx)
	if (contains != nil && !contains(doc.key)) || (accept != nil && !accept(doc.key)) {
		return q.work.candidateSkip()
	}
	score, matched, err := q.score(plan, doc, id)
	if err != nil {
		return err
	}
	if !matched {
		return q.work.candidateSkip()
	}
	result := Result[string]{ID: doc.key, Score: score}
	if len(results.entries) < k {
		heap.Push(results, result)
	} else if results.better(result, results.entries[0]) {
		results.entries[0] = result
		heap.Fix(results, 0)
	}
	return nil
}

func (q *diskQuery) score(plan diskPlan, doc diskDocValue, id uint64) (float64, bool, error) {
	var score float64
	var coverage int
	var matched bool
	for _, clause := range plan.clauses {
		clauseMatched := false
		for _, term := range clause.terms {
			if err := q.work.visit(WorkPostingVisits, 1); err != nil {
				return 0, false, err
			}
			if term.posting.id != id {
				continue
			}
			clauseMatched, matched = true, true
			contribution, err := q.scoreTerm(term, doc)
			if err != nil {
				return 0, false, err
			}
			score += contribution
			if math.IsNaN(score) || math.IsInf(score, 0) {
				return 0, false, nil
			}
		}
		if clause.class == ClassWord && clauseMatched {
			coverage++
		}
	}
	if !matched || coverage < plan.threshold {
		return 0, false, nil
	}
	if plan.phrase {
		match, err := q.phraseMatches(plan.exact, id)
		return score, match, err
	}
	if q.index.config.positions && q.index.config.proximityWeight != 0 && len(plan.exact) >= 2 {
		bonus, err := q.proximity(plan.exact, id)
		if err != nil {
			return 0, false, err
		}
		score += q.index.config.proximityWeight * bonus
	}
	return score, !math.IsNaN(score) && !math.IsInf(score, 0), nil
}

func (q *diskQuery) scoreTerm(term *diskQueryTerm, doc diskDocValue) (float64, error) {
	class := term.token.Class
	var score float64
	for field, frequency := range term.posting.frequency {
		if frequency == 0 {
			continue
		}
		stats := q.index.footer.Stats[class][field]
		length := doc.lengths[class][field]
		if term.meta.df[field] == 0 || stats.Count == 0 || length < int(frequency) || uint64(length) > stats.Length {
			return 0, diskInvalid("posting contradicts corpus statistics")
		}
		score += q.index.config.scorer.Score(TermStats{
			TF: int(frequency), DF: term.meta.df[field], N: int(stats.Count),
			DocLen: length, AvgLen: float64(stats.Length) / float64(stats.Count),
			Class: class, Field: FieldID(field),
		})
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return math.NaN(), nil
		}
	}
	return score, nil
}

func (q *diskQuery) phraseMatches(terms []*diskQueryTerm, id uint64) (bool, error) {
	for field := range numDocumentFields {
		allPresent := true
		for _, term := range terms {
			if term.posting.id != id || term.posting.frequency[field] == 0 {
				allPresent = false
				break
			}
		}
		if !allPresent {
			continue
		}
		positions, err := q.positionsFor(terms, id, int(field))
		if err != nil {
			return false, err
		}
		if len(positions) == len(terms) && phraseAdjacentDecoded(positions) {
			return true, nil
		}
	}
	return false, nil
}

func (q *diskQuery) proximity(terms []*diskQueryTerm, id uint64) (float64, error) {
	var best float64
	var scratch executorScratch
	for field := range numDocumentFields {
		positions, err := q.positionsFor(terms, id, int(field))
		if err != nil {
			return 0, err
		}
		bonus, err := scratch.proximityBonus(positions, q.work)
		if err != nil {
			return 0, err
		}
		best = max(best, bonus)
	}
	return best, nil
}

func (q *diskQuery) positionsFor(terms []*diskQueryTerm, id uint64, field int) ([][]uint64, error) {
	out := make([][]uint64, 0, len(terms))
	cache := make(map[*diskQueryTerm][]uint64, len(terms))
	for _, term := range terms {
		if term.posting.id != id || term.posting.positions[field] == 0 {
			continue
		}
		count := term.posting.positions[field]
		if count > uint64(q.work.budget.MaxPositionVisits) {
			return nil, &BudgetExceededError{Kind: WorkPositionVisits, Limit: q.work.budget.MaxPositionVisits}
		}
		if err := q.work.visit(WorkPositionVisits, int64(count)); err != nil {
			return nil, err
		}
		positions := cache[term]
		if positions == nil {
			var err error
			positions, err = q.readPositions(term.token, id, field, count, term.posting.inline[field])
			if err != nil {
				return nil, err
			}
			cache[term] = positions
		}
		out = append(out, positions)
	}
	return out, nil
}

func (q *diskQuery) readPositions(token Token, id uint64, field int, count uint64, inline []byte) ([]uint64, error) {
	positions := make([]uint64, 0, min(count, diskPositionChunk))
	if len(inline) != 0 {
		return decodeDiskPositions(positions, inline, count)
	}
	for chunk := uint64(0); count != 0; chunk++ {
		key := diskKey(q.index.base, diskPositionRow, uint8(token.Class), token.Term, id, uint8(field), chunk)
		value, err := q.required(key)
		if err != nil {
			return nil, err
		}
		n := min(count, diskPositionChunk)
		positions, err = decodeDiskPositions(positions, value, n)
		if err != nil {
			return nil, err
		}
		count -= n
	}
	return positions, nil
}

func decodeDiskPositions(positions []uint64, value []byte, count uint64) ([]uint64, error) {
	if count > diskPositionChunk || uint64(len(value)) < count || uint64(len(value)) > count*10 {
		return nil, diskInvalid("invalid position chunk length")
	}
	d := diskDecoder{data: value}
	var position uint64
	for range count {
		gap := d.number()
		if gap > math.MaxUint64-position {
			return nil, diskInvalid("position gap overflows")
		}
		position += gap
		if len(positions) != 0 && position <= positions[len(positions)-1] {
			return nil, diskInvalid("unordered positions")
		}
		positions = append(positions, position)
	}
	if !d.success() {
		return nil, diskInvalid("malformed position chunk")
	}
	return positions, nil
}
