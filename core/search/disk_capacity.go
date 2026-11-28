package search

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"math"
	"time"

	"github.com/anaregdesign/tarp"
)

// diskCapacity counts distinct class/term entries, per-field postings and
// primary-word positions, matching InvertedIndex admission's units.
type diskCapacity struct {
	Terms     int64
	Postings  int64
	Positions int64
}

func (c *diskCapacity) addPosting(positions uint64) error {
	if c.Postings == math.MaxInt64 || positions > uint64(math.MaxInt64-c.Positions) {
		return diskInvalid("index capacity counters overflow")
	}
	c.Postings++
	c.Positions += int64(positions)
	return nil
}

func (c *diskCapacity) removePosting(positions uint64) error {
	if c.Postings <= 0 || c.Positions < 0 || positions > uint64(c.Positions) {
		return diskInvalid("index capacity counters underflow")
	}
	c.Postings--
	c.Positions -= int64(positions)
	return nil
}

func (limits SearchAnalysisLimits) hasDiskAggregateLimits() bool {
	return limits.MaxLiveTerms > 0 || limits.MaxLivePostings > 0 || limits.MaxPositionEntries > 0
}

func (c diskCapacity) validateLimits(limits SearchAnalysisLimits) error {
	if err := limitError(LimitLiveTerms, c.Terms, int64(limits.MaxLiveTerms)); err != nil {
		return err
	}
	if err := limitError(LimitLivePostings, c.Postings, limits.MaxLivePostings); err != nil {
		return err
	}
	return limitError(LimitPositionEntries, c.Positions, limits.MaxPositionEntries)
}

func (f diskFooter) validateCapacity() error {
	c := f.Capacity
	if f.LiveDocuments > f.Documents || f.Deadlines > f.LiveDocuments || f.LiveDocuments > uint64(max(c.Postings, 0)) {
		return diskInvalid("invalid document or deadline count")
	}
	if c.Terms < 0 || c.Postings < c.Terms || c.Positions < 0 || (c.Terms == 0) != (c.Postings == 0) {
		return diskInvalid("invalid index capacity")
	}
	var allTokens, wordTokens uint64
	for class, fields := range f.Stats {
		for _, field := range fields {
			if field.Length > math.MaxUint64-allTokens {
				return diskInvalid("corpus length overflows")
			}
			allTokens += field.Length
			if TokenClass(class) == ClassWord {
				wordTokens += field.Length
			}
		}
	}
	if uint64(c.Postings) > allTokens || (c.Postings == 0) != (allTokens == 0) ||
		(f.Positions && uint64(c.Positions) != wordTokens) || (!f.Positions && c.Positions != 0) {
		return diskInvalid("index capacity disagrees with corpus statistics")
	}
	return nil
}

func (q *diskQuery) effectiveCapacity() (diskCapacity, error) {
	result := q.capacity
	adjust := func(token Token) error {
		key := diskKey(q.index.base, diskTermRow, uint8(token.Class), token.Term)
		value, err := q.read(key)
		var stored diskTermValue
		present := err == nil
		if !errors.Is(err, tarp.ErrNotFound) {
			if err != nil {
				return err
			}
			stored, err = decodeDiskTerm(value)
			if err != nil {
				return err
			}
		}
		_, live, err := q.liveDF(token, stored)
		if err != nil {
			return err
		}
		if present && !live {
			if result.Terms == 0 {
				return diskInvalid("index term capacity underflows")
			}
			result.Terms--
		}
		if !present && live {
			if result.Terms == math.MaxInt64 {
				return diskInvalid("index term capacity overflows")
			}
			result.Terms++
		}
		return nil
	}
	for token := range q.overlay.removedDF {
		if err := adjust(token); err != nil {
			return diskCapacity{}, err
		}
	}
	for token := range q.overlay.terms {
		if _, done := q.overlay.removedDF[token]; !done {
			if err := adjust(token); err != nil {
				return diskCapacity{}, err
			}
		}
	}
	if result.Terms > result.Postings || (result.Terms == 0) != (result.Postings == 0) {
		return diskCapacity{}, diskInvalid("effective corpus capacity is inconsistent")
	}
	return result, nil
}

