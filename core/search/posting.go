package search

import (
	"encoding/binary"

	"github.com/RoaringBitmap/roaring/v2"
)

// postingList is one term's posting set: which document ordinals contain the
// term, plus field-local term frequencies and — when the index tracks positions
// — token positions. Union membership lives in a Roaring bitmap; structured
// fields add sparse per-field bitmaps while the single default-field path reuses
// the union bitmap and pays no duplicate membership store. This fits the decaying
// workload's constant add / remove without the per-entry overhead of a map
// keyed by the document id. Frequencies default to 1 and only the
// comparatively rare term that occurs more than once in a document is recorded
// in tfHi, so the common case carries no per-(term, document) frequency
// storage at all. fieldPosition entries exist only when the index was built WithPositions
// and the term carries at least one position in that field, so an index that
// serves only OR-union ranking pays nothing for phrase/proximity support
// (#889). Each document's positions are stored delta+varint packed (#908): a
// uint64 combines field-instance and token offset before compact encoding, so
// the store costs ~1 byte per position (short vertex values) rather than 4.
type postingList struct {
	docs          *roaring.Bitmap // document ordinals containing this term in any field
	hasNonDefault bool
	fieldDocs     [numDocumentFields]*roaring.Bitmap
	fieldTFHi     [numDocumentFields]map[uint32]uint16
	fieldPosition [numDocumentFields]map[uint32][]byte
}

// newPostingList returns an empty posting list ready to record documents.
func newPostingList() *postingList {
	return &postingList{docs: roaring.New()}
}

// set records a default-field term and backs legacy single-text documents. A tf of 1
// — the common case — costs only the bitmap membership bit. positions, when
// non-empty, are the term's ascending token positions in the document; they
// are stored only when the index tracks positions (WithPositions) and only for
// the primary word channel, so an OR-union-only index passes nil and pays
// nothing. They are delta+varint packed into a fresh []byte, so the caller's
// transient position slice is not retained and the store holds only the
// compact bytes.
func (p *postingList) set(ord uint32, tf int, positions []uint64) {
	var fields [numDocumentFields]preparedFieldTerm
	fields[FieldDefault] = preparedFieldTerm{frequency: tf, positions: positions}
	p.setFields(ord, fields)
}

func (p *postingList) setFields(ord uint32, fields [numDocumentFields]preparedFieldTerm) {
	if !p.hasNonDefault {
		for field := FieldKey; field < numDocumentFields; field++ {
			if fields[field].frequency == 0 {
				continue
			}
			p.hasNonDefault = true
			if !p.docs.IsEmpty() {
				p.fieldDocs[FieldDefault] = p.docs.Clone()
			}
			break
		}
	}
	p.docs.Add(ord)
	for field, value := range fields {
		if value.frequency == 0 {
			continue
		}
		if FieldID(field) == FieldDefault && !p.hasNonDefault {
			// Until a non-default field appears, the union bitmap is exactly the
			// default-field bitmap; do not duplicate it.
		} else if p.fieldDocs[field] == nil {
			p.fieldDocs[field] = roaring.New()
		}
		if p.fieldDocs[field] != nil {
			p.fieldDocs[field].Add(ord)
		}
		if value.frequency > 1 {
			if p.fieldTFHi[field] == nil {
				p.fieldTFHi[field] = make(map[uint32]uint16)
			}
			p.fieldTFHi[field][ord] = clampTF(value.frequency)
		}
		if len(value.positions) > 0 {
			if p.fieldPosition[field] == nil {
				p.fieldPosition[field] = make(map[uint32][]byte)
			}
			p.fieldPosition[field][ord] = packPositions(value.positions)
		}
	}
}

// remove drops document ord and reports whether the list is now empty, so the
// caller can delete it and release the term id in lockstep. Positions are
// dropped in the same step, so the decaying delete path never leaks a
// document's position slice.
func (p *postingList) remove(ord uint32) (empty bool) {
	p.docs.Remove(ord)
	for field := range p.fieldDocs {
		if FieldID(field) == FieldDefault && !p.hasNonDefault {
			// p.docs owns default membership in the single-field fast path.
		} else if p.fieldDocs[field] != nil {
			p.fieldDocs[field].Remove(ord)
			if p.fieldDocs[field].IsEmpty() {
				p.fieldDocs[field] = nil
			}
		}
		if p.fieldTFHi[field] != nil {
			delete(p.fieldTFHi[field], ord)
			if len(p.fieldTFHi[field]) == 0 {
				p.fieldTFHi[field] = nil
			}
		}
		if p.fieldPosition[field] != nil {
			delete(p.fieldPosition[field], ord)
			if len(p.fieldPosition[field]) == 0 {
				p.fieldPosition[field] = nil
			}
		}
	}
	return p.docs.IsEmpty()
}

