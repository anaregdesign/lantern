package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"time"

	"github.com/anaregdesign/tarp"
)

const diskBuildBatchBytes = 4 << 20

type diskInputSuffix struct {
	Class uint8
	Term  string
	ID    uint64
}

var diskInputCodec = mustDiskOrderedCodec[diskInputSuffix]()

func mustDiskOrderedCodec[T any]() tarp.Codec[T] {
	codec, err := tarp.NewOrderedCodec[T]()
	if err != nil {
		panic(err)
	}
	return codec
}

type diskBuildBatch struct {
	db      *tarp.DB
	batch   tarp.WriteBatch
	sizer   tarp.WriteBatchSizer
	bytes   int
	records uint64
}

func (b *diskBuildBatch) put(ctx context.Context, key, value []byte, final bool) error {
	if err := b.reserve(ctx, key, value); err != nil {
		return err
	}
	if final {
		if b.records == math.MaxUint64 {
			return diskInvalid("record count exhausted")
		}
		b.records++
	}
	b.batch.Put(key, value)
	return nil
}

func (b *diskBuildBatch) remove(ctx context.Context, key []byte) error {
	if err := b.reserve(ctx, key, nil); err != nil {
		return err
	}
	b.batch.Delete(key)
	return nil
}

func (b *diskBuildBatch) reserve(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	size := len(key) + len(value)
	next := b.sizer
	err := next.Add(uint64(len(key)), uint64(len(value)))
	if b.batch.Len() != 0 && (err != nil || size > diskBuildBatchBytes-b.bytes) {
		if err := b.flush(); err != nil {
			return err
		}
		next = b.sizer
		err = next.Add(uint64(len(key)), uint64(len(value)))
	}
	if err != nil {
		return err
	}
	b.bytes += size
	b.sizer = next
	return nil
}

func (b *diskBuildBatch) flush() error {
	if b.batch.Len() == 0 {
		return nil
	}
	if err := b.db.Apply(&b.batch); err != nil {
		return err
	}
	b.batch.Reset()
	b.sizer = b.db.WriteBatchSizer()
	b.bytes = 0
	return nil
}

