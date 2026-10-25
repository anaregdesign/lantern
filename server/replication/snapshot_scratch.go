package replication

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"connectrpc.com/connect"
)

const (
	snapshotScratchSlots                = 128
	snapshotScratchBlockBytes    uint64 = 4096
	snapshotScratchMetadataBytes uint64 = 4096
	snapshotScratchOwnerName            = ".lantern-snapshot-owner"
)

var errSnapshotScratchClosed = errors.New("Snapshot scratch is closed")

// GraphSnapshotSpoolConfig selects explicit private scratch storage. The byte
// allowance charges 4096-byte-rounded logical extents and 4096 bytes per file,
// including the stable owner while open. It is not physical filesystem usage.
// The directory must already exist; no temporary or receipt path is inferred.
type GraphSnapshotSpoolConfig struct {
	Directory     string
	MaxDiskBytes  uint64
	MaxFrameBytes int
}

// ValidateConfiguration checks the persisted byte/path contract without
// filesystem access. Explicit setup may validate it before creating storage.
func (c GraphSnapshotSpoolConfig) ValidateConfiguration() error {
	if c.Directory == "" || !filepath.IsAbs(c.Directory) ||
		c.MaxDiskBytes < snapshotScratchMetadataBytes || c.MaxDiskBytes > math.MaxInt64 ||
		c.MaxFrameBytes <= 0 || uint64(c.MaxFrameBytes) > c.MaxDiskBytes {
		return errors.New("Snapshot spool requires an absolute private directory and finite positive byte/frame limits")
	}
	return nil
}

// Validate additionally verifies that the private storage directory exists.
// Ordinary runtime startup never creates or repairs this directory.
func (c GraphSnapshotSpoolConfig) Validate() error {
	if err := c.ValidateConfiguration(); err != nil {
		return err
	}
	dir, err := openSnapshotScratchDirectory(c.Directory)
	if err != nil {
		return err
	}
	return dir.Close()
}

// snapshotScratch owns one install's lease, fixed file slots and aggregate
// allowance. All I/O and cleanup serialize under mu. The original request owns
// cancellation; scratch never acquires native authority or extends its deadline.
type snapshotScratch struct {
	mu         sync.Mutex
	ctx        context.Context
	config     GraphSnapshotSpoolConfig
	dir, lease *os.File
	files      [snapshotScratchSlots]*snapshotScratchFile
	usedBytes  uint64
	closed     bool
	failure    error
	writeAt    func(*os.File, []byte, int64) (int, error)
	readAt     func(*os.File, []byte, int64) (int, error)
	// The hook reports an error only after the actual descriptor was closed.
	afterClose func(*os.File) error
	// This hook likewise runs only after the actual lease and directory closes.
	afterOwnerClose func() error
}

func newSnapshotScratch(ctx context.Context, config GraphSnapshotSpoolConfig) (*snapshotScratch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	dir, err := openSnapshotScratchDirectory(config.Directory)
	if err != nil {
		return nil, err
	}
	lease, err := lockSnapshotScratchOwner(dir)
	if err != nil {
		return nil, errors.Join(err, dir.Close())
	}
	if err := recoverSnapshotScratchSlots(dir); err != nil {
		return nil, errors.Join(err, lease.Close(), dir.Close())
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, lease.Close(), dir.Close())
	}
	return &snapshotScratch{
		ctx: ctx, config: config, dir: dir, lease: lease,
		usedBytes: snapshotScratchMetadataBytes,
		writeAt:   func(f *os.File, p []byte, offset int64) (int, error) { return f.WriteAt(p, offset) },
		readAt:    func(f *os.File, p []byte, offset int64) (int, error) { return f.ReadAt(p, offset) },
	}, nil
}

func snapshotScratchSlotName(slot int) string {
	return fmt.Sprintf(".lantern-snapshot-slot-%03d", slot)
}

func snapshotScratchQuotaError() error {
	return connect.NewError(connect.CodeResourceExhausted, errors.New("Snapshot scratch disk allowance exhausted"))
}

func (s *snapshotScratch) checkLocked() error {
	if s.closed {
		if s.failure != nil {
			return s.failure
		}
		return errSnapshotScratchClosed
	}
	if err := s.ctx.Err(); err != nil {
		return s.failLocked(err)
	}
	return nil
}

func (s *snapshotScratch) create() (*snapshotScratchFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(); err != nil {
		return nil, err
	}
	slot := -1
	for i, f := range s.files {
		if f == nil {
			slot = i
			break
		}
	}
	if slot < 0 || snapshotScratchMetadataBytes > s.config.MaxDiskBytes-s.usedBytes {
		return nil, s.failLocked(snapshotScratchQuotaError())
	}
	// Reserve metadata before creating even an empty file.
	s.usedBytes += snapshotScratchMetadataBytes
	file, err := createSnapshotScratchSlot(s.dir, snapshotScratchSlotName(slot))
	if err != nil {
		s.usedBytes -= snapshotScratchMetadataBytes
		return nil, s.failLocked(err)
	}
	f := &snapshotScratchFile{owner: s, file: file, slot: slot, charge: snapshotScratchMetadataBytes}
	s.files[slot] = f
	if err := s.checkLocked(); err != nil {
		return nil, err
	}
	return f, nil
}

