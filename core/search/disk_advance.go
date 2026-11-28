package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"iter"
	"math"
	"slices"
	"time"

	"github.com/anaregdesign/tarp"
)

// AdvanceDiskBase materializes a verified source suffix and due TTLs with one
// atomic Tarp batch. The caller serializes index writers and publishes the
// returned handle in the same ownership scope used for new Search captures.
// A reader already inside a Tarp Snapshot retains the previous complete view.
// A bounded-work rejection publishes nothing. Reopen and check the exact
// certified footer after an uncertain Tarp commit before retrying.
func (idx *DiskIndex) AdvanceDiskBase(ctx context.Context, source DiskSource, changes iter.Seq2[DiskDocument, error], budget Budget) (*DiskIndex, error) {
	old := idx.footer.Source
	if !validDiskSource(source) || old.Store != source.Store || old.Corpus != source.Corpus ||
		old.Fingerprint != source.Fingerprint || old.Epoch != source.Epoch || source.Sequence < old.Sequence ||
		source.Watermark.Before(old.Watermark) || (source.Sequence == old.Sequence && source.Digest != old.Digest) ||
		(source.Sequence > old.Sequence && changes == nil) {
		return nil, diskInvalid("incompatible source advancement")
	}
	if sameDiskSource(old, source) && changes == nil {
		return idx, nil
	}
	budget = diskBoundBudget(budget)
	work := newWorkTracker(ctx, budget)
	ctx = work.ctx
	snap, err := idx.db.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snap.Close()
	q := &diskQuery{index: idx, snap: snap, work: work, now: source.Watermark, stats: idx.footer.Stats, capacity: idx.footer.Capacity, keepVersions: true}
	stored, err := q.required(diskKey(idx.base, diskFooterRow))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(stored, idx.seal) {
		return nil, diskInvalid("cannot advance a superseded disk view")
	}
	if err := q.applyChanges(changes); err != nil {
		return nil, err
	}
	if source.Sequence == old.Sequence && q.work.stats.ChangeVisits != 0 {
		return nil, diskInvalid("changed documents without a new source publication")
	}
	if sameDiskSource(old, source) {
		return idx, nil
	}
	if err := q.applyDueDeadlines(); err != nil {
		return nil, err
	}

	p := &diskAdvancePlan{
		query: q, rows: make(map[string]diskAdvanceRow), terms: make(map[Token]*diskTermRewrite),
		footer: idx.footer,
	}
	p.footer.Source, p.footer.Stats = source, q.stats
	for id := range q.overlay.addedDocs {
		p.footer.Documents = max(p.footer.Documents, id)
	}
	p.footer.LiveDocuments, p.footer.Deadlines, err = q.effectiveDocumentCounts()
	if err != nil {
		return nil, err
	}
	p.footer.Capacity, err = q.effectiveCapacity()
	if err != nil {
		return nil, err
	}
	if err := p.footer.validateCapacity(); err != nil {
		return nil, err
	}
	if err := p.footer.Capacity.validateLimits(idx.config.limits); err != nil {
		return nil, err
	}
	if err := p.prepare(); err != nil {
		return nil, err
	}
	return p.publish(ctx)
}

type diskAdvanceRow struct {
	value  []byte
	remove bool
}

type diskAdvancePlan struct {
	query  *diskQuery
	rows   map[string]diskAdvanceRow
	terms  map[Token]*diskTermRewrite
	footer diskFooter
	bytes  int
}

func (p *diskAdvancePlan) reserve(size int) error {
	if size < 0 || size > diskBuildBatchBytes-p.bytes {
		return &BudgetExceededError{Kind: WorkDiskReadBytes, Limit: diskBuildBatchBytes}
	}
	p.bytes += size
	return nil
}

func (p *diskAdvancePlan) row(key, value []byte, remove bool) error {
	if err := p.query.work.check(); err != nil {
		return err
	}
	if err := p.reserve(len(key) + len(value) + 64); err != nil {
		return err
	}
	p.rows[string(key)] = diskAdvanceRow{value: value, remove: remove}
	return nil
}

func (p *diskAdvancePlan) prepare() error {
	q := p.query
	for id := range q.overlay.excluded {
		if err := p.removeDocument(id); err != nil {
			return err
		}
	}
	for id, doc := range q.overlay.addedDocs {
		if err := p.addDocument(id, doc); err != nil {
			return err
		}
	}
	for token := range q.overlay.terms {
		p.term(token)
	}
	for token, plan := range p.terms {
		if err := p.writeTerm(token, plan); err != nil {
			return err
		}
	}
	for class, stats := range q.stats {
		for field, s := range stats {
			if s == q.index.footer.Stats[class][field] {
				continue
			}
			value := appendDiskNumber(appendDiskNumber(nil, s.Count), s.Length)
			if err := p.row(diskKey(p.footer.Base, diskStatsRow, uint8(class), uint8(field)), value, false); err != nil {
				return err
			}
		}
	}
	return p.findFirstDeadline()
}

