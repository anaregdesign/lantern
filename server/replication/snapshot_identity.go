package replication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"

	"github.com/anaregdesign/lantern/core/graphcache"
)

const (
	snapshotIdentityPayloadBytes = 88
	snapshotIdentityRecordBytes  = snapshotIdentityPayloadBytes + sha256.Size
	snapshotIdentityChunkRecords = (1 << 20) / snapshotIdentityRecordBytes
	snapshotIdentityKeyBuffer    = 4096
	snapshotIdentityLevels       = 64
	snapshotIdentityEdge         = 0
	snapshotIdentityContribution = 1
)

// Keys live only in the scratch arena. A descriptor has constant size even
// when a legal frame contains a very long key. Hashes accelerate ordering;
// exact, length-delimited arena bytes decide identity after a hash match.
type snapshotIdentityRecord struct {
	digest           [sha256.Size]byte
	offset           uint64
	tailLen, headLen uint64
	id               graphcache.ContribID
	kind             byte
	tombstone        bool
}

type snapshotIdentityRun struct {
	file  *snapshotScratchFile
	count uint64
}

// snapshotIdentityIndex is owned by one staged install, with one fixed sort
// chunk and at most 64 binary merge runs. It retains no key strings or run
// list. The enclosing scratch owner also closes files if collection aborts
// before Finish. Only auxiliary identities are sorted; Snapshot replay keeps
// its original frame order.
type snapshotIdentityIndex struct {
	ctx        context.Context
	scratch    *snapshotScratch
	arena      *snapshotScratchFile
	hasher     hash.Hash
	arenaHash  hash.Hash
	arenaBytes uint64
	records    [snapshotIdentityChunkRecords]snapshotIdentityRecord
	n          int
	chunkLimit int
	levels     [snapshotIdentityLevels]*snapshotIdentityRun
	keyA, keyB [snapshotIdentityKeyBuffer]byte
	rawRead    [snapshotIdentityRecordBytes]byte
	rawWrite   [snapshotIdentityRecordBytes]byte
	prefix     [17]byte
	err        error
	finished   bool
}

func newSnapshotIdentityIndex(ctx context.Context, scratch *snapshotScratch) (*snapshotIdentityIndex, error) {
	if ctx == nil || scratch == nil {
		return nil, snapshotProtocolError("identity index requires an owned context and scratch")
	}
	// Construction precedes the transport's first Receive. No identity bytes
	// are retained until Add checks the captured original response admission.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	arena, err := scratch.create()
	if err != nil {
		return nil, err
	}
	return &snapshotIdentityIndex{
		ctx: ctx, scratch: scratch, arena: arena, hasher: sha256.New(), arenaHash: sha256.New(),
		chunkLimit: snapshotIdentityChunkRecords,
	}, nil
}

func (i *snapshotIdentityIndex) AddEdge(tail, head string) error {
	return i.add(tail, head, graphcache.ContribID{}, snapshotIdentityEdge, false)
}

func (i *snapshotIdentityIndex) AddContribution(tail, head string, id graphcache.ContribID, tombstone bool) error {
	return i.add(tail, head, id, snapshotIdentityContribution, tombstone)
}

func (i *snapshotIdentityIndex) fail(err error) error {
	if i.err == nil {
		i.err = err
	}
	return i.err
}

func (i *snapshotIdentityIndex) add(tail, head string, id graphcache.ContribID, kind byte, tombstone bool) error {
	if i == nil {
		return snapshotProtocolError("identity index is nil")
	}
	if i.err != nil {
		return i.err
	}
	if i.finished {
		return i.fail(snapshotProtocolError("identity index is finished"))
	}
	if err := checkSnapshotContext(i.ctx); err != nil {
		return i.fail(err)
	}
	if tail == "" || head == "" || i.chunkLimit <= 0 || i.chunkLimit > len(i.records) {
		return i.fail(snapshotProtocolError("invalid identity or sort chunk"))
	}
	record := &i.records[i.n]
	*record = snapshotIdentityRecord{
		offset: i.arena.Size(), tailLen: uint64(len(tail)), headLen: uint64(len(head)),
		id: id, kind: kind, tombstone: tombstone,
	}
	i.hasher.Reset()
	i.prefix[0] = kind
	binary.BigEndian.PutUint64(i.prefix[1:9], record.tailLen)
	binary.BigEndian.PutUint64(i.prefix[9:17], record.headLen)
	_, _ = i.hasher.Write(i.prefix[:])
	if err := i.appendKey(tail); err != nil {
		return i.fail(err)
	}
	if err := i.appendKey(head); err != nil {
		return i.fail(err)
	}
	_, _ = i.hasher.Write(id[:])
	i.hasher.Sum(record.digest[:0])
	i.n++
	if i.n == i.chunkLimit {
		return i.fail(i.flush())
	}
	return nil
}

