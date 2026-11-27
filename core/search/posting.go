package search

import (
	"encoding/binary"
	"maps"
	"math/bits"
	"slices"

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
// storage at all. fieldPosition entries exist only when the index was built
// WithPositions and the term carries a position in that field. Documents in
// each block share a byte buffer for their delta-encoded positions. A uint64
// combines field-instance and token offset before encoding.
type postingList struct {
	docs          *roaring.Bitmap // document ordinals containing this term in any field
	hasNonDefault bool
	fieldDocs     [numDocumentFields]*roaring.Bitmap
	fieldTFHi     [numDocumentFields]map[uint32]uint16
	fieldPosition [numDocumentFields]*postingPositions
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
// nothing. The index delta-encodes the positions and keeps the encoded bytes.
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
				p.fieldPosition[field] = new(postingPositions)
			}
			p.fieldPosition[field].set(ord, value.positions)
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
		if positions := p.fieldPosition[field]; positions != nil && positions.remove(ord) {
			p.fieldPosition[field] = nil
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
	return p.fieldPosition[field].decode(ord, dst)
}

func (p *postingList) positionsInField(ord uint32, field FieldID) []uint64 {
	return p.positionsInto(ord, field, nil)
}

const (
	positionBlockWidth = 64
	// A block holds at most 16 KiB of payload. Large values use their own
	// backing so inserting a short value never shifts those bytes.
	maxBlockPositionBytes = 256
)

// postingPositions owns the positional values for one term in one field.
// A short singleton needs no backing allocation beyond this struct. Groups
// of nearby ordinals then share their payload and offsets in sparse blocks.
type postingPositions struct {
	blocks    map[uint32]positionSlot
	large     map[uint32][]byte
	inline    [8]byte
	inlineOrd uint32
	inlineLen uint8
	blockPeak int
	largePeak int
}

func (p *postingPositions) set(ord uint32, positions []uint64) {
	if len(positions) <= maxBlockPositionBytes/binary.MaxVarintLen64 {
		// Even with the widest gaps these positions fit in a block.
		var scratch [64]byte
		p.setSmall(ord, appendPackedPositions(scratch[:0], positions))
		return
	}
	data := packPositions(positions)
	if len(data) > maxBlockPositionBytes {
		p.setLarge(ord, data)
		return
	}
	p.setSmall(ord, data)
}

func (p *postingPositions) setSmall(ord uint32, data []byte) {
	p.removeLarge(ord)
	if len(p.blocks) == 0 {
		if (p.inlineLen == 0 || p.inlineOrd == ord) && len(data) <= len(p.inline) {
			copy(p.inline[:], data)
			p.inlineOrd, p.inlineLen = ord, uint8(len(data))
			return
		}
		if p.inlineLen != 0 && p.inlineOrd != ord {
			p.setBlock(p.inlineOrd, p.inline[:p.inlineLen])
		}
		p.inlineLen = 0
	}
	p.setBlock(ord, data)
}

func (p *postingPositions) setBlock(ord uint32, data []byte) {
	key, offset := positionBlockKey(ord)
	slot := p.blocks[key]
	bit := uint64(1) << offset
	if slot.block != nil {
		slot.block.set(bit, data)
		return
	}
	if len(data) <= len(slot.inline) && (slot.length == 0 || slot.offset == offset) {
		slot.offset, slot.length = offset, uint8(copy(slot.inline[:], data))
	} else {
		block := new(positionBlock)
		if slot.length != 0 && slot.offset != offset {
			block.set(uint64(1)<<slot.offset, slot.inline[:slot.length])
		}
		block.set(bit, data)
		slot = positionSlot{block: block}
	}
	if p.blocks == nil {
		p.blocks = make(map[uint32]positionSlot)
	}
	p.blocks[key] = slot
	p.blockPeak = max(p.blockPeak, len(p.blocks))
}

func (p *postingPositions) setLarge(ord uint32, data []byte) {
	p.removeSmall(ord)
	if p.large == nil {
		p.large = make(map[uint32][]byte)
	}
	p.large[ord] = data
	p.largePeak = max(p.largePeak, len(p.large))
}

func (p *postingPositions) decode(ord uint32, dst []uint64) []uint64 {
	dst = dst[:0]
	if p.inlineLen != 0 && p.inlineOrd == ord {
		return unpackPositionsInto(dst, p.inline[:p.inlineLen])
	}
	if len(p.blocks) != 0 {
		key, offset := positionBlockKey(ord)
		slot := p.blocks[key]
		if block := slot.block; block != nil {
			bit := uint64(1) << offset
			if block.members&bit != 0 {
				_, start, end := block.bounds(bit)
				return unpackPositionsInto(dst, block.data[start:end])
			}
		} else if slot.offset == offset && slot.length != 0 {
			return unpackPositionsInto(dst, slot.inline[:slot.length])
		}
	}
	return unpackPositionsInto(dst, p.large[ord])
}

func (p *postingPositions) remove(ord uint32) (empty bool) {
	p.removeLarge(ord)
	p.removeSmall(ord)
	return p.inlineLen == 0 && len(p.blocks) == 0 && len(p.large) == 0
}

func (p *postingPositions) removeLarge(ord uint32) {
	if len(p.large) == 0 {
		return
	}
	delete(p.large, ord)
	p.large = trimPositionMap(p.large, &p.largePeak)
}

func (p *postingPositions) removeSmall(ord uint32) {
	if p.inlineLen != 0 && p.inlineOrd == ord {
		p.inlineLen = 0
		return
	}
	if len(p.blocks) == 0 {
		return
	}
	key, offset := positionBlockKey(ord)
	slot := p.blocks[key]
	if block := slot.block; block != nil {
		bit := uint64(1) << offset
		if block.members&bit == 0 {
			return
		}
		if block.members != bit {
			block.set(bit, nil)
			if len(block.ends) == 1 && len(block.data) <= len(slot.inline) {
				slot = positionSlot{offset: uint8(bits.TrailingZeros64(block.members))}
				slot.length = uint8(copy(slot.inline[:], block.data))
				p.blocks[key] = slot
			}
			p.inlineSingleton()
			return
		}
	} else {
		if slot.length == 0 || slot.offset != offset {
			return
		}
	}
	delete(p.blocks, key)
	p.blocks = trimPositionMap(p.blocks, &p.blockPeak)
	p.inlineSingleton()
}

func (p *postingPositions) inlineSingleton() {
	if len(p.blocks) != 1 {
		return
	}
	for key, slot := range p.blocks {
		offset, data := slot.offset, slot.inline[:slot.length]
		if block := slot.block; block != nil {
			if len(block.ends) != 1 || len(block.data) > len(p.inline) {
				return
			}
			offset, data = uint8(bits.TrailingZeros64(block.members)), block.data
		}
		p.inlineOrd = key*positionBlockWidth + uint32(offset)
		p.inlineLen = uint8(copy(p.inline[:], data))
		p.blocks = nil
		p.blockPeak = 0
	}
}

func trimPositionMap[V any](m map[uint32]V, peak *int) map[uint32]V {
	n := len(m)
	switch {
	case n == 0:
		*peak = 0
		return nil
	case *peak >= 32 && n <= *peak/4:
		// Go maps retain buckets on deletion. Rebuild only after a large
		// reduction so the work is amortized over the preceding removals.
		compacted := make(map[uint32]V, n)
		maps.Copy(compacted, m)
		*peak = n
		return compacted
	default:
		return m
	}
}

// A sparse block's first short value fits inside its map slot. That avoids
// separate allocations when the same term appears at widely spaced ordinals.
type positionSlot struct {
	block  *positionBlock
	inline [6]byte
	offset uint8
	length uint8
}

type positionBlock struct {
	members uint64
	data    []byte
	// Ends are indexed by bitmap rank. Every encoded value is non-empty.
	ends []uint16
}

func positionBlockKey(ord uint32) (uint32, uint8) {
	return ord / positionBlockWidth, uint8(ord % positionBlockWidth)
}

func (b *positionBlock) bounds(bit uint64) (rank, start, end int) {
	rank = bits.OnesCount64(b.members & (bit - 1))
	if rank != 0 {
		start = int(b.ends[rank-1])
	}
	end = start
	if b.members&bit != 0 {
		end = int(b.ends[rank])
	}
	return rank, start, end
}

func (b *positionBlock) set(bit uint64, data []byte) {
	rank, start, end := b.bounds(bit)
	delta := len(data) - (end - start)
	oldLen := len(b.data)
	if delta > 0 {
		b.data = slices.Grow(b.data, delta)[:oldLen+delta]
	}
	copy(b.data[start+len(data):], b.data[end:oldLen])
	copy(b.data[start:], data)
	b.data = b.data[:oldLen+delta]
	switch {
	case len(data) == 0:
		b.members &^= bit
		b.ends = slices.Delete(b.ends, rank, rank+1)
	case start == end:
		b.members |= bit
		b.ends = slices.Insert(b.ends, rank, uint16(start+len(data)))
		rank++
	default:
		b.ends[rank] = uint16(start + len(data))
		rank++
	}
	for i := rank; i < len(b.ends); i++ {
		b.ends[i] = uint16(int(b.ends[i]) + delta)
	}
	if cap(b.data) >= 128 && len(b.data) <= cap(b.data)/4 {
		b.data = slices.Clone(b.data)
	}
	if cap(b.ends) >= 16 && len(b.ends) <= cap(b.ends)/4 {
		b.ends = slices.Clone(b.ends)
	}
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
	return appendPackedPositions(make([]byte, 0, len(positions)), positions)
}

func appendPackedPositions(buf []byte, positions []uint64) []byte {
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