func (s *snapshotScratch) stats() (uint64, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, f := range s.files {
		if f != nil {
			count++
		}
	}
	return s.usedBytes, count
}

func (s *snapshotScratch) closeDescriptorLocked(file *os.File) error {
	err := file.Close()
	if s.afterClose != nil {
		err = errors.Join(err, s.afterClose(file))
	}
	return err
}

func (s *snapshotScratch) closeLocked() error {
	if s.closed {
		return s.failure
	}
	s.closed = true
	var err error
	for _, f := range s.files {
		if f != nil {
			err = errors.Join(err, f.closeLocked())
		}
	}
	if s.lease != nil {
		err = errors.Join(err, s.lease.Close())
		s.lease = nil
	}
	if s.dir != nil {
		err = errors.Join(err, s.dir.Close())
		s.dir = nil
	}
	if s.afterOwnerClose != nil {
		err = errors.Join(err, s.afterOwnerClose())
	}
	// No further operation can reuse this terminal owner's credits. File close
	// failures retained their charge until all descriptors and the lease closed.
	for i := range s.files {
		s.files[i] = nil
	}
	s.usedBytes = 0
	s.failure = errors.Join(s.failure, err)
	return s.failure
}

func (s *snapshotScratch) failLocked(err error) error {
	s.failure = errors.Join(s.failure, err)
	return s.closeLocked()
}

func (s *snapshotScratch) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

type snapshotScratchFile struct {
	owner        *snapshotScratch
	file         *os.File
	slot         int
	size, charge uint64
	offset       int64
}

func (f *snapshotScratchFile) Size() uint64 {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	return f.size
}

func (f *snapshotScratchFile) checkLocked() error {
	if err := f.owner.checkLocked(); err != nil {
		return err
	}
	if f.file == nil {
		return errSnapshotScratchClosed
	}
	return nil
}

func (f *snapshotScratchFile) writeLocked(p []byte, offset int64) (int, error) {
	s := f.owner
	if err := f.checkLocked(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if len(p) == 0 {
		return 0, nil
	}
	if uint64(len(p)) > uint64(math.MaxInt64)-uint64(offset) {
		return 0, s.failLocked(snapshotScratchQuotaError())
	}
	end := uint64(offset) + uint64(len(p))
	if end > math.MaxInt64-(snapshotScratchBlockBytes-1) {
		return 0, s.failLocked(snapshotScratchQuotaError())
	}
	charge := snapshotScratchMetadataBytes + ((max(end, f.size)+snapshotScratchBlockBytes-1)/snapshotScratchBlockBytes)*snapshotScratchBlockBytes
	if charge > f.charge {
		delta := charge - f.charge
		if delta > s.config.MaxDiskBytes-s.usedBytes {
			return 0, s.failLocked(snapshotScratchQuotaError())
		}
		s.usedBytes += delta
		f.charge = charge
	}
	n, err := s.writeAt(f.file, p, offset)
	if n < 0 || n > len(p) {
		return 0, s.failLocked(io.ErrShortWrite)
	}
	f.size = max(f.size, uint64(offset)+uint64(n))
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return n, s.failLocked(err)
	}
	if err := s.checkLocked(); err != nil {
		return n, err
	}
	return n, nil
}

func (f *snapshotScratchFile) Write(p []byte) (int, error) {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	n, err := f.writeLocked(p, f.offset)
	if f.file != nil {
		f.offset += int64(n)
	}
	return n, err
}

func (f *snapshotScratchFile) WriteAt(p []byte, offset int64) (int, error) {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	return f.writeLocked(p, offset)
}

func (f *snapshotScratchFile) readLocked(p []byte, offset int64) (int, error) {
	s := f.owner
	if err := f.checkLocked(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	n, err := s.readAt(f.file, p, offset)
	if n < 0 || n > len(p) {
		return 0, s.failLocked(io.ErrUnexpectedEOF)
	}
	if err == nil && n != len(p) {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, s.failLocked(err)
	}
	if err := s.checkLocked(); err != nil {
		return n, err
	}
	return n, err
}

func (f *snapshotScratchFile) Read(p []byte) (int, error) {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	n, err := f.readLocked(p, f.offset)
	if f.file != nil {
		f.offset += int64(n)
	}
	return n, err
}

func (f *snapshotScratchFile) ReadAt(p []byte, offset int64) (int, error) {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	return f.readLocked(p, offset)
}

func (f *snapshotScratchFile) Seek(offset int64, whence int) (int64, error) {
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return 0, err
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = f.offset
	case io.SeekEnd:
		base = int64(f.size)
	default:
		return 0, os.ErrInvalid
	}
	if offset < -base || offset > math.MaxInt64-base {
		return 0, os.ErrInvalid
	}
	f.offset = base + offset
	return f.offset, nil
}

func (f *snapshotScratchFile) closeLocked() error {
	if f.file == nil {
		return nil
	}
	err := f.owner.closeDescriptorLocked(f.file)
	f.file = nil
	if err == nil {
		f.owner.usedBytes -= f.charge
		f.owner.files[f.slot] = nil
		f.size, f.charge, f.offset = 0, 0, 0
	}
	return err
}

func (f *snapshotScratchFile) Close() error {
	if f == nil {
		return nil
	}
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	if f.file == nil {
		return nil
	}
	if err := f.closeLocked(); err != nil {
		return f.owner.failLocked(err)
	}
	return nil
}
