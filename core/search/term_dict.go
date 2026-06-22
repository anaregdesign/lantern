package search

// termDict assigns a compact, reusable uint32 id to each distinct term so the
// inverted index can key its postings and per-document forward lists by id
// instead of repeating the term string everywhere. Storing each term string
// exactly once here is what collapses the ~(docs × terms-per-doc) term-string
// objects a string-keyed index would otherwise hold down to one per distinct
// term — the dominant object-count and a large byte-count saving for an
// n-gram index, where the same short grams recur across most documents.
//
// termDict is not safe for concurrent use; InvertedIndex guards it with idx.mu,
// the same lock that protects the postings it stays in lockstep with.
type termDict struct {
	// ids maps a term to its assigned id. Its size is the number of live terms.
	ids map[string]uint32
	// terms is the reverse map id -> term, indexed by id. A released id keeps a
	// "" slot until it is handed back out, so terms never shrinks but its
	// high-water mark is the distinct-term count (small for an n-gram corpus).
	terms []string
	// free holds released ids, reused before the id space grows so ids stay
	// dense under a delete-heavy (decaying) workload.
	free []uint32
}

// newTermDict returns an empty dictionary ready to intern terms.
func newTermDict() *termDict {
	return &termDict{ids: make(map[string]uint32)}
}

// intern returns the id for term, assigning and recording a fresh one the first
// time term is seen. A released id is reused before the id space grows.
func (d *termDict) intern(term string) uint32 {
	if id, ok := d.ids[term]; ok {
		return id
	}
	var id uint32
	if n := len(d.free); n > 0 {
		id = d.free[n-1]
		d.free = d.free[:n-1]
		d.terms[id] = term
	} else {
		id = uint32(len(d.terms))
		d.terms = append(d.terms, term)
	}
	d.ids[term] = id
	return id
}

// lookup returns the id for term and whether it is currently interned, without
// assigning one. Search uses it so a query term absent from the corpus simply
// misses instead of growing the dictionary.
func (d *termDict) lookup(term string) (uint32, bool) {
	id, ok := d.ids[term]
	return id, ok
}

// release forgets id and its term, returning the id to the free list for reuse.
// The index calls it when a term's last posting is removed, so the dictionary
// decays in lockstep with the postings and leaks nothing under churn. Dropping
// the reverse slot to "" lets the term's string backing be reclaimed.
func (d *termDict) release(id uint32) {
	delete(d.ids, d.terms[id])
	d.terms[id] = ""
	d.free = append(d.free, id)
}

// len reports the number of live (interned) terms.
func (d *termDict) len() int { return len(d.ids) }
