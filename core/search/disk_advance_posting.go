package search

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"sort"

	"github.com/anaregdesign/tarp"
)

type diskTermRewrite struct {
	blocks  map[uint64]*diskRewriteBlock
	removed uint64
}

type diskRewriteBlock struct {
	start    uint64
	postings []diskPosting
}

func (p *diskAdvancePlan) term(token Token) *diskTermRewrite {
	term := p.terms[token]
	if term == nil {
		term = &diskTermRewrite{blocks: make(map[uint64]*diskRewriteBlock)}
		p.terms[token] = term
	}
	return term
}

func (p *diskAdvancePlan) rawBlock(token Token, term *diskTermRewrite, id uint64) (*diskRewriteBlock, error) {
	q := p.query
	prefix := diskKey(p.footer.Base, diskPostingRow, uint8(token.Class), token.Term)
	_, upper := tarp.PrefixRange(diskKey(p.footer.Base, diskPostingRow, uint8(token.Class), token.Term, id))
	for row, err := range q.snap.ScanRange(q.work.ctx, prefix, upper, tarp.WithReverse()) {
		if err != nil {
			return nil, err
		}
		if err := q.work.visit(WorkDiskReadBytes, int64(len(row.Key)+len(row.Value))); err != nil {
			return nil, err
		}
		start, err := diskNumberKey(row.Key, prefix)
		if err != nil {
			return nil, err
		}
		if existing := term.blocks[start]; existing != nil {
			return existing, nil
		}
		postings, err := decodeRewriteBlock(row.Value, start, q.index.footer.Documents)
		if err != nil {
			return nil, err
		}
		if err := p.reserve(192*len(postings) + len(row.Value)); err != nil {
			return nil, err
		}
		block := &diskRewriteBlock{start: start, postings: postings}
		term.blocks[start] = block
		return block, nil
	}
	return nil, diskInvalid("missing block for index advancement")
}

func decodeRewriteBlock(value []byte, start, largest uint64) ([]diskPosting, error) {
	d := diskDecoder{data: value}
	count := d.number()
	if len(value) > diskPostingBlockSize || count == 0 || count > diskPostingBlockDocs {
		return nil, diskInvalid("invalid posting block size")
	}
	postings := make([]diskPosting, 0, count)
	var prior uint64
	for range count {
		gap := d.number()
		if gap == 0 || gap > math.MaxUint64-prior {
			return nil, diskInvalid("invalid posting ID gap")
		}
		posting := decodeDiskPosting(&d)
		posting.id = prior + gap
		if (prior == 0 && posting.id != start) || posting.id > largest {
			return nil, diskInvalid("posting ID out of bounds")
		}
		postings = append(postings, posting)
		prior = posting.id
	}
	if !d.success() {
		return nil, diskInvalid("malformed posting block")
	}
	return postings, nil
}

func (p *diskAdvancePlan) removePosting(token Token, id uint64, forward diskPosting) error {
	q := p.query
	term := p.term(token)
	block, err := p.rawBlock(token, term, id)
	if err != nil {
		return err
	}
	i := sort.Search(len(block.postings), func(i int) bool { return block.postings[i].id >= id })
	if i == len(block.postings) || block.postings[i].id != id || block.postings[i].frequency != forward.frequency ||
		block.postings[i].positions != forward.positions {
		return diskInvalid("forward reference disagrees with posting block")
	}
	for field, count := range forward.positions {
		if !bytes.Equal(block.postings[i].inline[field], forward.inline[field]) {
			return diskInvalid("forward position data disagrees with posting block")
		}
		if count == 0 || len(forward.inline[field]) != 0 {
			continue
		}
		for chunk := uint64(0); chunk <= (count-1)/diskPositionChunk; chunk++ {
			if err := q.work.visit(WorkPostingVisits, 1); err != nil {
				return err
			}
			key := diskKey(p.footer.Base, diskPositionRow, uint8(token.Class), token.Term, id, uint8(field), chunk)
			if err := p.row(key, nil, true); err != nil {
				return err
			}
		}
	}
	term.removed++
	block.postings = slices.Delete(block.postings, i, i+1)
	return nil
}

