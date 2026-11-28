package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"strings"
	"time"

	"github.com/anaregdesign/tarp"
)

// DiskSource identifies the authoritative graph and the exact cut from which
// an immutable index was built. Fingerprint covers analysis, projection and
// scoring configuration. Watermark is the fixed liveness instant of this base.
type DiskSource struct {
	Store       string
	Corpus      string
	Fingerprint string
	Epoch       uint64
	Sequence    uint64
	Digest      [32]byte
	Watermark   time.Time
}

// DiskDocument is one accepted graph Vertex at the supplied source cut.
// Version is opaque to search. The stream is in strictly ascending Key order.
type DiskDocument struct {
	Key        string
	Version    []byte
	Expiration time.Time
	Document   Document
}

// ErrDiskIndex identifies missing, incompatible, or invalid immutable index data.
var ErrDiskIndex = errors.New("search: invalid disk index")

// DiskIndex is a read-only fixed-cut index. The caller owns the database and
// must keep the base reachable for as long as this handle can start queries.
type DiskIndex struct {
	db      *tarp.DB
	base    uint64
	footer  diskFooter
	config  *InvertedIndex[string, Document]
	seal    []byte
	readers chan struct{}
}

const (
	diskDomain uint8 = 0xc4
	diskLayout uint8 = 3
	diskMarker uint8 = iota + 1
	diskFooterRow
	diskDocIDRow
	diskDocKeyRow
	diskForwardRow
	diskPositionRow
	diskDeadlineRow
	diskStatsRow
	diskTermRow
	diskPostingRow
	diskInputRow
)

const (
	diskPostingBlockDocs = 256
	diskPostingBlockSize = 16 << 10
	diskInlinePositions  = 64
	diskPositionChunk    = 1024
	diskMaxKeyBytes      = 1 << 20
	diskMaxFooterBytes   = 16 << 10
)

type diskFieldStats struct {
	Count  uint64
	Length uint64
}

type diskFooter struct {
	Layout        uint8
	Base          uint64
	Source        DiskSource
	Positions     bool
	Documents     uint64 // document ID high-water mark
	LiveDocuments uint64
	Deadlines     uint64
	Records       uint64
	Digest        [32]byte
	Stats         [numTokenClasses][numDocumentFields]diskFieldStats
	FirstDeadline time.Time
	Capacity      diskCapacity
}

type diskDocValue struct {
	key        string
	version    []byte
	expiration time.Time
	lengths    [numTokenClasses][numDocumentFields]int
	terms      [numTokenClasses][numDocumentFields]int
}

type diskTermValue struct {
	documents uint64
	blocks    uint64
	df        [numDocumentFields]int
}

type diskPosting struct {
	id        uint64
	frequency [numDocumentFields]uint16
	positions [numDocumentFields]uint64
	inline    [numDocumentFields][]byte
	overlay   *[numDocumentFields][]uint64
}

func diskKey(base uint64, family uint8, suffix ...any) []byte {
	fields := append([]any{diskDomain, diskLayout, base, family}, suffix...)
	key, err := tarp.EncodeOrderedTuplePrefix(fields...)
	if err != nil {
		panic(err) // every internal suffix is a fixed, supported key type
	}
	return key
}

func diskBasePrefix(base uint64) []byte {
	key, err := tarp.EncodeOrderedTuplePrefix(diskDomain, diskLayout, base)
	if err != nil {
		panic(err)
	}
	return key
}

func diskRange(ctx context.Context, snap *tarp.Snapshot, prefix []byte) iter.Seq2[tarp.Entry, error] {
	lower, upper := tarp.PrefixRange(prefix)
	return snap.ScanRange(ctx, lower, upper)
}

func diskInvalid(detail string) error { return fmt.Errorf("%w: %s", ErrDiskIndex, detail) }

func validDiskSource(source DiskSource) bool {
	return source.Store != "" && len(source.Store) <= 512 &&
		source.Corpus != "" && len(source.Corpus) <= 512 &&
		source.Fingerprint != "" && len(source.Fingerprint) <= 512 &&
		source.Epoch != 0 && !source.Watermark.IsZero()
}

