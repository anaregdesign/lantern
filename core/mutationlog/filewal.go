package mutationlog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/anaregdesign/lantern/core/hlc"
)

// FileWAL is an opt-in, synchronous WAL for one new, exclusively owned file.
// Its v1 format is an eight-byte magic followed by frames containing a
// big-endian body length, CRC32C over that length and body, local seq, HLC,
// and caller-encoded payload. Frames are capped at 32 MiB. There is no
// compaction or append-resume API.
// It provides durable bytes, not application recovery: a restored server must
// replay the complete mutation envelope into graph, receipts, origin state,
// and the in-memory Log before serving. Production does not wire FileWAL yet.
// The caller must ensure no other process writes the path.
type FileWAL struct {
	mu       sync.Mutex
	file     fileWALWriter
	encode   func(MutationOp) ([]byte, error)
	lastSeq  uint64
	unusable bool
	closed   bool
}

type fileWALWriter interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

const (
	fileWALMagic       = "LNWAL01\n"
	fileWALFrameHeader = 8  // uint32 body length, uint32 CRC32C(length || body)
	fileWALBodyHeader  = 36 // seq, HLC wall/logical/node ID
	maxFileWALBody     = 32 << 20
)

var fileWALCRC = crc32.MakeTable(crc32.Castagnoli)

var (
	ErrFileWALCorrupt  = errors.New("mutationlog: FileWAL is corrupt")
	ErrFileWALTornTail = errors.New("mutationlog: FileWAL has a torn tail")
	ErrFileWALSequence = errors.New("mutationlog: FileWAL sequence is not contiguous")
	ErrFileWALTooLarge = errors.New("mutationlog: FileWAL record exceeds size limit")
	ErrFileWALUnusable = errors.New("mutationlog: FileWAL write result is indeterminate")
	ErrFileWALClosed   = errors.New("mutationlog: FileWAL is closed")
)

// CreateFileWAL creates a brand-new file; it never resumes an existing one.
// A successful call syncs both the version header and its parent directory,
// so subsequent successful Write calls can claim a recoverable file name.
// On an error, an incomplete file may remain and must be inspected rather
// than silently reused. encode must produce owned, immutable payload bytes.
func CreateFileWAL(path string, encode func(MutationOp) ([]byte, error)) (*FileWAL, error) {
	if encode == nil {
		return nil, errors.New("mutationlog: FileWAL encoder is nil")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = f.Close()
		}
	}()
	if n, err := f.Write([]byte(fileWALMagic)); err != nil {
		return nil, fmt.Errorf("mutationlog: write FileWAL header: %w", err)
	} else if n != len(fileWALMagic) {
		return nil, fmt.Errorf("mutationlog: write FileWAL header: %w", io.ErrShortWrite)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("mutationlog: sync FileWAL header: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("mutationlog: open FileWAL directory: %w", err)
	}
	dirSyncErr := dir.Sync()
	dirCloseErr := dir.Close()
	if dirSyncErr != nil {
		return nil, fmt.Errorf("mutationlog: sync FileWAL directory: %w", dirSyncErr)
	}
	if dirCloseErr != nil {
		return nil, fmt.Errorf("mutationlog: close FileWAL directory: %w", dirCloseErr)
	}
	closeOnError = false
	return &FileWAL{file: f, encode: encode}, nil
}

// Write implements WAL. A nil return means the full frame and file have been
// synced. Encoder, size, and sequence errors precede all I/O and are definite
// aborts. Once a frame write begins, a short write, I/O error, or Sync error
// is indeterminate; this FileWAL rejects every later write. The enclosing Log
// must also fail closed until full application recovery establishes a new cut.
func (w *FileWAL) Write(entry Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrFileWALClosed
	}
	if w.unusable {
		return ErrFileWALUnusable
	}
	if w.lastSeq == math.MaxUint64 || entry.Seq != w.lastSeq+1 {
		return &DefiniteWALAbort{Cause: fmt.Errorf("%w: got %d after %d", ErrFileWALSequence, entry.Seq, w.lastSeq)}
	}
	payload, err := w.encode(entry.Op)
	if err != nil {
		return &DefiniteWALAbort{Cause: fmt.Errorf("mutationlog: encode FileWAL record: %w", err)}
	}
	if len(payload) > maxFileWALBody-fileWALBodyHeader {
		return &DefiniteWALAbort{Cause: ErrFileWALTooLarge}
	}
	frame := encodeFileWALFrame(entry, payload)
	// Keep the guard set even if a writer or Sync implementation panics after
	// a partial or complete durable record. A fresh recovered Log is required.
	w.unusable = true
	n, err := w.file.Write(frame)
	if err != nil {
		return fmt.Errorf("mutationlog: write FileWAL frame: %w", err)
	}
	if n != len(frame) {
		return fmt.Errorf("mutationlog: write FileWAL frame: %w", io.ErrShortWrite)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("mutationlog: sync FileWAL frame: %w", err)
	}
	w.lastSeq = entry.Seq
	w.unusable = false
	return nil
}