func (q *diskQuery) validateCapacity() error {
	limits := q.index.config.limits
	if !limits.hasDiskAggregateLimits() {
		return nil
	}
	capacity, err := q.effectiveCapacity()
	if err != nil {
		return err
	}
	if err := capacity.validateLimits(limits); err != nil {
		return errors.Join(ErrIndexIncomplete, err)
	}
	return nil
}

// DiskAdmission pins one certified index snapshot across repeatable Graph
// preparation. Close it when the enclosing mutation has reached an outcome.
type DiskAdmission struct {
	index    *DiskIndex
	snapshot *tarp.Snapshot
}

// PinAdmission captures and verifies the selected derived footer before reads.
func (idx *DiskIndex) PinAdmission(ctx context.Context) (*DiskAdmission, error) {
	snapshot, err := idx.db.Snapshot()
	if err != nil {
		return nil, err
	}
	value, err := snapshot.GetContext(ctx, diskKey(idx.base, diskFooterRow))
	if err != nil {
		_ = snapshot.Close()
		return nil, errors.Join(ErrIndexIncomplete, err)
	}
	if !bytes.Equal(value, idx.seal) {
		_ = snapshot.Close()
		return nil, ErrIndexIncomplete
	}
	return &DiskAdmission{index: idx, snapshot: snapshot}, nil
}

func (admission *DiskAdmission) Close() error { return admission.snapshot.Close() }

// ValidateAdmission evaluates the complete final batch against an exact
// source snapshot and its verified current overlay. Streams must contain
// strictly ascending, unique final postimages. A proposed key overrides the
// current key, and a nil Document is a deletion. This method does not write
// rows. The owner keeps the authoritative write exclusion through publication.
func (idx *DiskIndex) ValidateAdmission(ctx context.Context, now time.Time, current, proposed iter.Seq2[DiskDocument, error], budget Budget) error {
	view, err := idx.PinAdmission(ctx)
	if err != nil {
		return err
	}
	defer view.Close()
	return view.Validate(ctx, now, current, proposed, budget)
}

// Validate evaluates the same exact batch as ValidateAdmission on the
// already pinned index. It can be called again if Graph repeats preparation.
func (admission *DiskAdmission) Validate(ctx context.Context, now time.Time, current, proposed iter.Seq2[DiskDocument, error], budget Budget) error {
	idx := admission.index
	if now.Before(idx.footer.Source.Watermark) {
		return ErrIndexIncomplete
	}
	work := newWorkTracker(ctx, diskBoundBudget(budget))
	if err := work.check(); err != nil {
		return err
	}
	if err := work.visit(WorkDiskReadBytes, int64(len(idx.seal))); err != nil {
		return err
	}
	q := diskQuery{
		index: idx, snap: admission.snapshot, work: work, now: now, stats: idx.footer.Stats,
		capacity: idx.footer.Capacity,
	}
	if err := q.applyChanges(mergeDiskPostimages(current, proposed)); err != nil {
		return err
	}
	if !idx.config.limits.hasDiskAggregateLimits() {
		return work.check()
	}
	if err := q.applyDueDeadlines(); err != nil {
		return err
	}
	capacity, err := q.effectiveCapacity()
	if err != nil {
		return err
	}
	return capacity.validateLimits(idx.config.limits)
}

func mergeDiskPostimages(current, proposed iter.Seq2[DiskDocument, error]) iter.Seq2[DiskDocument, error] {
	return func(yield func(DiskDocument, error) bool) {
		left := openDiskChangeStream(current)
		defer left.stop()
		right := openDiskChangeStream(proposed)
		defer right.stop()
		left.advance()
		right.advance()
		for left.active || right.active {
			if left.err != nil {
				yield(DiskDocument{}, left.err)
				return
			}
			if right.err != nil {
				yield(DiskDocument{}, right.err)
				return
			}
			if !right.active || (left.active && left.doc.Key < right.doc.Key) {
				if !yield(left.doc, nil) {
					return
				}
				left.advance()
				continue
			}
			if !yield(right.doc, nil) {
				return
			}
			if left.active && left.doc.Key == right.doc.Key {
				left.advance()
			}
			right.advance()
		}
	}
}

