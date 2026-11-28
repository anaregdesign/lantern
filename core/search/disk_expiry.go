package search

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/anaregdesign/tarp"
)

const diskQueryOverlayBytes = 8 << 20

var diskForwardCodec = mustDiskOrderedCodec[struct {
	Class uint8
	Term  string
}]()

type diskQueryOverlay struct {
	excluded         map[uint64]struct{}
	removedDF        map[Token][numDocumentFields]int
	addedDocs        map[uint64]diskDocValue
	terms            map[Token]*diskOverlayTerm
	bytes            int
	removedDeadlines uint64
	expiredDocuments uint64
}

func (q *diskQuery) applyDueDeadlines() error {
	first := q.index.footer.FirstDeadline
	if first.IsZero() || first.After(q.now) {
		return nil
	}
	prefix := diskKey(q.index.base, diskDeadlineRow)
	_, upper := tarp.PrefixRange(diskKey(q.index.base, diskDeadlineRow, q.now))
	q.overlay.initRemovals()
	seenDeadline := false
	for row, err := range q.snap.ScanRange(q.work.ctx, prefix, upper) {
		if err != nil {
			return err
		}
		if err := q.work.visit(WorkExpirationVisits, 1); err != nil {
			return err
		}
		if err := q.work.visit(WorkDiskReadBytes, int64(len(row.Key)+len(row.Value))); err != nil {
			return err
		}
		if len(row.Key) != len(prefix)+20 {
			return diskInvalid("malformed expiry key")
		}
		at, err := (tarp.OrderedTimeCodec{}).Decode(row.Key[len(prefix) : len(prefix)+12])
		if err != nil || at.After(q.now) || !at.After(q.index.footer.Source.Watermark) ||
			(!seenDeadline && !at.Equal(first)) {
			return diskInvalid("expiry key contradicts the source cut")
		}
		seenDeadline = true
		id := binary.BigEndian.Uint64(row.Key[len(prefix)+12:])
		if id == 0 || id > q.index.footer.Documents {
			return diskInvalid("expiry references an unknown document")
		}

		value, err := q.required(diskKey(q.index.base, diskDocIDRow, id))
		if err != nil {
			return err
		}
		doc, err := decodeDiskDoc(value)
		if err != nil {
			return err
		}
		if !at.Equal(doc.expiration) || !bytes.Equal(row.Value, doc.version) {
			return diskInvalid("expiry does not match its document")
		}
		if err := q.excludeDocument(id, doc); err != nil {
			return err
		}
	}
	if !seenDeadline {
		return diskInvalid("missing due expiry rows")
	}
	return q.work.check()
}

func (o *diskQueryOverlay) initRemovals() {
	if o.excluded == nil {
		o.excluded = make(map[uint64]struct{})
		o.removedDF = make(map[Token][numDocumentFields]int)
	}
}

func (o *diskQueryOverlay) reserve(size int) error {
	if size < 0 || size > diskQueryOverlayBytes-o.bytes {
		return &BudgetExceededError{Kind: WorkDiskReadBytes, Limit: diskQueryOverlayBytes}
	}
	o.bytes += size
	return nil
}

func (q *diskQuery) excludeDocument(id uint64, doc diskDocValue) error {
	q.overlay.initRemovals()
	if _, excluded := q.overlay.excluded[id]; excluded {
		return nil
	}
	if err := q.overlay.reserve(16); err != nil {
		return err
	}
	if err := q.subtractDocument(id, doc); err != nil {
		return err
	}
	q.overlay.excluded[id] = struct{}{}
	if !doc.expiration.IsZero() {
		q.overlay.removedDeadlines++
		if !doc.expiration.After(q.now) {
			q.overlay.expiredDocuments++
		}
	}
	return nil
}

// subtractDocument removes a base document's score contribution. Forward rows
// give exact field membership even when a term occurs in several field instances.
func (q *diskQuery) subtractDocument(id uint64, doc diskDocValue) error {
	for class, lengths := range doc.lengths {
		for field, length := range lengths {
			if length == 0 {
				continue
			}
			stats := &q.stats[class][field]
			if stats.Count == 0 || uint64(length) > stats.Length {
				return diskInvalid("expired document contradicts corpus statistics")
			}
			stats.Count--
			stats.Length -= uint64(length)
			if (stats.Count == 0) != (stats.Length == 0) {
				return diskInvalid("invalid remaining corpus statistics")
			}
		}
	}
	prefix := diskKey(q.index.base, diskForwardRow, id)
	var seen [numTokenClasses][numDocumentFields]int
	var terms [numTokenClasses][numDocumentFields]int
	for row, err := range diskRange(q.work.ctx, q.snap, prefix) {
		if err != nil {
			return err
		}
		if err := q.work.visit(WorkDiskReadBytes, int64(len(row.Key)+len(row.Value))); err != nil {
			return err
		}
		suffix, err := diskForwardCodec.Decode(row.Key[len(prefix):])
		if err != nil || int(suffix.Class) >= numTokenClasses || suffix.Term == "" {
			return diskInvalid("malformed forward term reference")
		}
		token := Token{Class: TokenClass(suffix.Class), Term: suffix.Term}
		d := diskDecoder{data: row.Value}
		posting := decodeDiskPosting(&d)
		if !d.success() {
			return diskInvalid("malformed forward posting")
		}
		counts, known := q.overlay.removedDF[token]
		if !known {
			if err := q.overlay.reserve(len(token.Term) + 64); err != nil {
				return err
			}
		}
		for field, frequency := range posting.frequency {
			if frequency == 0 {
				continue
			}
			if err := q.work.visit(WorkPostingVisits, 1); err != nil {
				return err
			}
			if int(frequency) > doc.lengths[token.Class][field]-seen[token.Class][field] || counts[field] == math.MaxInt {
				return diskInvalid("expired posting contradicts document metadata")
			}
			if err := q.capacity.removePosting(posting.positions[field]); err != nil {
				return err
			}
			seen[token.Class][field] += int(frequency)
			terms[token.Class][field]++
			counts[field]++
		}
		q.overlay.removedDF[token] = counts
	}
	for class, fields := range doc.lengths {
		for field, length := range fields {
			if seen[class][field] < min(length, math.MaxUint16) || terms[class][field] != doc.terms[class][field] {
				return diskInvalid("incomplete forward term references")
			}
		}
	}
	return nil
}

func (q *diskQuery) liveDF(token Token, stored diskTermValue) ([numDocumentFields]int, bool, error) {
	df := stored.df
	removed := q.overlay.removedDF[token]
	live := false
	for field := range df {
		if removed[field] > df[field] {
			return df, false, diskInvalid("expiry document frequency underflows")
		}
		df[field] -= removed[field]
		if added := q.overlay.terms[token]; added != nil {
			if added.df[field] > math.MaxInt-df[field] {
				return df, false, diskInvalid("live document frequency overflows")
			}
			df[field] += added.df[field]
		}
		if uint64(df[field]) > q.stats[token.Class][field].Count {
			return df, false, diskInvalid("live document frequency exceeds corpus")
		}
		live = live || df[field] != 0
	}
	return df, live, nil
}
