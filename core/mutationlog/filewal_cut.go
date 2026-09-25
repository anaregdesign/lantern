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

// FileWALCut identifies exact on-disk bytes through one local sequence and
// reports the exact complete WAL tip observed by the same stable inspection.
// A zero sequence identifies the version header before the first frame. It is
// an artifact-binding witness, not proof that the WAL belongs to a given
// graph/receipt image or that commits after the observed tip were retained.
type FileWALCut struct {
	Seq                 uint64
	Offset              int64
	SHA256              [sha256.Size]byte
	ChainSHA256         [sha256.Size]byte
	ObservedLast        uint64
	ObservedOffset      int64
	ObservedSHA256      [sha256.Size]byte
	ObservedChainSHA256 [sha256.Size]byte
}

type fileWALCutPass struct {
	cuts     []FileWALCut
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
	cuts, err := InspectFileWALCuts(path, []uint64{seq}, decode, validate)
	if err != nil {
		return FileWALCut{}, err
	}
	return cuts[0], nil
}

// InspectFileWALCuts is the index-aligned multi-cut form of InspectFileWALCut.
// Every requested prefix and the complete observed tip are derived from the
// same two stable passes over one file identity. Duplicate and zero sequences
// are allowed; at least one cut is required.
func InspectFileWALCuts(path string, seqs []uint64, decode func([]byte) (MutationOp, error), validate func(Entry) error) ([]FileWALCut, error) {
	if decode == nil || validate == nil {
		return nil, errors.New("mutationlog: FileWAL cut decoder and entry validator are required")
	}
	if len(seqs) == 0 {
		return nil, errors.New("mutationlog: at least one FileWAL cut is required")
	}
	seqs = append([]uint64(nil), seqs...)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	initial, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !initial.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrFileWALCorrupt)
	}
	initialPath, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(initial, initialPath) {
		return nil, fmt.Errorf("%w: FileWAL path changed before cut inspection", ErrFileWALCorrupt)
	}
	first, err := inspectFileWALCutsPass(f, seqs, decode, validate)
	if err != nil {
		return nil, err
	}
	second, err := inspectFileWALCutsPass(f, seqs, decode, validate)
	if err != nil {
		return nil, err
	}
	final, err := f.Stat()
	if err != nil {
		return nil, err
	}
	finalPath, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(initial, final) || !os.SameFile(initial, finalPath) || initial.Size() != first.fullSize ||
		final.Size() != second.fullSize || !equalFileWALCutPass(first, second) {
		return nil, fmt.Errorf("%w: FileWAL changed during cut inspection", ErrFileWALCorrupt)
	}
	return first.cuts, nil
}

func inspectFileWALCutsPass(f *os.File, seqs []uint64, decode func([]byte) (MutationOp, error), validate func(Entry) error) (fileWALCutPass, error) {
	result := fileWALCutPass{cuts: make([]FileWALCut, len(seqs))}
	indexes := make(map[uint64][]int, len(seqs))
	for i, seq := range seqs {
		result.cuts[i].Seq = seq
		indexes[seq] = append(indexes[seq], i)
	}
	// scanFileWALFrames verifies these exact magic bytes before it can return
	// success. At seq zero, the prefix is the version header, not empty bytes.
	h := sha256.New()
	_, _ = h.Write([]byte(fileWALMagic))
	chain := fileWALChainSeed()
	for _, i := range indexes[0] {
		result.cuts[i].Offset = int64(len(fileWALMagic))
		result.cuts[i].SHA256 = digestFileWALPrefix(h)
		result.cuts[i].ChainSHA256 = chain
	}
	offset := int64(len(fileWALMagic))
	last, err := scanFileWALFrames(f, decode, validate, func(entry Entry, header, body []byte) error {
		_, _ = h.Write(header)
		_, _ = h.Write(body)
		chain = fileWALChainNext(chain, header, body)
		offset += int64(len(header) + len(body))
		for _, i := range indexes[entry.Seq] {
			result.cuts[i].Offset = offset
			result.cuts[i].SHA256 = digestFileWALPrefix(h)
			result.cuts[i].ChainSHA256 = chain
		}
		return nil
	})
	if err != nil {
		return fileWALCutPass{}, err
	}
	for _, seq := range seqs {
		if seq > last {
			return fileWALCutPass{}, fmt.Errorf("%w: requested seq %d exceeds last seq %d", ErrFileWALCutUnavailable, seq, last)
		}
	}
	end, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return fileWALCutPass{}, err
	}
	if end != offset {
		return fileWALCutPass{}, fmt.Errorf("%w: FileWAL scan offset differs from frame lengths", ErrFileWALCorrupt)
	}
	fullHash := digestFileWALPrefix(h)
	for i := range result.cuts {
		result.cuts[i].ObservedLast = last
		result.cuts[i].ObservedOffset = end
		result.cuts[i].ObservedSHA256 = fullHash
		result.cuts[i].ObservedChainSHA256 = chain
	}
	result.fullSize = end
	result.fullHash = fullHash
	return result, nil
}

func equalFileWALCutPass(a, b fileWALCutPass) bool {
	if a.fullSize != b.fullSize || a.fullHash != b.fullHash || len(a.cuts) != len(b.cuts) {
		return false
	}
	for i := range a.cuts {
		if a.cuts[i] != b.cuts[i] {
			return false
		}
	}
	return true
}

func digestFileWALPrefix(h hash.Hash) [sha256.Size]byte {
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
