package mutationlog

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
)

// ErrFileWALCutUnavailable means the requested cut has no complete frame in
// the validated file. A zero cut is the version header before the first frame.
var ErrFileWALCutUnavailable = errors.New("mutationlog: FileWAL cut is unavailable")

// FileWALCut identifies exact on-disk bytes through one local sequence. It
// is an artifact-binding witness, not proof that the WAL belongs to a given
// graph/receipt image or that no later commit was lost.
type FileWALCut struct {
	Seq          uint64
	Offset       int64
	SHA256       [sha256.Size]byte
	ObservedLast uint64
}

type fileWALCutPass struct {
	cut      FileWALCut
	fullSize int64
	fullHash [sha256.Size]byte
}

// InspectFileWALCut checks the entire file, including a suffix after seq, and
// hashes the original frame bytes through seq. The caller must exclusively
// own a closed or otherwise non-mutating FileWAL path for the whole call;
// FileWAL itself does not hold an OS lock. Two same-fd passes plus initial /
// final regular-file identity and size checks detect many races but cannot
// make an uncooperative concurrent writer safe. decode and validate must be
// deterministic and side-effect-free; decode rejects malformed payloads and
// validate checks application-specific relationships between the decoded
// payload and frame metadata. The function never repairs or writes a WAL.
func InspectFileWALCut(path string, seq uint64, decode func([]byte) (MutationOp, error), validate func(Entry) error) (FileWALCut, error) {
	if decode == nil || validate == nil {
		return FileWALCut{}, errors.New("mutationlog: FileWAL cut decoder and entry validator are required")
	}
	f, err := os.Open(path)
	if err != nil {
		return FileWALCut{}, err
	}
	defer f.Close()
	initial, err := f.Stat()
	if err != nil {
		return FileWALCut{}, err
	}
	if !initial.Mode().IsRegular() {
		return FileWALCut{}, fmt.Errorf("%w: not a regular file", ErrFileWALCorrupt)
	}
	initialPath, err := os.Stat(path)
	if err != nil {
		return FileWALCut{}, err
	}
	if !os.SameFile(initial, initialPath) {
		return FileWALCut{}, fmt.Errorf("%w: FileWAL path changed before cut inspection", ErrFileWALCorrupt)
	}
	first, err := inspectFileWALCutPass(f, seq, decode, validate)
	if err != nil {
		return FileWALCut{}, err
	}
	second, err := inspectFileWALCutPass(f, seq, decode, validate)
	if err != nil {
		return FileWALCut{}, err
	}
	final, err := f.Stat()
	if err != nil {
		return FileWALCut{}, err
	}
	finalPath, err := os.Stat(path)
	if err != nil {
		return FileWALCut{}, err
	}
	if !os.SameFile(initial, final) || !os.SameFile(initial, finalPath) || initial.Size() != first.fullSize ||
		final.Size() != second.fullSize || first != second {
		return FileWALCut{}, fmt.Errorf("%w: FileWAL changed during cut inspection", ErrFileWALCorrupt)
	}
	return first.cut, nil
}

func inspectFileWALCutPass(f *os.File, seq uint64, decode func([]byte) (MutationOp, error), validate func(Entry) error) (fileWALCutPass, error) {
	var result fileWALCutPass
	result.cut.Seq = seq
	// scanFileWALFrames verifies these exact magic bytes before it can return
	// success. At seq zero, the prefix is the version header, not empty bytes.
	h := sha256.New()
	_, _ = h.Write([]byte(fileWALMagic))
	if seq == 0 {
		result.cut.Offset = int64(len(fileWALMagic))
		result.cut.SHA256 = digestFileWALPrefix(h)
	}
	offset := int64(len(fileWALMagic))
	last, err := scanFileWALFrames(f, decode, validate, func(entry Entry, header, body []byte) error {
		_, _ = h.Write(header)
		_, _ = h.Write(body)
		offset += int64(len(header) + len(body))
		if entry.Seq == seq {
			result.cut.Offset = offset
			result.cut.SHA256 = digestFileWALPrefix(h)
		}
		return nil
	})
	if err != nil {
		return fileWALCutPass{}, err
	}
	if seq > last {
		return fileWALCutPass{}, fmt.Errorf("%w: requested seq %d exceeds last seq %d", ErrFileWALCutUnavailable, seq, last)
	}
	end, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return fileWALCutPass{}, err
	}
	if end != offset {
		return fileWALCutPass{}, fmt.Errorf("%w: FileWAL scan offset differs from frame lengths", ErrFileWALCorrupt)
	}
	result.cut.ObservedLast = last
	result.fullSize = end
	result.fullHash = digestFileWALPrefix(h)
	return result, nil
}

func digestFileWALPrefix(h hash.Hash) [sha256.Size]byte {
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
