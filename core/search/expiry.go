package search

import (
	"container/heap"
	"time"
)

// expiryRecord is the one heap node owned by an expiring live document. The
// index field lets overwrite/delete remove it eagerly, keeping the heap bounded
// by physical documents instead of retaining stale generations.
type expiryRecord struct {
	at    time.Time
	ord   uint32
	index int
}

type expiryHeap []*expiryRecord

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *expiryHeap) Push(value any) {
	record := value.(*expiryRecord)
	record.index = len(*h)
	*h = append(*h, record)
}
func (h *expiryHeap) Pop() any {
	old := *h
	last := len(old) - 1
	record := old[last]
	old[last] = nil
	record.index = -1
	*h = old[:last]
	return record
}

func expirationFinite(expiration time.Time) bool {
	return !expiration.IsZero() && expiration.Unix() > 0
}

func expirationLiveAt(expiration, now time.Time) bool {
	return !expirationFinite(expiration) || expiration.After(now)
}

// purgeExpired removes every document due at the query's single sampled now
// before scoring or vocabulary expansion. Heap visits and per-document posting
// removals are charged before mutation; exhausting either budget returns no
// query result, while already completed purges remain safe progress for the
// next attempt.
func (idx *InvertedIndex[S, D]) purgeExpired(work *workTracker, now time.Time) error {
	idx.mu.RLock()
	if idx.health != IndexHealthy {
		idx.mu.RUnlock()
		return ErrIndexIncomplete
	}
	due := len(idx.expirations) > 0 && !idx.expirations[0].at.After(now)
	idx.mu.RUnlock()
	if !due {
		return work.check()
	}

	idx.lockWrite()
	defer idx.mu.Unlock()
	if idx.health != IndexHealthy {
		return ErrIndexIncomplete
	}
	started := time.Now()
	var purged uint64
	defer func() {
		if purged > 0 {
			idx.expirationPurged += purged
			idx.lastExpirationPurge = time.Since(started)
		}
	}()
	for len(idx.expirations) > 0 {
		record := idx.expirations[0]
		if record.at.After(now) {
			break
		}
		if err := work.visit(WorkExpirationVisits, 1); err != nil {
			return err
		}
		entry, ok := idx.docs[record.ord]
		if !ok || entry.expiry != record {
			// Defensive only: eager heap removal should make stale records
			// impossible, but dropping one is safer than blocking every query.
			heap.Pop(&idx.expirations)
			continue
		}
		postingVisits := entry.postingEntries
		if err := work.visit(WorkPostingVisits, int64(postingVisits)); err != nil {
			return err
		}
		heap.Pop(&idx.expirations)
		idx.deleteLocked(entry.id)
		purged++
	}
	// Returning an empty index to fresh storage is O(1) in live corpus size and
	// prevents retired dictionary slots from charging later expansion work. Do
	// not run ratio compaction here: rebuilding a non-empty corpus would escape
	// the query's expiration/posting budgets.
	if purged > 0 && len(idx.docs) == 0 {
		idx.compactLocked()
	}
	return nil
}