func (i *snapshotIdentityIndex) appendKey(key string) error {
	for len(key) != 0 {
		if err := checkSnapshotContext(i.ctx); err != nil {
			return err
		}
		n := copy(i.keyA[:], key)
		_, _ = i.hasher.Write(i.keyA[:n])
		if err := i.write(i.arena, i.keyA[:n]); err != nil {
			return err
		}
		_, _ = i.arenaHash.Write(i.keyA[:n])
		i.arenaBytes += uint64(n)
		key = key[n:]
	}
	return nil
}

func (i *snapshotIdentityIndex) write(file *snapshotScratchFile, data []byte) error {
	for len(data) != 0 {
		if err := checkSnapshotContext(i.ctx); err != nil {
			return err
		}
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (i *snapshotIdentityIndex) compareKeys(a, b snapshotIdentityRecord) (int, error) {
	for _, part := range [2][4]uint64{
		{a.offset, a.tailLen, b.offset, b.tailLen},
		{a.offset + a.tailLen, a.headLen, b.offset + b.tailLen, b.headLen},
	} {
		common := min(part[1], part[3])
		for pos := uint64(0); pos < common; {
			n := int(min(common-pos, uint64(len(i.keyA))))
			if err := i.readAt(i.keyA[:n], part[0]+pos); err != nil {
				return 0, err
			}
			if err := i.readAt(i.keyB[:n], part[2]+pos); err != nil {
				return 0, err
			}
			if order := bytes.Compare(i.keyA[:n], i.keyB[:n]); order != 0 {
				return order, nil
			}
			pos += uint64(n)
		}
		if part[1] < part[3] {
			return -1, nil
		}
		if part[1] > part[3] {
			return 1, nil
		}
	}
	return 0, nil
}

func (i *snapshotIdentityIndex) readAt(buffer []byte, offset uint64) error {
	if err := checkSnapshotContext(i.ctx); err != nil {
		return err
	}
	if offset > math.MaxInt64 || offset > i.arena.Size() || uint64(len(buffer)) > i.arena.Size()-offset {
		return snapshotProtocolError("identity arena offset is invalid")
	}
	n, err := i.arena.ReadAt(buffer, int64(offset))
	if n != len(buffer) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("read Snapshot identity key: %w", err)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// The arena is auxiliary to the sealed replay spool, but corruption here must
// not turn a duplicate identity into two apparently distinct keys. Its digest
// is independent of the injectable ordering accelerator used by descriptors.
func (i *snapshotIdentityIndex) verifyArena() error {
	if err := checkSnapshotContext(i.ctx); err != nil {
		return err
	}
	if i.arena.Size() != i.arenaBytes {
		return snapshotProtocolError("identity arena length changed")
	}
	actual := sha256.New()
	for offset := uint64(0); offset < i.arenaBytes; {
		n := int(min(i.arenaBytes-offset, uint64(len(i.keyA))))
		if err := i.readAt(i.keyA[:n], offset); err != nil {
			return err
		}
		_, _ = actual.Write(i.keyA[:n])
		offset += uint64(n)
	}
	var want, got [sha256.Size]byte
	i.arenaHash.Sum(want[:0])
	actual.Sum(got[:0])
	if want != got {
		return snapshotProtocolError("identity arena digest mismatch")
	}
	return checkSnapshotContext(i.ctx)
}

func (i *snapshotIdentityIndex) compare(a, b snapshotIdentityRecord) (int, error) {
	if err := checkSnapshotContext(i.ctx); err != nil {
		return 0, err
	}
	if a.kind < b.kind {
		return -1, nil
	}
	if a.kind > b.kind {
		return 1, nil
	}
	if order := bytes.Compare(a.digest[:], b.digest[:]); order != 0 {
		return order, nil
	}
	if order, err := i.compareKeys(a, b); order != 0 || err != nil {
		return order, err
	}
	return bytes.Compare(a.id[:], b.id[:]), nil
}

// Error-returning heap sort needs neither a second chunk nor a comparator
// that conceals scratch I/O or original-admission failures from sort.Slice.
func (i *snapshotIdentityIndex) sortChunk() error {
	sift := func(start, end int) error {
		for root := start; root < end/2; {
			child := 2*root + 1
			if child+1 < end {
				order, err := i.compare(i.records[child], i.records[child+1])
				if err != nil {
					return err
				}
				if order < 0 {
					child++
				}
			}
			order, err := i.compare(i.records[root], i.records[child])
			if err != nil {
				return err
			}
			if order >= 0 {
				return nil
			}
			i.records[root], i.records[child] = i.records[child], i.records[root]
			root = child
		}
		return nil
	}
	for start := i.n / 2; start > 0; start-- {
		if err := sift(start-1, i.n); err != nil {
			return err
		}
	}
	for end := i.n - 1; end > 0; end-- {
		i.records[0], i.records[end] = i.records[end], i.records[0]
		if err := sift(0, end); err != nil {
			return err
		}
	}
	return nil
}

func (i *snapshotIdentityIndex) writeRecord(file *snapshotScratchFile, r snapshotIdentityRecord) error {
	raw := i.rawWrite[:]
	clear(raw)
	raw[0] = r.kind
	if r.tombstone {
		raw[1] = 1
	}
	binary.BigEndian.PutUint64(raw[8:16], r.offset)
	binary.BigEndian.PutUint64(raw[16:24], r.tailLen)
	binary.BigEndian.PutUint64(raw[24:32], r.headLen)
	copy(raw[32:56], r.id[:])
	copy(raw[56:88], r.digest[:])
	checksum := sha256.Sum256(raw[:snapshotIdentityPayloadBytes])
	copy(raw[snapshotIdentityPayloadBytes:], checksum[:])
	return i.write(file, raw)
}

func (i *snapshotIdentityIndex) readRecord(file *snapshotScratchFile) (snapshotIdentityRecord, error) {
	var r snapshotIdentityRecord
	if err := checkSnapshotContext(i.ctx); err != nil {
		return r, err
	}
	raw := i.rawRead[:]
	if _, err := io.ReadFull(file, raw); err != nil {
		return r, err
	}
	checksum := sha256.Sum256(raw[:snapshotIdentityPayloadBytes])
	if !bytes.Equal(checksum[:], raw[snapshotIdentityPayloadBytes:]) {
		return r, snapshotProtocolError("identity descriptor checksum mismatch")
	}
	r.kind, r.tombstone = raw[0], raw[1] == 1
	r.offset = binary.BigEndian.Uint64(raw[8:16])
	r.tailLen = binary.BigEndian.Uint64(raw[16:24])
	r.headLen = binary.BigEndian.Uint64(raw[24:32])
	copy(r.id[:], raw[32:56])
	copy(r.digest[:], raw[56:88])
	if r.kind > snapshotIdentityContribution || raw[1] > 1 ||
		(r.kind == snapshotIdentityEdge && (r.tombstone || r.id != graphcache.ContribID{})) ||
		r.tailLen == 0 || r.headLen == 0 || r.offset > i.arena.Size() ||
		r.tailLen > i.arena.Size()-r.offset || r.headLen > i.arena.Size()-r.offset-r.tailLen {
		return r, snapshotProtocolError("invalid identity descriptor")
	}
	return r, nil
}

func (i *snapshotIdentityIndex) reader(run *snapshotIdentityRun) error {
	if err := checkSnapshotContext(i.ctx); err != nil {
		return err
	}
	if run.count > math.MaxInt64/snapshotIdentityRecordBytes || run.file.Size() != run.count*snapshotIdentityRecordBytes {
		return snapshotProtocolError("invalid identity run length")
	}
	_, err := run.file.Seek(0, io.SeekStart)
	return err
}

func (i *snapshotIdentityIndex) merge(a, b *snapshotIdentityRun) (out *snapshotIdentityRun, err error) {
	limit := uint64(math.MaxInt64 / snapshotIdentityRecordBytes)
	if a.count > limit || b.count > limit || a.count > limit-b.count {
		return nil, snapshotProtocolError("identity run count overflow")
	}
	file, err := i.scratch.create()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close())
		}
	}()
	if err = i.reader(a); err != nil {
		return nil, err
	}
	if err = i.reader(b); err != nil {
		return nil, err
	}
	var left, right snapshotIdentityRecord
	var haveLeft, haveRight bool
	leftCount, rightCount := a.count, b.count
	for leftCount != 0 || rightCount != 0 || haveLeft || haveRight {
		if !haveLeft && leftCount != 0 {
			left, err = i.readRecord(a.file)
			if err != nil {
				return nil, err
			}
			leftCount--
			haveLeft = true
		}
		if !haveRight && rightCount != 0 {
			right, err = i.readRecord(b.file)
			if err != nil {
				return nil, err
			}
			rightCount--
			haveRight = true
		}
		takeLeft := !haveRight
		if haveLeft && haveRight {
			order, compareErr := i.compare(left, right)
			if compareErr != nil {
				return nil, compareErr
			}
			takeLeft = order <= 0
		}
		if takeLeft {
			err, haveLeft = i.writeRecord(file, left), false
		} else {
			err, haveRight = i.writeRecord(file, right), false
		}
		if err != nil {
			return nil, err
		}
	}
	if err = errors.Join(a.file.Close(), b.file.Close()); err != nil {
		return nil, err
	}
	return &snapshotIdentityRun{file: file, count: a.count + b.count}, nil
}