func (p *diskAdvancePlan) writeTerm(token Token, term *diskTermRewrite) error {
	q := p.query
	key := diskKey(p.footer.Base, diskTermRow, uint8(token.Class), token.Term)
	value, err := q.read(key)
	var meta diskTermValue
	if !errors.Is(err, tarp.ErrNotFound) {
		if err != nil {
			return err
		}
		meta, err = decodeDiskTerm(value)
		if err != nil {
			return err
		}
	}
	if term.removed > meta.documents {
		return diskInvalid("term document count underflow")
	}
	meta.documents -= term.removed
	added := q.overlay.terms[token]
	if added != nil {
		if uint64(len(added.postings)) > math.MaxUint64-meta.documents {
			return diskInvalid("term document count overflow")
		}
		meta.documents += uint64(len(added.postings))
		if err := p.appendPostings(token, term, added, &meta); err != nil {
			return err
		}
	}
	for _, block := range term.blocks {
		if len(block.postings) == 0 || block.postings[0].id != block.start {
			oldKey := diskKey(p.footer.Base, diskPostingRow, uint8(token.Class), token.Term, block.start)
			if err := p.row(oldKey, nil, true); err != nil {
				return err
			}
		}
		if len(block.postings) == 0 {
			if meta.blocks == 0 {
				return diskInvalid("term block count underflow")
			}
			meta.blocks--
			continue
		}
		if err := p.writeBlock(token, block.postings); err != nil {
			return err
		}
	}
	df, live, err := q.liveDF(token, meta)
	if err != nil {
		return err
	}
	if !live {
		if meta.documents != 0 || meta.blocks != 0 {
			return diskInvalid("empty term still references postings")
		}
		return p.row(key, nil, true)
	}
	if meta.documents == 0 || meta.blocks == 0 || meta.blocks > meta.documents {
		return diskInvalid("nonempty term has invalid posting counts")
	}
	meta.df = df
	return p.row(key, encodeDiskTerm(meta), false)
}

func (p *diskAdvancePlan) diskPosting(token Token, input diskPosting) (diskPosting, error) {
	posting := input
	posting.overlay = nil
	for field, count := range input.positions {
		if count == 0 {
			continue
		}
		if input.overlay == nil || count != uint64(len(input.overlay[field])) {
			return diskPosting{}, diskInvalid("missing changed positional data")
		}
		positions := input.overlay[field]
		if len(positions) <= diskInlinePositions {
			packed := packPositions(positions)
			if len(packed) <= diskInlinePositions {
				posting.inline[field] = packed
				continue
			}
		}
		for offset := 0; offset < len(positions); offset += diskPositionChunk {
			chunk := positions[offset:min(offset+diskPositionChunk, len(positions))]
			key := diskKey(p.footer.Base, diskPositionRow, uint8(token.Class), token.Term, input.id, uint8(field), uint64(offset/diskPositionChunk))
			if err := p.row(key, packPositions(chunk), false); err != nil {
				return diskPosting{}, err
			}
		}
	}
	key := diskKey(p.footer.Base, diskForwardRow, posting.id, uint8(token.Class), token.Term)
	return posting, p.row(key, encodeDiskPosting(nil, posting), false)
}

func (p *diskAdvancePlan) appendPostings(token Token, term *diskTermRewrite, added *diskOverlayTerm, meta *diskTermValue) error {
	var tail *diskRewriteBlock
	var previous uint64
	var encodedBytes int
	if meta.blocks != 0 {
		block, err := p.rawBlock(token, term, math.MaxUint64)
		if err != nil {
			return err
		}
		if len(block.postings) != 0 {
			tail = block
			previous = block.postings[len(block.postings)-1].id
			encodedBytes = len(encodeRewritePostings(block.postings))
		}
	}
	var pending []diskPosting
	for _, source := range added.postings {
		posting, err := p.diskPosting(token, source)
		if err != nil {
			return err
		}
		if posting.id <= previous {
			return diskInvalid("unordered appended document ID")
		}
		size := len(encodeDiskPosting(appendDiskNumber(nil, posting.id-previous), posting))
		if tail != nil {
			if len(tail.postings) < diskPostingBlockDocs && encodedBytes+size+2 <= diskPostingBlockSize {
				tail.postings = append(tail.postings, posting)
				previous, encodedBytes = posting.id, encodedBytes+size
				continue
			}
			tail = nil
			previous, encodedBytes = 0, 0
		}
		if len(pending) == diskPostingBlockDocs || encodedBytes+size+2 > diskPostingBlockSize {
			if err := p.writeBlock(token, pending); err != nil {
				return err
			}
			meta.blocks++
			pending = pending[:0]
			previous, encodedBytes = 0, 0
		}
		if previous == 0 {
			size = len(encodeDiskPosting(appendDiskNumber(nil, posting.id), posting))
		}
		pending = append(pending, posting)
		previous, encodedBytes = posting.id, encodedBytes+size
	}
	if len(pending) != 0 {
		if err := p.writeBlock(token, pending); err != nil {
			return err
		}
		meta.blocks++
	}
	return nil
}

func (p *diskAdvancePlan) writeBlock(token Token, postings []diskPosting) error {
	if len(postings) == 0 || len(postings) > diskPostingBlockDocs {
		return diskInvalid("invalid rewritten posting block count")
	}
	value := encodeDiskBlock(len(postings), encodeRewritePostings(postings))
	if len(value) > diskPostingBlockSize {
		return diskInvalid("rewritten posting block exceeds size limit")
	}
	key := diskKey(p.footer.Base, diskPostingRow, uint8(token.Class), token.Term, postings[0].id)
	return p.row(key, value, false)
}

func encodeRewritePostings(postings []diskPosting) []byte {
	var payload []byte
	var prior uint64
	for _, posting := range postings {
		payload = encodeDiskPosting(appendDiskNumber(payload, posting.id-prior), posting)
		prior = posting.id
	}
	return payload
}