// BuildDiskBase builds an immutable base from an admitted, ascending key
// stream. The caller serializes builders and allocates a fresh base ID. A
// failed build leaves an unpublished reserved base; it can never be opened.
func BuildDiskBase(ctx context.Context, db *tarp.DB, base uint64, source DiskSource, documents iter.Seq2[DiskDocument, error], analyzer Analyzer, opts ...IndexOption) error {
	if db == nil || base == 0 || !validDiskSource(source) || documents == nil || analyzer == nil {
		return diskInvalid("missing database, source, document stream or Analyzer")
	}
	config := diskSearchConfig(analyzer, nil, opts)
	marker := diskKey(base, diskMarker)
	_, err := db.GetContext(ctx, marker)
	if err == nil {
		return diskInvalid("base ID already allocated")
	}
	if !errors.Is(err, tarp.ErrNotFound) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := db.Put(marker, nil); err != nil {
		return err
	}
	footer := diskFooter{Layout: diskLayout, Base: base, Source: source, Positions: config.positions}
	batch := &diskBuildBatch{db: db, sizer: db.WriteBatchSizer()}
	if err := writeDiskDocuments(ctx, batch, &footer, config, documents); err != nil {
		return err
	}
	if err := batch.flush(); err != nil {
		return err
	}
	if err := writeDiskTerms(ctx, batch, &footer, config.limits.MaxLiveTerms); err != nil {
		return err
	}
	for class, stats := range footer.Stats {
		for field, s := range stats {
			value := appendDiskNumber(appendDiskNumber(nil, s.Count), s.Length)
			if err := batch.put(ctx, diskKey(base, diskStatsRow, uint8(class), uint8(field)), value, true); err != nil {
				return err
			}
		}
	}
	if err := batch.flush(); err != nil {
		return err
	}
	footer.Records = batch.records
	if err := footer.validateCapacity(); err != nil {
		return err
	}
	footer.Digest, err = verifyDiskBuild(ctx, db, base, footer.Records, marker)
	if err != nil {
		return err
	}
	value, err := json.Marshal(footer)
	if err != nil {
		return err
	}
	if len(value) > diskMaxFooterBytes {
		return diskInvalid("commit footer exceeds size limit")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.Put(diskKey(base, diskFooterRow), value)
}

func writeDiskDocuments(ctx context.Context, batch *diskBuildBatch, footer *diskFooter, config *InvertedIndex[string, Document], documents iter.Seq2[DiskDocument, error]) error {
	var previous string
	for input, streamErr := range documents {
		if streamErr != nil {
			return streamErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if input.Key <= previous || len(input.Key) > diskMaxKeyBytes || len(input.Version) > diskMaxKeyBytes || input.Document == nil {
			return diskInvalid("invalid or unordered source document")
		}
		previous = input.Key
		if !expirationLiveAt(input.Expiration, footer.Source.Watermark) {
			continue
		}
		prepared, _, err := config.Prepare(input.Document)
		if err != nil {
			return err
		}
		if prepared.postings == 0 {
			continue
		}
		if footer.Documents == math.MaxUint64 {
			return diskInvalid("document ID exhausted")
		}
		if int64(prepared.postings) > math.MaxInt64-footer.Capacity.Postings || int64(prepared.positionEntries) > math.MaxInt64-footer.Capacity.Positions {
			return diskInvalid("posting or position count exhausted")
		}
		footer.Documents++
		footer.Capacity.Postings += int64(prepared.postings)
		footer.Capacity.Positions += int64(prepared.positionEntries)
		if err := limitError(LimitLivePostings, footer.Capacity.Postings, config.limits.MaxLivePostings); err != nil {
			return err
		}
		if err := limitError(LimitPositionEntries, footer.Capacity.Positions, config.limits.MaxPositionEntries); err != nil {
			return err
		}
		if err := writeDiskDocument(ctx, batch, footer, input, prepared); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func writeDiskDocument(ctx context.Context, batch *diskBuildBatch, footer *diskFooter, input DiskDocument, prepared PreparedDocument) error {
	base, id := footer.Base, footer.Documents
	doc := diskDocValue{key: input.Key, version: input.Version, expiration: input.Expiration}
	for class, data := range prepared.classes {
		doc.lengths[class] = data.fieldLengths
		for field, length := range data.fieldLengths {
			if length == 0 {
				continue
			}
			stats := &footer.Stats[class][field]
			if uint64(length) > math.MaxInt-stats.Length {
				return diskInvalid("corpus length exhausted")
			}
			stats.Count++
			stats.Length += uint64(length)
		}
		for _, term := range data.terms {
			for field, occurrence := range term.fields {
				if occurrence.frequency != 0 {
					doc.terms[class][field]++
				}
			}
			if err := writeDiskOccurrence(ctx, batch, base, id, uint8(class), term); err != nil {
				return err
			}
		}
	}
	if err := batch.put(ctx, diskKey(base, diskDocIDRow, id), encodeDiskDoc(doc), true); err != nil {
		return err
	}
	if err := batch.put(ctx, diskKey(base, diskDocKeyRow, input.Key), appendDiskNumber(nil, id), true); err != nil {
		return err
	}
	if !input.Expiration.IsZero() {
		if footer.FirstDeadline.IsZero() || input.Expiration.Before(footer.FirstDeadline) {
			footer.FirstDeadline = input.Expiration
		}
		return batch.put(ctx, diskKey(base, diskDeadlineRow, input.Expiration, id), input.Version, true)
	}
	return nil
}

func writeDiskOccurrence(ctx context.Context, batch *diskBuildBatch, base, id uint64, class uint8, term preparedTerm) error {
	var posting diskPosting
	for field, data := range term.fields {
		posting.frequency[field] = clampTF(data.frequency)
		posting.positions[field] = uint64(len(data.positions))
		if len(data.positions) <= diskInlinePositions {
			packed := packPositions(data.positions)
			if len(packed) <= diskInlinePositions {
				posting.inline[field] = packed
				continue
			}
		}
		for offset := 0; offset < len(data.positions); offset += diskPositionChunk {
			positions := data.positions[offset:min(offset+diskPositionChunk, len(data.positions))]
			key := diskKey(base, diskPositionRow, class, term.term, id, uint8(field), uint64(offset/diskPositionChunk))
			if err := batch.put(ctx, key, packPositions(positions), true); err != nil {
				return err
			}
		}
	}
	value := encodeDiskPosting(nil, posting)
	if err := batch.put(ctx, diskKey(base, diskForwardRow, id, class, term.term), value, true); err != nil {
		return err
	}
	return batch.put(ctx, diskKey(base, diskInputRow, class, term.term, id), value, false)
}

type diskTermBuilder struct {
	term       diskInputSuffix
	stats      diskTermValue
	blockFirst uint64
	previous   uint64
	count      int
	payload    []byte
}

func (t *diskTermBuilder) finishBlock(ctx context.Context, batch *diskBuildBatch, base uint64) error {
	if t.count == 0 {
		return nil
	}
	key := diskKey(base, diskPostingRow, t.term.Class, t.term.Term, t.blockFirst)
	if err := batch.put(ctx, key, encodeDiskBlock(t.count, t.payload), true); err != nil {
		return err
	}
	t.stats.blocks++
	t.count = 0
	t.previous = 0
	t.payload = t.payload[:0]
	return nil
}

func (t *diskTermBuilder) finishTerm(ctx context.Context, batch *diskBuildBatch, base uint64) error {
	if t.stats.documents == 0 {
		return nil
	}
	if err := t.finishBlock(ctx, batch, base); err != nil {
		return err
	}
	key := diskKey(base, diskTermRow, t.term.Class, t.term.Term)
	if err := batch.put(ctx, key, encodeDiskTerm(t.stats), true); err != nil {
		return err
	}
	t.stats = diskTermValue{}
	return nil
}

func writeDiskTerms(ctx context.Context, batch *diskBuildBatch, footer *diskFooter, termLimit int) error {
	base := footer.Base
	snap, err := batch.db.Snapshot()
	if err != nil {
		return err
	}
	defer snap.Close()
	prefix := diskKey(base, diskInputRow)
	var term diskTermBuilder
	for row, readErr := range diskRange(ctx, snap, prefix) {
		if readErr != nil {
			return readErr
		}
		input, err := diskInputCodec.Decode(row.Key[len(prefix):])
		if err != nil || int(input.Class) >= numTokenClasses || input.Term == "" || input.ID == 0 {
			return diskInvalid("malformed sorted posting input")
		}
		if input.Class != term.term.Class || input.Term != term.term.Term {
			if err := term.finishTerm(ctx, batch, base); err != nil {
				return err
			}
			if footer.Capacity.Terms == math.MaxInt64 {
				return diskInvalid("term count exhausted")
			}
			footer.Capacity.Terms++
			if err := limitError(LimitLiveTerms, footer.Capacity.Terms, int64(termLimit)); err != nil {
				return err
			}
			term.term = input
		}
		d := diskDecoder{data: row.Value}
		posting := decodeDiskPosting(&d)
		if !d.success() || input.ID <= term.previous {
			return diskInvalid("unordered posting input")
		}
		encoded := appendDiskNumber(nil, input.ID-term.previous)
		encoded = append(encoded, row.Value...)
		if term.count == diskPostingBlockDocs || len(term.payload)+len(encoded)+2 > diskPostingBlockSize {
			if err := term.finishBlock(ctx, batch, base); err != nil {
				return err
			}
			encoded = append(appendDiskNumber(encoded[:0], input.ID), row.Value...)
		}
		if term.count == 0 {
			term.blockFirst = input.ID
		}
		term.previous = input.ID
		term.count++
		term.payload = append(term.payload, encoded...)
		term.stats.documents++
		for field, frequency := range posting.frequency {
			if frequency != 0 {
				term.stats.df[field]++
			}
		}
		if err := batch.remove(ctx, row.Key); err != nil {
			return err
		}
	}
	return term.finishTerm(ctx, batch, base)
}

func verifyDiskBuild(ctx context.Context, db *tarp.DB, base, expected uint64, marker []byte) ([32]byte, error) {
	snap, err := db.Snapshot()
	if err != nil {
		return [32]byte{}, err
	}
	defer snap.Close()
	digest := sha256.New()
	prefix := diskBasePrefix(base)
	var count uint64
	for row, readErr := range diskRange(ctx, snap, prefix) {
		if readErr != nil {
			return [32]byte{}, readErr
		}
		if bytes.Equal(row.Key, marker) {
			continue
		}
		if len(row.Key) <= len(prefix) || row.Key[len(prefix)] == diskInputRow {
			return [32]byte{}, diskInvalid("unfinished index staging rows")
		}
		var framing [20]byte
		header := appendDiskNumber(framing[:0], uint64(len(row.Key)))
		header = appendDiskNumber(header, uint64(len(row.Value)))
		digest.Write(header)
		digest.Write(row.Key)
		digest.Write(row.Value)
		count++
	}
	if count != expected {
		return [32]byte{}, fmt.Errorf("%w: built %d rows, verified %d", ErrDiskIndex, expected, count)
	}
	return [32]byte(digest.Sum(nil)), nil
}

// Source returns the graph cut certified by the base's commit footer.
func (idx *DiskIndex) Source() DiskSource { return idx.footer.Source }

// NextDeadline returns the first finite expiry represented in the fixed base.
func (idx *DiskIndex) NextDeadline() time.Time { return idx.footer.FirstDeadline }

// RetireDiskBase removes an unpublished or superseded base in bounded Tarp
// batches. The caller serializes base writers and has stopped selecting the
// base for new readers. A query already holding a Snapshot keeps its old view.
// Repeating retirement after cancellation is safe. The ID remains reserved.
func RetireDiskBase(ctx context.Context, db *tarp.DB, base uint64) error {
	if db == nil || base == 0 {
		return diskInvalid("missing database or base")
	}
	marker := diskKey(base, diskMarker)
	if _, err := db.GetContext(ctx, marker); err != nil {
		if errors.Is(err, tarp.ErrNotFound) {
			return nil
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := db.Delete(diskKey(base, diskFooterRow)); err != nil && !errors.Is(err, tarp.ErrNotFound) {
		return err
	}
	snap, err := db.Snapshot()
	if err != nil {
		return err
	}
	defer snap.Close()
	batch := &diskBuildBatch{db: db, sizer: db.WriteBatchSizer()}
	lower, upper := tarp.PrefixRange(diskBasePrefix(base))
	for key, err := range snap.KeysRange(ctx, lower, upper) {
		if err != nil {
			return err
		}
		if !bytes.Equal(key, marker) {
			if err := batch.remove(ctx, key); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return batch.flush()
}