func (i *snapshotIdentityIndex) flush() error {
	if i.n == 0 {
		return nil
	}
	if err := i.sortChunk(); err != nil {
		return err
	}
	file, err := i.scratch.create()
	if err != nil {
		return err
	}
	run := &snapshotIdentityRun{file: file, count: uint64(i.n)}
	for n := range i.n {
		if err := i.writeRecord(file, i.records[n]); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	i.n = 0
	for level := range i.levels {
		if i.levels[level] == nil {
			i.levels[level] = run
			return nil
		}
		merged, err := i.merge(i.levels[level], run)
		if err != nil {
			return errors.Join(err, run.file.Close())
		}
		i.levels[level], run = nil, merged
	}
	return errors.Join(snapshotProtocolError("identity merge levels exhausted"), run.file.Close())
}

func (i *snapshotIdentityIndex) closeFiles() error {
	var err error
	for level, run := range i.levels {
		if run != nil {
			err = errors.Join(err, run.file.Close())
			i.levels[level] = nil
		}
	}
	if i.arena != nil {
		err = errors.Join(err, i.arena.Close())
		i.arena = nil
	}
	return err
}

func (i *snapshotIdentityIndex) Finish() (err error) {
	if i == nil {
		return snapshotProtocolError("identity index is nil")
	}
	if i.finished {
		return i.err
	}
	i.finished = true
	defer func() {
		err = errors.Join(err, i.closeFiles())
		i.err = err
	}()
	if i.err != nil {
		return i.err
	}
	if err := checkSnapshotContext(i.ctx); err != nil {
		return err
	}
	if err := i.flush(); err != nil {
		return err
	}
	var merged *snapshotIdentityRun
	for level, run := range i.levels {
		if run == nil {
			continue
		}
		if merged == nil {
			merged = run
			i.levels[level] = nil
			continue
		}
		next, mergeErr := i.merge(merged, run)
		if mergeErr != nil {
			return errors.Join(mergeErr, merged.file.Close())
		}
		i.levels[level], merged = nil, next
	}
	if err := i.verifyArena(); err != nil {
		if merged != nil {
			return errors.Join(err, merged.file.Close())
		}
		return err
	}
	if merged == nil {
		return nil
	}
	defer func() { err = errors.Join(err, merged.file.Close()) }()
	if err := i.reader(merged); err != nil {
		return err
	}
	var prior snapshotIdentityRecord
	for position := uint64(0); position < merged.count; position++ {
		record, err := i.readRecord(merged.file)
		if err != nil {
			return err
		}
		if position != 0 {
			order, err := i.compare(prior, record)
			if err != nil {
				return err
			}
			if order > 0 {
				return snapshotProtocolError("identity run is not sorted")
			}
			if order == 0 {
				if record.kind == snapshotIdentityEdge {
					return snapshotProtocolError("duplicate live edge frame")
				}
				if record.tombstone != prior.tombstone {
					return snapshotProtocolError("live edge Add and contribution tombstone overlap")
				}
				if record.tombstone {
					return snapshotProtocolError("duplicate edge contribution tombstone")
				}
				return snapshotProtocolError("duplicate live edge contribution")
			}
		}
		prior = record
	}
	return checkSnapshotContext(i.ctx)
}