// tf returns document ord's frequency for this term: 1 unless an override was
// recorded. Callers iterate p.docs, so ord is always a member.
func (p *postingList) tf(ord uint32) int {
	var total int
	for field := FieldID(0); field < numDocumentFields; field++ {
		total += p.tfInField(ord, field)
	}
	if total == 0 {
		return 1
	}
	return total
}

func (p *postingList) tfInField(ord uint32, field FieldID) int {
	docs := p.fieldDocs[field]
	if field == FieldDefault && !p.hasNonDefault {
		docs = p.docs
	}
	if docs == nil || !docs.Contains(ord) {
		return 0
	}
	if p.fieldTFHi[field] != nil {
		if frequency, ok := p.fieldTFHi[field][ord]; ok {
			return int(frequency)
		}
	}
	return 1
}

func (p *postingList) containsField(ord uint32, field FieldID) bool {
	if field == FieldDefault && !p.hasNonDefault {
		return p.docs.Contains(ord)
	}
	return p.fieldDocs[field] != nil && p.fieldDocs[field].Contains(ord)
}

func (p *postingList) fieldCardinality(field FieldID) int {
	if field == FieldDefault && !p.hasNonDefault {
		return int(p.docs.GetCardinality())
	}
	if p.fieldDocs[field] == nil {
		return 0
	}
	return int(p.fieldDocs[field].GetCardinality())
}

// positionsOf returns document ord's ascending default-field positions,
// or nil when the index does not track positions (or ord carries none). The
// packed bytes are decoded into a fresh []uint64 on each call, so the returned
// slice is owned by the caller and safe to mutate; it is not shared with the
// posting list.
func (p *postingList) positionsOf(ord uint32) []uint64 {
	return p.positionsInField(ord, FieldDefault)
}

// positionsInto decodes ord's positions into dst, reusing its capacity. The
// bounded query executor keeps one scratch slice per query term so positional
// work does not allocate once per matching document.
func (p *postingList) positionsInto(ord uint32, field FieldID, dst []uint64) []uint64 {
	if p.fieldPosition[field] == nil {
		return dst[:0]
	}
	return unpackPositionsInto(dst[:0], p.fieldPosition[field][ord])
}

func (p *postingList) positionsInField(ord uint32, field FieldID) []uint64 {
	return p.positionsInto(ord, field, nil)
}

// packPositions delta+varint encodes an ascending position slice: the first
// value is emitted as-is (its delta from 0) and each subsequent value as the
// gap from its predecessor, then each gap is written as an unsigned varint.
// Positions in a document are strictly increasing (each word token advances the
// counter), so gaps are small and pack into a single byte for the short vertex
// values Lantern indexes — a ~4x shrink over the raw []uint32. positions must
// be ascending; the indexer builds it that way.
func packPositions(positions []uint64) []byte {
	// One byte per position is the common case, so size the buffer for it.
	buf := make([]byte, 0, len(positions))
	var prev uint64
	for _, pos := range positions {
		buf = binary.AppendUvarint(buf, pos-prev)
		prev = pos
	}
	return buf
}

// unpackPositions reverses packPositions, decoding the delta+varint bytes back
// into the ascending absolute positions. It returns nil for empty input (a
// document that carries no positions for the term).
func unpackPositions(b []byte) []uint64 {
	return unpackPositionsInto(nil, b)
}

func unpackPositionsInto(dst []uint64, b []byte) []uint64 {
	if len(b) == 0 {
		return dst[:0]
	}
	// Each position occupies at least one byte, so len(b) bounds the count.
	out := dst
	if cap(out) < len(b) {
		out = make([]uint64, 0, len(b))
	}
	var acc uint64
	for i := 0; i < len(b); {
		delta, n := binary.Uvarint(b[i:])
		if n <= 0 {
			break // malformed; only reachable on a corrupted buffer
		}
		acc += delta
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