func sameDiskSource(a, b DiskSource) bool {
	return a.Store == b.Store && a.Corpus == b.Corpus && a.Fingerprint == b.Fingerprint &&
		a.Epoch == b.Epoch && a.Sequence == b.Sequence && a.Digest == b.Digest && a.Watermark.Equal(b.Watermark)
}

func diskSearchConfig(analyzer Analyzer, scorer Scorer, opts []IndexOption) *InvertedIndex[string, Document] {
	config := NewInvertedIndex[string, Document](analyzer, scorer, strings.Compare, opts...)
	if config.limits.MaxDocumentBytes <= 0 {
		config.limits.MaxDocumentBytes = diskMaxKeyBytes
	}
	if config.limits.MaxDocumentTokens <= 0 {
		config.limits.MaxDocumentTokens = 250_000
	}
	if config.limits.MaxDocumentTerms <= 0 {
		config.limits.MaxDocumentTerms = 100_000
	}
	return config
}

// OpenDiskIndex opens a completed base at an exact source cut. Query snapshots
// independently verify the chosen footer before reading other rows.
func OpenDiskIndex(ctx context.Context, db *tarp.DB, base uint64, source DiskSource, analyzer Analyzer, scorer Scorer, opts ...IndexOption) (*DiskIndex, error) {
	if db == nil || analyzer == nil || base == 0 || !validDiskSource(source) {
		return nil, diskInvalid("missing database, Analyzer, base or source")
	}
	config := diskSearchConfig(analyzer, scorer, opts)
	snap, err := db.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snap.Close()
	value, err := snap.GetContext(ctx, diskKey(base, diskFooterRow))
	if errors.Is(err, tarp.ErrNotFound) {
		return nil, diskInvalid("missing commit footer")
	}
	if err != nil {
		return nil, err
	}
	var footer diskFooter
	if len(value) > diskMaxFooterBytes || json.Unmarshal(value, &footer) != nil ||
		footer.Layout != diskLayout || footer.Base != base || !sameDiskSource(footer.Source, source) ||
		footer.Positions != config.positions || footer.Digest == ([32]byte{}) ||
		(!footer.FirstDeadline.IsZero() && !footer.FirstDeadline.After(source.Watermark)) {
		return nil, diskInvalid("incompatible commit footer")
	}
	if err := footer.validateCapacity(); err != nil {
		return nil, err
	}
	for class, fields := range footer.Stats {
		for fieldID, field := range fields {
			if field.Count > footer.Documents || field.Length > math.MaxInt || field.Length < field.Count ||
				(field.Count == 0) != (field.Length == 0) {
				return nil, diskInvalid("invalid corpus statistics")
			}
			value, err := snap.GetContext(ctx, diskKey(base, diskStatsRow, uint8(class), uint8(fieldID)))
			if errors.Is(err, tarp.ErrNotFound) {
				return nil, diskInvalid("missing corpus statistics")
			}
			if err != nil {
				return nil, err
			}
			d := diskDecoder{data: value}
			if (diskFieldStats{Count: d.number(), Length: d.number()}) != field || !d.success() {
				return nil, diskInvalid("statistics disagree with commit footer")
			}
		}
	}
	return &DiskIndex{db: db, base: base, footer: footer, config: config, seal: value, readers: make(chan struct{}, 32)}, nil
}

func appendDiskNumber(buf []byte, value uint64) []byte { return binary.AppendUvarint(buf, value) }

func appendDiskBytes(buf, data []byte) []byte {
	return append(appendDiskNumber(buf, uint64(len(data))), data...)
}

type diskDecoder struct {
	data []byte
	bad  bool
}

func (d *diskDecoder) number() uint64 {
	value, n := binary.Uvarint(d.data)
	if n <= 0 {
		d.bad = true
		return 0
	}
	d.data = d.data[n:]
	return value
}

func (d *diskDecoder) bytes(limit int) []byte {
	n := d.number()
	if n > uint64(limit) || n > uint64(len(d.data)) {
		d.bad = true
		return nil
	}
	value := d.data[:int(n)]
	d.data = d.data[int(n):]
	return value
}

func (d *diskDecoder) success() bool { return !d.bad && len(d.data) == 0 }

