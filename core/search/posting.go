package search

import (
	"encoding/binary"

	"github.com/RoaringBitmap/roaring/v2"
)

// postingList is one term's posting set: which document ordinals contain the
// term, plus their term frequencies and — when the index tracks positions —
// the token positions the term occupies in each document. Membership lives in
// a Roaring bitmap — compressed and mutable, so it fits the decaying
// workload's constant add / remove without the per-entry overhead of a map
// keyed by the document id. Frequencies default to 1 and only the
// comparatively rare term that occurs more than once in a document is recorded
// in tfHi, so the common case carries no per-(term, document) frequency
// storage at all. positions is nil unless the index was built WithPositions
// and the term carries at least one position in the document, so an index that
// serves only OR-union ranking pays nothing for phrase/proximity support
// (#889). Each document's positions are stored delta+varint packed (#908): the
// ascending token positions become a compact []byte instead of a []uint32, so
// the store costs ~1 byte per position (short vertex values) rather than 4.
type postingList struct {
	docs      *roaring.Bitmap   // document ordinals containing this term
	tfHi      map[uint32]uint16 // ordinal -> term frequency, recorded only when tf > 1
	positions map[uint32][]byte // ordinal -> delta+varint packed ascending positions, only when the index tracks positions
}

// newPostingList returns an empty posting list ready to record documents.
func newPostingList() *postingList {
	return &postingList{docs: roaring.New()}
}

// set records that document ord contains the term tf times (tf >= 1). A tf of 1
// — the common case — costs only the bitmap membership bit. positions, when
// non-empty, are the term's ascending token positions in the document; they
// are stored only when the index tracks positions (WithPositions) and only for
// the primary word channel, so an OR-union-only index passes nil and pays
// nothing. They are delta+varint packed into a fresh []byte, so the caller's
// transient position slice is not retained and the store holds only the
// compact bytes.
func (p *postingList) set(ord uint32, tf int, positions []uint32) {
	p.docs.Add(ord)
	if tf > 1 {
		if p.tfHi == nil {
			p.tfHi = make(map[uint32]uint16)
		}
		p.tfHi[ord] = clampTF(tf)
	}
	if len(positions) > 0 {
		if p.positions == nil {
			p.positions = make(map[uint32][]byte)
		}
		p.positions[ord] = packPositions(positions)
	}
}

// remove drops document ord and reports whether the list is now empty, so the
// caller can delete it and release the term id in lockstep. Positions are
// dropped in the same step, so the decaying delete path never leaks a
// document's position slice.
func (p *postingList) remove(ord uint32) (empty bool) {
	p.docs.Remove(ord)
	if p.tfHi != nil {
		delete(p.tfHi, ord)
		if len(p.tfHi) == 0 {
			p.tfHi = nil
		}
	}
	if p.positions != nil {
		delete(p.positions, ord)
		if len(p.positions) == 0 {
			p.positions = nil
		}
	}
	return p.docs.IsEmpty()
}

// tf returns document ord's frequency for this term: 1 unless an override was
// recorded. Callers iterate p.docs, so ord is always a member.
func (p *postingList) tf(ord uint32) int {
	if p.tfHi != nil {
		if f, ok := p.tfHi[ord]; ok {
			return int(f)
		}
	}
	return 1
}

// positionsOf returns document ord's ascending token positions for this term,
// or nil when the index does not track positions (or ord carries none). The
// packed bytes are decoded into a fresh []uint32 on each call, so the returned
// slice is owned by the caller and safe to mutate; it is not shared with the
// posting list.
func (p *postingList) positionsOf(ord uint32) []uint32 {
	if p.positions == nil {
		return nil
	}
	return unpackPositions(p.positions[ord])
}

// packPositions delta+varint encodes an ascending position slice: the first
// value is emitted as-is (its delta from 0) and each subsequent value as the
// gap from its predecessor, then each gap is written as an unsigned varint.
// Positions in a document are strictly increasing (each word token advances the
// counter), so gaps are small and pack into a single byte for the short vertex
// values Lantern indexes — a ~4x shrink over the raw []uint32. positions must
// be ascending; the indexer builds it that way.
func packPositions(positions []uint32) []byte {
	// One byte per position is the common case, so size the buffer for it.
	buf := make([]byte, 0, len(positions))
	var prev uint32
	for _, pos := range positions {
		buf = binary.AppendUvarint(buf, uint64(pos-prev))
		prev = pos
	}
	return buf
}

// unpackPositions reverses packPositions, decoding the delta+varint bytes back
// into the ascending absolute positions. It returns nil for empty input (a
// document that carries no positions for the term).
func unpackPositions(b []byte) []uint32 {
	if len(b) == 0 {
		return nil
	}
	// Each position occupies at least one byte, so len(b) bounds the count.
	out := make([]uint32, 0, len(b))
	var acc uint32
	for i := 0; i < len(b); {
		delta, n := binary.Uvarint(b[i:])
		if n <= 0 {
			break // malformed; only reachable on a corrupted buffer
		}
		acc += uint32(delta)
		out = append(out, acc)
		i += n
	}
	return out
}

// cardinality is the document frequency (DF): how many documents hold the term.
func (p *postingList) cardinality() int { return int(p.docs.GetCardinality()) }

// clampTF caps a term frequency at the uint16 ceiling. BM25 saturates long
// before this, so the cap never changes a realistic ranking.
func clampTF(tf int) uint16 {
	if tf >= int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(tf)
}

// ordinals assigns each distinct document id a compact, reusable uint32 ordinal
// so postings address documents by a 4-byte integer (a Roaring bitmap member)
// instead of repeating the caller's id — typically a string vertex key — in
// every posting the document appears in. Released ordinals are reused before
// the space grows, keeping the range dense under a delete-heavy (decaying)
// workload. Not safe for concurrent use; InvertedIndex guards it with idx.mu.
type ordinals[S comparable] struct {
	byKey map[S]uint32
	free  []uint32
	next  uint32
}

// newOrdinals returns an empty ordinal allocator.
func newOrdinals[S comparable]() *ordinals[S] {
	return &ordinals[S]{byKey: make(map[S]uint32)}
}

// assign returns key's ordinal, allocating one (reusing a freed ordinal before
// growing the space) the first time key is seen.
func (o *ordinals[S]) assign(key S) uint32 {
	if id, ok := o.byKey[key]; ok {
		return id
	}
	var id uint32
	if n := len(o.free); n > 0 {
		id = o.free[n-1]
		o.free = o.free[:n-1]
	} else {
		id = o.next
		o.next++
	}
	o.byKey[key] = id
	return id
}

// lookup returns key's ordinal and whether it is currently assigned, without
// allocating one.
func (o *ordinals[S]) lookup(key S) (uint32, bool) {
	id, ok := o.byKey[key]
	return id, ok
}

// release frees key's ordinal for reuse by a later document.
func (o *ordinals[S]) release(key S, id uint32) {
	delete(o.byKey, key)
	o.free = append(o.free, id)
}
