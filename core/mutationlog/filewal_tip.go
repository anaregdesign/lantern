package mutationlog

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	fileWALTipMagic      = "LNWLTIP1\n"
	fileWALTipHeaderSize = len(fileWALTipMagic) + sha256.Size + sha256.Size + 4
	fileWALTipRecordSize = 8 + sha256.Size + 4
)

var (
	ErrFileWALTipCorrupt    = errors.New("mutationlog: FileWAL tip journal is corrupt")
	ErrFileWALTipTornTail   = errors.New("mutationlog: FileWAL tip journal has a torn tail")
	ErrFileWALTipBinding    = errors.New("mutationlog: FileWAL tip journal binding differs")
	ErrFileWALTipMismatch   = errors.New("mutationlog: FileWAL differs from durable tip")
	ErrFileWALTipUnverified = errors.New("mutationlog: FileWAL tip journal is unverified")
	ErrFileWALTipUnusable   = errors.New("mutationlog: FileWAL tip journal write is indeterminate")
	ErrFileWALTipClosed     = errors.New("mutationlog: FileWAL tip journal is closed")
	fileWALTipCRC           = crc32.MakeTable(crc32.Castagnoli)
)

type fileWALTipWriter interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// FileWALTipJournal is an independently synced lower bound on the WAL frames
// eligible for Log publication. Each record binds a local sequence to a rolling
// SHA-256 chain of the exact frame bytes. The caller must hold the same
// exclusive FileWAL path lease from creation or resume through Close. Its
// binding must include the active receipt epoch and policy; the journal does
// not decide whether a decoded graph/receipt image is safe to serve.
type FileWALTipJournal struct {
	mu       sync.Mutex
	file     fileWALTipWriter
	walPath  string
	seq      uint64
	chain    [sha256.Size]byte
	verified bool
	bound    bool
	unusable bool
	closed   bool
}

// CreateFileWALTipJournal creates a new sidecar. binding is a nonzero,
// caller-derived digest of the active epoch and receipt policy. This function
// does not certify an existing WAL; VerifyAndCatchUp must succeed before the
// writer may be attached to a FileWAL.
func CreateFileWALTipJournal(walPath string, binding [sha256.Size]byte) (*FileWALTipJournal, error) {
	header, path, canonical, err := fileWALTipHeader(walPath, binding)
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
		return nil, fmt.Errorf("mutationlog: write FileWAL tip header: %w", err)
	} else if n != len(header) {
		return nil, fmt.Errorf("mutationlog: write FileWAL tip header: %w", io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("mutationlog: sync FileWAL tip header: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("mutationlog: open FileWAL tip directory: %w", err)
	}
	if err := errors.Join(dir.Sync(), dir.Close()); err != nil {
		return nil, fmt.Errorf("mutationlog: sync FileWAL tip directory: %w", err)
	}
	keep = true
	return &FileWALTipJournal{file: f, walPath: canonical, chain: fileWALChainSeed()}, nil
}

// ResumeFileWALTipJournal rejects a missing, torn, corrupt, or mismatched
// sidecar. A successful read is still unverified until the complete WAL has
// been checked against its last recorded prefix through VerifyAndCatchUp.
func ResumeFileWALTipJournal(walPath string, binding [sha256.Size]byte) (*FileWALTipJournal, error) {
	expected, path, canonical, err := fileWALTipHeader(walPath, binding)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: sidecar is not a regular file", ErrFileWALTipCorrupt)
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
		return nil, fmt.Errorf("%w: sidecar changed during open", ErrFileWALTipCorrupt)
	}
	var actual [fileWALTipHeaderSize]byte
	if _, err := io.ReadFull(f, actual[:]); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrFileWALTipCorrupt, err)
	}
	if fileWALTipChecksum(actual[:len(actual)-4]) != binary.BigEndian.Uint32(actual[len(actual)-4:]) ||
		string(actual[:len(fileWALTipMagic)]) != fileWALTipMagic {
		return nil, ErrFileWALTipCorrupt
	}
	if actual != expected {
		return nil, ErrFileWALTipBinding
	}
	j := &FileWALTipJournal{file: f, walPath: canonical, chain: fileWALChainSeed()}
	offset := int64(len(actual))
	for {
		var record [fileWALTipRecordSize]byte
		n, err := io.ReadFull(f, record[:])
		if errors.Is(err, io.EOF) && n == 0 {
			break
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrFileWALTipTornTail
		}
		if err != nil {
			return nil, fmt.Errorf("mutationlog: read FileWAL tip journal: %w", err)
		}
		seq := binary.BigEndian.Uint64(record[:8])
		if fileWALTipChecksum(record[:fileWALTipRecordSize-4]) != binary.BigEndian.Uint32(record[fileWALTipRecordSize-4:]) ||
			seq <= j.seq {
			return nil, ErrFileWALTipCorrupt
		}
		j.seq = seq
		copy(j.chain[:], record[8:8+sha256.Size])
		if j.chain == ([sha256.Size]byte{}) {
			return nil, ErrFileWALTipCorrupt
		}
		offset += int64(n)
	}
	final, err := f.Stat()
	if err != nil {
		return nil, err
	}
	finalPath, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, final) || !os.SameFile(opened, finalPath) || opened.Size() != offset || final.Size() != offset {
		return nil, fmt.Errorf("%w: sidecar changed during replay", ErrFileWALTipCorrupt)
	}
	keep = true
	return j, nil
}