func encodeDiskDoc(doc diskDocValue) []byte {
	buf := appendDiskBytes(nil, []byte(doc.key))
	buf = appendDiskBytes(buf, doc.version)
	if doc.expiration.IsZero() {
		buf = appendDiskBytes(buf, nil)
	} else {
		expiry, err := (tarp.OrderedTimeCodec{}).Encode(doc.expiration)
		if err != nil {
			panic(err)
		}
		buf = appendDiskBytes(buf, expiry)
	}
	for class, lengths := range doc.lengths {
		for field, size := range lengths {
			buf = appendDiskNumber(buf, uint64(size))
			buf = appendDiskNumber(buf, uint64(doc.terms[class][field]))
		}
	}
	return buf
}

func decodeDiskDoc(value []byte) (diskDocValue, error) {
	d := diskDecoder{data: value}
	doc := diskDocValue{key: string(d.bytes(diskMaxKeyBytes)), version: d.bytes(diskMaxKeyBytes)}
	if expiry := d.bytes(12); len(expiry) != 0 {
		var err error
		doc.expiration, err = (tarp.OrderedTimeCodec{}).Decode(expiry)
		if err != nil || doc.expiration.IsZero() {
			return diskDocValue{}, diskInvalid("invalid document expiry")
		}
	}
	for class := range doc.lengths {
		for field := range doc.lengths[class] {
			n, terms := d.number(), d.number()
			if n > math.MaxInt || terms > n || (terms == 0) != (n == 0) {
				return diskDocValue{}, diskInvalid("invalid document length or term count")
			}
			doc.lengths[class][field], doc.terms[class][field] = int(n), int(terms)
		}
	}
	if !d.success() || doc.key == "" {
		return diskDocValue{}, diskInvalid("malformed document row")
	}
	return doc, nil
}

func encodeDiskTerm(term diskTermValue) []byte {
	buf := appendDiskNumber(nil, term.documents)
	buf = appendDiskNumber(buf, term.blocks)
	for _, df := range term.df {
		buf = appendDiskNumber(buf, uint64(df))
	}
	return buf
}

func decodeDiskTerm(value []byte) (diskTermValue, error) {
	d := diskDecoder{data: value}
	t := diskTermValue{documents: d.number(), blocks: d.number()}
	var populated bool
	for field := range t.df {
		n := d.number()
		if n > t.documents || n > math.MaxInt {
			return diskTermValue{}, diskInvalid("invalid document frequency")
		}
		t.df[field] = int(n)
		populated = populated || n != 0
	}
	if !d.success() || !populated || t.blocks == 0 || t.documents < t.blocks ||
		(t.documents-1)/diskPostingBlockDocs >= t.blocks {
		return diskTermValue{}, diskInvalid("malformed term row")
	}
	return t, nil
}

func encodeDiskPosting(buf []byte, p diskPosting) []byte {
	for field, frequency := range p.frequency {
		buf = appendDiskNumber(buf, uint64(frequency))
		buf = appendDiskNumber(buf, p.positions[field])
		buf = appendDiskBytes(buf, p.inline[field])
	}
	return buf
}

func decodeDiskPosting(d *diskDecoder) diskPosting {
	var p diskPosting
	var present bool
	for field := range p.frequency {
		frequency, positions := d.number(), d.number()
		inline := d.bytes(diskInlinePositions)
		if frequency > math.MaxUint16 || positions > math.MaxInt ||
			(positions != 0 && uint64(clampTF(int(positions))) != frequency) ||
			(len(inline) != 0 && (positions == 0 || positions > uint64(len(inline)))) {
			d.bad = true
		}
		p.frequency[field] = uint16(frequency)
		p.positions[field] = positions
		p.inline[field] = inline
		present = present || frequency != 0
	}
	d.bad = d.bad || !present
	return p
}

func encodeDiskBlock(count int, payload []byte) []byte {
	return append(appendDiskNumber(nil, uint64(count)), payload...)
}

func diskNumberKey(key, prefix []byte) (uint64, error) {
	if len(key) != len(prefix)+8 || !bytes.HasPrefix(key, prefix) {
		return 0, diskInvalid("invalid document key suffix")
	}
	result := binary.BigEndian.Uint64(key[len(prefix):])
	if result == 0 {
		return 0, diskInvalid("zero document ID")
	}
	return result, nil
}