func encodeFileWALFrame(entry Entry, payload []byte) []byte {
	bodyLen := fileWALBodyHeader + len(payload)
	frame := make([]byte, fileWALFrameHeader+bodyLen)
	binary.BigEndian.PutUint32(frame[:4], uint32(bodyLen))
	body := frame[fileWALFrameHeader:]
	binary.BigEndian.PutUint64(body[:8], entry.Seq)
	binary.BigEndian.PutUint64(body[8:16], uint64(entry.HLC.WallNs))
	binary.BigEndian.PutUint32(body[16:20], entry.HLC.Logical)
	copy(body[20:36], entry.HLC.NodeID[:])
	copy(body[36:], payload)
	crc := crc32.Checksum(frame[:4], fileWALCRC)
	crc = crc32.Update(crc, fileWALCRC, body)
	binary.BigEndian.PutUint32(frame[4:8], crc)
	return frame
}

// Close releases the file. It does not grant permission to reopen and append:
// startup must first restore every application component from a verified cut.
func (w *FileWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}

// ReplayFileWAL validates a closed WAL file's framing, checksum, sequence,
// and decoding in full before invoking visit for any entry. It then scans a
// third time to stream decoded entries in order. The extra pass avoids
// exposing a valid prefix before a later record proves corrupt.
// It never truncates a torn tail or guesses whether an unacknowledged write
// committed. decode and visit must be non-nil; decode must be deterministic.
// The caller must own the file exclusively for both passes and must discard
// any partly restored application state if visit returns an error. Successful
// byte replay alone does not establish graph/receipt/epoch continuity.
func ReplayFileWAL(path string, decode func([]byte) (MutationOp, error), visit func(Entry) error) error {
	if decode == nil || visit == nil {
		return errors.New("mutationlog: FileWAL decoder and visitor are required")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := scanFileWAL(f, nil, nil); err != nil {
		return err
	}
	if _, err := scanFileWAL(f, decode, nil); err != nil {
		return err
	}
	_, err = scanFileWAL(f, decode, visit)
	return err
}

func scanFileWAL(f *os.File, decode func([]byte) (MutationOp, error), visit func(Entry) error) (uint64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	var magic [len(fileWALMagic)]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return 0, fmt.Errorf("%w: incomplete version header: %v", ErrFileWALCorrupt, err)
	}
	if string(magic[:]) != fileWALMagic {
		return 0, fmt.Errorf("%w: unsupported version header", ErrFileWALCorrupt)
	}
	var lastSeq uint64
	for {
		var header [fileWALFrameHeader]byte
		n, err := io.ReadFull(f, header[:])
		if err == io.EOF && n == 0 {
			return lastSeq, nil
		}
		if err != nil {
			return lastSeq, fmt.Errorf("%w: frame header after seq %d: %v", ErrFileWALTornTail, lastSeq, err)
		}
		bodyLen := binary.BigEndian.Uint32(header[:4])
		if bodyLen < fileWALBodyHeader || bodyLen > maxFileWALBody {
			return lastSeq, fmt.Errorf("%w: invalid frame length %d", ErrFileWALCorrupt, bodyLen)
		}
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(f, body); err != nil {
			return lastSeq, fmt.Errorf("%w: frame body after seq %d: %v", ErrFileWALTornTail, lastSeq, err)
		}
		crc := crc32.Checksum(header[:4], fileWALCRC)
		crc = crc32.Update(crc, fileWALCRC, body)
		if crc != binary.BigEndian.Uint32(header[4:8]) {
			return lastSeq, fmt.Errorf("%w: frame checksum after seq %d", ErrFileWALCorrupt, lastSeq)
		}
		seq := binary.BigEndian.Uint64(body[:8])
		if lastSeq == math.MaxUint64 || seq != lastSeq+1 {
			return lastSeq, fmt.Errorf("%w: got %d after %d", ErrFileWALSequence, seq, lastSeq)
		}
		var nodeID hlc.NodeID
		copy(nodeID[:], body[20:36])
		entry := Entry{
			Seq: seq,
			HLC: hlc.Timestamp{
				WallNs:  int64(binary.BigEndian.Uint64(body[8:16])),
				Logical: binary.BigEndian.Uint32(body[16:20]),
				NodeID:  nodeID,
			},
		}
		if decode != nil {
			entry.Op, err = decode(body[fileWALBodyHeader:])
			if err != nil {
				return lastSeq, fmt.Errorf("%w: decode seq %d: %v", ErrFileWALCorrupt, seq, err)
			}
		}
		if visit != nil {
			if err := visit(entry); err != nil {
				return lastSeq, err
			}
		}
		lastSeq = seq
	}
}