func fileWALTipHeader(walPath string, binding [sha256.Size]byte) ([fileWALTipHeaderSize]byte, string, string, error) {
	var header [fileWALTipHeaderSize]byte
	if walPath == "" || binding == ([sha256.Size]byte{}) {
		return header, "", "", ErrFileWALTipBinding
	}
	abs, err := filepath.Abs(walPath)
	if err != nil {
		return header, "", "", err
	}
	canonical := filepath.Clean(abs)
	copy(header[:], fileWALTipMagic)
	copy(header[len(fileWALTipMagic):], binding[:])
	pathHash := sha256.Sum256([]byte(canonical))
	copy(header[len(fileWALTipMagic)+sha256.Size:], pathHash[:])
	binary.BigEndian.PutUint32(header[len(header)-4:], fileWALTipChecksum(header[:len(header)-4]))
	return header, canonical + ".tip", canonical, nil
}

func fileWALTipChecksum(data []byte) uint32 { return crc32.Checksum(data, fileWALTipCRC) }

// VerifyAndCatchUp validates the full WAL under the caller's path lease,
// including any suffix after the last recorded tip. A missing attested
// prefix or changed chain fails closed. Extra valid frames may be from a
// crash after WAL Sync but before publication; they are attested before any
// application state may be served. The caller must separately replay those
// frames into graph, Store, origins, Log, and clock before publication.
func (j *FileWALTipJournal) VerifyAndCatchUp(
	walPath string,
	decode func([]byte) (MutationOp, error),
	validate func(Entry) error,
) error {
	if j == nil {
		return ErrFileWALTipUnverified
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrFileWALTipClosed
	}
	if j.unusable {
		return ErrFileWALTipUnusable
	}
	if j.bound {
		return ErrFileWALTipUnusable
	}
	abs, err := filepath.Abs(walPath)
	if err != nil {
		return err
	}
	if filepath.Clean(abs) != j.walPath {
		return ErrFileWALTipBinding
	}
	cut, err := InspectFileWALCut(j.walPath, j.seq, decode, validate)
	if err != nil {
		return err
	}
	if cut.ChainSHA256 != j.chain {
		return ErrFileWALTipMismatch
	}
	if cut.ObservedLast > j.seq {
		if err := j.advanceLocked(cut.ObservedLast, cut.ObservedChainSHA256); err != nil {
			return err
		}
	}
	j.verified = true
	return nil
}

// Frontier returns the last attested sequence and frame chain. verified is
// false after Create/Resume until VerifyAndCatchUp has examined the WAL.
func (j *FileWALTipJournal) Frontier() (seq uint64, chain [sha256.Size]byte, verified bool) {
	if j == nil {
		return 0, [sha256.Size]byte{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.seq, j.chain, j.verified && !j.unusable && !j.closed
}

// advance records a synced WAL frame before FileWAL.Write reports success.
// An uncertain sidecar write permanently poisons this owner.
func (j *FileWALTipJournal) advance(seq uint64, chain [sha256.Size]byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrFileWALTipClosed
	}
	if j.unusable {
		return ErrFileWALTipUnusable
	}
	if !j.verified || !j.bound {
		return ErrFileWALTipUnverified
	}
	return j.advanceLocked(seq, chain)
}

func (j *FileWALTipJournal) bind(seq uint64, chain [sha256.Size]byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrFileWALTipClosed
	}
	if j.unusable || j.bound {
		return ErrFileWALTipUnusable
	}
	if !j.verified {
		return ErrFileWALTipUnverified
	}
	if seq != j.seq || chain != j.chain {
		return ErrFileWALTipMismatch
	}
	j.bound = true
	return nil
}

func (j *FileWALTipJournal) advanceLocked(seq uint64, chain [sha256.Size]byte) error {
	if j.closed {
		return ErrFileWALTipClosed
	}
	if j.unusable {
		return ErrFileWALTipUnusable
	}
	if seq == j.seq && chain == j.chain {
		return nil
	}
	if seq <= j.seq || chain == ([sha256.Size]byte{}) {
		return ErrFileWALTipMismatch
	}
	var record [fileWALTipRecordSize]byte
	binary.BigEndian.PutUint64(record[:8], seq)
	copy(record[8:], chain[:])
	binary.BigEndian.PutUint32(record[fileWALTipRecordSize-4:], fileWALTipChecksum(record[:fileWALTipRecordSize-4]))
	j.unusable = true
	if n, err := j.file.Write(record[:]); err != nil {
		return errors.Join(ErrFileWALTipUnusable, fmt.Errorf("mutationlog: write FileWAL tip: %w", err))
	} else if n != len(record) {
		return errors.Join(ErrFileWALTipUnusable, io.ErrShortWrite)
	}
	if err := j.file.Sync(); err != nil {
		return errors.Join(ErrFileWALTipUnusable, fmt.Errorf("mutationlog: sync FileWAL tip: %w", err))
	}
	j.seq = seq
	j.chain = chain
	j.unusable = false
	return nil
}

// Close releases the journal before its caller releases the WAL path lease.
func (j *FileWALTipJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	j.verified = false
	j.bound = false
	return j.file.Close()
}
