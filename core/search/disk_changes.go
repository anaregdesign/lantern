package search

import (
	"bytes"
	"errors"
	"iter"
	"math"
	"strings"

	"github.com/anaregdesign/tarp"
)

const maxDiskChangedDocuments = 4096

type diskOverlayTerm struct {
	df       [numDocumentFields]int
	postings []diskPosting
}

func (q *diskQuery) applyChanges(changes iter.Seq2[DiskDocument, error]) error {
	if changes == nil {
		return nil
	}
	var previous string
	nextID := q.index.footer.Documents
	for input, streamErr := range changes {
		if streamErr != nil {
			return streamErr
		}
		if err := q.work.visit(WorkChangeVisits, 1); err != nil {
			return err
		}
		if input.Key <= previous || len(input.Key) > diskMaxKeyBytes {
			return diskInvalid("invalid or unordered changed document")
		}
		previous = input.Key
		if err := q.excludeByKey(input.Key); err != nil {
			return err
		}
		if input.Document == nil || !expirationLiveAt(input.Expiration, q.now) {
			continue
		}
		prepared, _, err := q.index.config.Prepare(input.Document)
		if err != nil {
			return err
		}
		if prepared.postings == 0 {
			continue
		}
		if nextID == math.MaxUint64 {
			return diskInvalid("overlay document ID exhausted")
		}
		nextID++
		if err := q.addChangedDocument(nextID, input, prepared); err != nil {
			return err
		}
	}
	return q.work.check()
}

func (q *diskQuery) excludeByKey(key string) error {
	value, err := q.read(diskKey(q.index.base, diskDocKeyRow, key))
	if errors.Is(err, tarp.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	d := diskDecoder{data: value}
	id := d.number()
	if !d.success() || id == 0 || id > q.index.footer.Documents {
		return diskInvalid("invalid reverse document reference")
	}
	value, err = q.required(diskKey(q.index.base, diskDocIDRow, id))
	if err != nil {
		return err
	}
	doc, err := decodeDiskDoc(value)
	if err != nil {
		return err
	}
	if doc.key != key {
		return diskInvalid("reverse document reference does not match its key")
	}
	return q.excludeDocument(id, doc)
}

func (q *diskQuery) addChangedDocument(id uint64, input DiskDocument, prepared PreparedDocument) error {
	if err := q.overlay.reserve(128 + len(input.Key)); err != nil {
		return err
	}
	if q.overlay.addedDocs == nil {
		q.overlay.addedDocs = make(map[uint64]diskDocValue)
		q.overlay.terms = make(map[Token]*diskOverlayTerm)
	}
	doc := diskDocValue{key: strings.Clone(input.Key), expiration: input.Expiration}
	if q.keepVersions {
		if len(input.Version) > diskMaxKeyBytes {
			return diskInvalid("invalid changed document version")
		}
		if err := q.overlay.reserve(len(input.Version)); err != nil {
			return err
		}
		doc.version = bytes.Clone(input.Version)
	}
	for class, analyzed := range prepared.classes {
		doc.lengths[class] = analyzed.fieldLengths
		for field, length := range analyzed.fieldLengths {
			if length == 0 {
				continue
			}
			stats := &q.stats[class][field]
			if uint64(length) > math.MaxInt-stats.Length {
				return diskInvalid("overlay corpus length exhausted")
			}
			stats.Count++
			stats.Length += uint64(length)
		}
		for _, term := range analyzed.terms {
			for field, occurrence := range term.fields {
				if occurrence.frequency != 0 {
					doc.terms[class][field]++
				}
			}
			if err := q.addChangedPosting(id, TokenClass(class), term); err != nil {
				return err
			}
		}
	}
	q.overlay.addedDocs[id] = doc
	return nil
}

func (q *diskQuery) addChangedPosting(id uint64, class TokenClass, term preparedTerm) error {
	if err := q.overlay.reserve(256); err != nil {
		return err
	}
	token := Token{Class: class, Term: term.term}
	entry := q.overlay.terms[token]
	if entry == nil {
		if err := q.overlay.reserve(64 + len(term.term)); err != nil {
			return err
		}
		entry = &diskOverlayTerm{}
		token.Term = strings.Clone(term.term)
		q.overlay.terms[token] = entry
	}
	posting := diskPosting{id: id}
	for field, occurrence := range term.fields {
		if occurrence.frequency == 0 {
			continue
		}
		if err := q.work.visit(WorkPostingVisits, 1); err != nil {
			return err
		}
		if err := q.capacity.addPosting(uint64(len(occurrence.positions))); err != nil {
			return err
		}
		entry.df[field]++
		posting.frequency[field] = clampTF(occurrence.frequency)
		if len(occurrence.positions) != 0 {
			if len(occurrence.positions) > diskQueryOverlayBytes/8 {
				return &BudgetExceededError{Kind: WorkDiskReadBytes, Limit: diskQueryOverlayBytes}
			}
			if err := q.overlay.reserve(8 * len(occurrence.positions)); err != nil {
				return err
			}
			if posting.overlay == nil {
				posting.overlay = new([numDocumentFields][]uint64)
			}
			posting.positions[field] = uint64(len(occurrence.positions))
			posting.overlay[field] = occurrence.positions
		}
	}
	entry.postings = append(entry.postings, posting)
	return nil
}