func (p *diskAdvancePlan) addDocument(id uint64, doc diskDocValue) error {
	base := p.footer.Base
	if err := p.row(diskKey(base, diskDocIDRow, id), encodeDiskDoc(doc), false); err != nil {
		return err
	}
	if err := p.row(diskKey(base, diskDocKeyRow, doc.key), appendDiskNumber(nil, id), false); err != nil {
		return err
	}
	if !doc.expiration.IsZero() {
		return p.row(diskKey(base, diskDeadlineRow, doc.expiration, id), doc.version, false)
	}
	return nil
}

func (p *diskAdvancePlan) removeDocument(id uint64) error {
	q := p.query
	base := p.footer.Base
	doc, err := q.document(id)
	if err != nil {
		return err
	}
	if err := p.row(diskKey(base, diskDocIDRow, id), nil, true); err != nil {
		return err
	}
	if err := p.row(diskKey(base, diskDocKeyRow, doc.key), nil, true); err != nil {
		return err
	}
	if !doc.expiration.IsZero() {
		if err := p.row(diskKey(base, diskDeadlineRow, doc.expiration, id), nil, true); err != nil {
			return err
		}
	}
	prefix := diskKey(base, diskForwardRow, id)
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
		d := diskDecoder{data: row.Value}
		posting := decodeDiskPosting(&d)
		if !d.success() {
			return diskInvalid("malformed forward posting")
		}
		if err := p.row(row.Key, nil, true); err != nil {
			return err
		}
		token := Token{Class: TokenClass(suffix.Class), Term: suffix.Term}
		if err := p.removePosting(token, id, posting); err != nil {
			return err
		}
	}
	return nil
}

func (p *diskAdvancePlan) findFirstDeadline() error {
	q := p.query
	var first time.Time
	for _, doc := range q.overlay.addedDocs {
		if !doc.expiration.IsZero() && (first.IsZero() || doc.expiration.Before(first)) {
			first = doc.expiration
		}
	}
	prefix := diskKey(p.footer.Base, diskDeadlineRow)
	for row, err := range diskRange(q.work.ctx, q.snap, prefix) {
		if err != nil {
			return err
		}
		if err := q.work.visit(WorkDiskReadBytes, int64(len(row.Key)+len(row.Value))); err != nil {
			return err
		}
		if len(row.Key) != len(prefix)+20 {
			return diskInvalid("malformed expiry key")
		}
		id := binary.BigEndian.Uint64(row.Key[len(prefix)+12:])
		if _, removed := q.overlay.excluded[id]; removed {
			continue
		}
		expiration, err := (tarp.OrderedTimeCodec{}).Decode(row.Key[len(prefix) : len(prefix)+12])
		if err != nil || !expiration.After(p.footer.Source.Watermark) || id == 0 || id > q.index.footer.Documents {
			return diskInvalid("invalid remaining deadline")
		}
		if first.IsZero() || expiration.Before(first) {
			first = expiration
		}
		break
	}
	p.footer.FirstDeadline = first
	return nil
}

func (p *diskAdvancePlan) publish(ctx context.Context) (*DiskIndex, error) {
	idx := p.query.index
	keys := make([]string, 0, len(p.rows))
	for key := range p.rows {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var batch tarp.WriteBatch
	sizer := idx.db.WriteBatchSizer()
	digest := sha256.New()
	digest.Write(idx.footer.Digest[:])
	for _, raw := range keys {
		key, change := []byte(raw), p.rows[raw]
		_, err := p.query.read(key)
		switch {
		case errors.Is(err, tarp.ErrNotFound):
			if change.remove || p.footer.Records == math.MaxUint64 {
				return nil, diskInvalid("invalid index row transition")
			}
			p.footer.Records++
		case err != nil:
			return nil, err
		case change.remove:
			if p.footer.Records == 0 {
				return nil, diskInvalid("index row count underflow")
			}
			p.footer.Records--
		}
		if err := sizer.Add(uint64(len(key)), uint64(len(change.value))); err != nil {
			return nil, errors.Join(ErrBudgetExceeded, err)
		}
		var frame [20]byte
		framing := appendDiskNumber(frame[:0], uint64(len(key)))
		framing = appendDiskNumber(framing, uint64(len(change.value)))
		digest.Write(framing)
		digest.Write(key)
		if change.remove {
			digest.Write([]byte{1})
			batch.Delete(key)
		} else {
			digest.Write([]byte{0})
			digest.Write(change.value)
			batch.Put(key, change.value)
		}
	}
	p.footer.Digest = [32]byte(digest.Sum(nil))
	seal, err := json.Marshal(p.footer)
	if err != nil {
		return nil, err
	}
	key := diskKey(idx.base, diskFooterRow)
	if err := sizer.Add(uint64(len(key)), uint64(len(seal))); err != nil {
		return nil, errors.Join(ErrBudgetExceeded, err)
	}
	batch.Put(key, seal)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := idx.db.Apply(&batch); err != nil {
		return nil, err
	}
	return &DiskIndex{db: idx.db, base: idx.base, footer: p.footer, config: idx.config, seal: seal, readers: idx.readers}, nil
}