type diskChangeStream struct {
	next   func() (DiskDocument, error, bool)
	stop   func()
	prior  string
	doc    DiskDocument
	err    error
	active bool
}

func openDiskChangeStream(stream iter.Seq2[DiskDocument, error]) diskChangeStream {
	if stream == nil {
		stream = func(func(DiskDocument, error) bool) {}
	}
	next, stop := iter.Pull2(stream)
	return diskChangeStream{next: next, stop: stop}
}

func (s *diskChangeStream) advance() {
	s.doc, s.err, s.active = s.next()
	if !s.active || s.err != nil {
		return
	}
	if s.doc.Key <= s.prior {
		s.err = diskInvalid("invalid or unordered admission postimage")
	}
	s.prior = s.doc.Key
}

func (q *diskQuery) effectiveDocumentCounts() (uint64, uint64, error) {
	footer := q.index.footer
	removed := uint64(len(q.overlay.excluded))
	if removed > footer.LiveDocuments || q.overlay.removedDeadlines > footer.Deadlines {
		return 0, 0, diskInvalid("document overlay counters underflow")
	}
	live := footer.LiveDocuments - removed + uint64(len(q.overlay.addedDocs))
	deadlines := footer.Deadlines - q.overlay.removedDeadlines
	for _, doc := range q.overlay.addedDocs {
		if !doc.expiration.IsZero() {
			deadlines++
		}
	}
	return live, deadlines, nil
}

// InspectAtWithChanges reports the logical index at a verified Graph cut and
// the physical cardinalities of its materialized base. RAM-only estimates are
// left unset. Malformed or incomplete views never report healthy.
func (idx *DiskIndex) InspectAtWithChanges(ctx context.Context, now time.Time, changes iter.Seq2[DiskDocument, error]) (IndexMemoryStats, error) {
	if now.Before(idx.footer.Source.Watermark) {
		return IndexMemoryStats{}, ErrIndexIncomplete
	}
	view, err := idx.PinAdmission(ctx)
	if err != nil {
		return IndexMemoryStats{}, err
	}
	defer view.Close()
	q := diskQuery{
		index: idx, snap: view.snapshot, work: newWorkTracker(ctx, diskBoundBudget(Budget{})),
		now: now, stats: idx.footer.Stats, capacity: idx.footer.Capacity,
	}
	if err := q.applyChanges(changes); err != nil {
		return IndexMemoryStats{}, err
	}
	if err := q.applyDueDeadlines(); err != nil {
		return IndexMemoryStats{}, err
	}
	capacity, err := q.effectiveCapacity()
	if err != nil {
		return IndexMemoryStats{}, err
	}
	if err := capacity.validateLimits(idx.config.limits); err != nil {
		return IndexMemoryStats{}, err
	}
	live, _, err := q.effectiveDocumentCounts()
	if err != nil {
		return IndexMemoryStats{}, err
	}
	return IndexMemoryStats{
		Documents:              int(min(live, math.MaxInt)),
		PhysicalDocuments:      int(min(idx.footer.LiveDocuments, math.MaxInt)),
		ExpiredDocuments:       int(min(q.overlay.expiredDocuments, math.MaxInt)),
		ExpirationQueueEntries: int(min(idx.footer.Deadlines, math.MaxInt)),
		LiveTerms:              int(min(capacity.Terms, math.MaxInt)),
		RetainedTermSlots:      int(min(idx.footer.Capacity.Terms, math.MaxInt)),
		RetainedOrdinals:       int(min(idx.footer.Documents, math.MaxInt)),
		Postings:               capacity.Postings,
		PositionEntries:        capacity.Positions,
		Generation:             idx.base,
		Health:                 IndexHealthy,
	}, nil
}
