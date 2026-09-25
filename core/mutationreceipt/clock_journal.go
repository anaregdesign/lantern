package mutationreceipt

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
)

const (
	clockJournalMagic      = "LNRCLK01\n"
	clockJournalHeaderSize = len(clockJournalMagic) + 16 + 32 + 32 + 4
	clockJournalRecordSize = 8 + 8 + 4
)

var (
	ErrClockJournalCorrupt  = errors.New("mutationreceipt: clock journal is corrupt")
	ErrClockJournalTornTail = errors.New("mutationreceipt: clock journal has a torn tail")
	ErrClockJournalBinding  = errors.New("mutationreceipt: clock journal binding differs")
	ErrClockJournalRollback = errors.New("mutationreceipt: clock high-water is below durable journal")
	ErrClockJournalUnusable = errors.New("mutationreceipt: clock journal write is indeterminate")
	ErrClockJournalClosed   = errors.New("mutationreceipt: clock journal is closed")
	clockJournalCRC         = crc32.MakeTable(crc32.Castagnoli)
)

type clockJournalWriter interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// ClockJournal synchronously records monotonic Store high-water. The caller
// must hold the FileWAL path lease before creating or resuming it and keep
// that lease through every Advance and Close. Use the lease's canonical path
// as walPath; its .clock sidecar is never silently recreated for an existing
// WAL. The header binds path, epoch, and policy, but does not attest WAL bytes
// or the archive cut. A serving installer must validate those separately.
type ClockJournal struct {
	mu        sync.Mutex
	file      clockJournalWriter
	seq       uint64
	highWater int64
	unusable  bool
	closed    bool
}

// CreateClockJournal creates a new sidecar and syncs its binding header and
// directory. An incomplete file left by failure must be inspected rather
// than overwritten. It cannot certify an already-existing WAL by itself.
func CreateClockJournal(walPath string, epoch Epoch, policyFingerprint [32]byte) (*ClockJournal, error) {
	header, path, err := clockJournalHeader(walPath, epoch, policyFingerprint)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
		}
	}()
	if n, err := f.Write(header[:]); err != nil {
		return nil, fmt.Errorf("mutationreceipt: write clock journal header: %w", err)
	} else if n != len(header) {
		return nil, fmt.Errorf("mutationreceipt: write clock journal header: %w", io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("mutationreceipt: sync clock journal header: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("mutationreceipt: open clock journal directory: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return nil, fmt.Errorf("mutationreceipt: sync clock journal directory: %w", err)
	}
	keep = true
	return &ClockJournal{file: f}, nil
}

// ResumeClockJournal validates the complete sidecar before returning an
// append writer. Missing, torn, corrupt, or mismatched metadata fails closed.
func ResumeClockJournal(walPath string, epoch Epoch, policyFingerprint [32]byte) (*ClockJournal, error) {
	expected, path, err := clockJournalHeader(walPath, epoch, policyFingerprint)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: sidecar is not a regular file", ErrClockJournalCorrupt)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
		}
	}()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%w: sidecar changed during open", ErrClockJournalCorrupt)
	}
	var actual [clockJournalHeaderSize]byte
	if _, err := io.ReadFull(f, actual[:]); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrClockJournalCorrupt, err)
	}
	if clockJournalChecksum(actual[:len(actual)-4]) != binary.BigEndian.Uint32(actual[len(actual)-4:]) ||
		string(actual[:len(clockJournalMagic)]) != clockJournalMagic {
		return nil, fmt.Errorf("%w: header checksum or version", ErrClockJournalCorrupt)
	}
	if actual != expected {
		return nil, ErrClockJournalBinding
	}
	j := &ClockJournal{file: f}
	offset := int64(len(actual))
	for {
		var record [clockJournalRecordSize]byte
		n, err := io.ReadFull(f, record[:])
		if errors.Is(err, io.EOF) && n == 0 {
			break
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrClockJournalTornTail
		}
		if err != nil {
			return nil, fmt.Errorf("mutationreceipt: read clock journal: %w", err)
		}
		seq := binary.BigEndian.Uint64(record[:8])
		ms := int64(binary.BigEndian.Uint64(record[8:16]))
		if clockJournalChecksum(record[:16]) != binary.BigEndian.Uint32(record[16:]) ||
			j.seq == math.MaxUint64 || seq != j.seq+1 || ms <= j.highWater {
			return nil, ErrClockJournalCorrupt
		}
		j.seq = seq
		j.highWater = ms
		offset += int64(n)
	}
	final, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if opened.Size() != offset || final.Size() != offset {
		return nil, fmt.Errorf("%w: sidecar size changed during replay", ErrClockJournalCorrupt)
	}
	keep = true
	return j, nil
}

func clockJournalHeader(walPath string, epoch Epoch, policyFingerprint [32]byte) ([clockJournalHeaderSize]byte, string, error) {
	var header [clockJournalHeaderSize]byte
	if walPath == "" || epoch == (Epoch{}) || policyFingerprint == ([32]byte{}) {
		return header, "", ErrInvalidConfig
	}
	abs, err := filepath.Abs(walPath)
	if err != nil {
		return header, "", err
	}
	path := filepath.Clean(abs)
	copy(header[:], clockJournalMagic)
	offset := len(clockJournalMagic)
	copy(header[offset:], epoch[:])
	offset += len(epoch)
	copy(header[offset:], policyFingerprint[:])
	offset += len(policyFingerprint)
	digest := sha256.Sum256([]byte(path))
	copy(header[offset:], digest[:])
	binary.BigEndian.PutUint32(header[len(header)-4:], clockJournalChecksum(header[:len(header)-4]))
	return header, path + ".clock", nil
}

func clockJournalChecksum(data []byte) uint32 { return crc32.Checksum(data, clockJournalCRC) }

// Advance implements ClockHighWaterSink. A successful return means the new
// high-water frame has been synced. Any write or Sync failure permanently
// poisons this writer; even a short write may have reached durable storage.
func (j *ClockJournal) Advance(ms int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClockJournalClosed
	}
	if j.unusable {
		return ErrClockJournalUnusable
	}
	if ms < 0 {
		return ErrInvalidClock
	}
	if ms < j.highWater {
		return ErrClockJournalRollback
	}
	if ms == j.highWater {
		return nil
	}
	if j.seq == math.MaxUint64 {
		return ErrClockJournalCorrupt
	}
	var record [clockJournalRecordSize]byte
	binary.BigEndian.PutUint64(record[:8], j.seq+1)
	binary.BigEndian.PutUint64(record[8:16], uint64(ms))
	binary.BigEndian.PutUint32(record[16:], clockJournalChecksum(record[:16]))
	j.unusable = true
	if n, err := j.file.Write(record[:]); err != nil {
		return errors.Join(ErrClockJournalUnusable, fmt.Errorf("mutationreceipt: write clock journal: %w", err))
	} else if n != len(record) {
		return errors.Join(ErrClockJournalUnusable, io.ErrShortWrite)
	}
	if err := j.file.Sync(); err != nil {
		return errors.Join(ErrClockJournalUnusable, fmt.Errorf("mutationreceipt: sync clock journal: %w", err))
	}
	j.seq++
	j.highWater = ms
	j.unusable = false
	return nil
}

// HighWaterMillis is diagnostic until the caller has validated the matching
// WAL/archive cut. It does not certify status or admission by itself.
func (j *ClockJournal) HighWaterMillis() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.highWater
}

// Close releases the journal writer. The owning composition root must close
// it before releasing the FileWAL path lease.
func (j *ClockJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.file.Close()
}
