package security

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
)

const systemManifestMagic = "LNSMAN01"
const systemManifestBytes = len(systemManifestMagic) + 32 + 16 + 8 + 32 + 32

// nativeManifest selects exactly one WAL segment. Its binding pins the operator
// authority/generation; the selected first checkpoint keeps its original writer
// signature. A missing/corrupt selector never triggers a scan for older state.
type nativeManifest struct {
	binding    [32]byte
	segment    [16]byte
	base       uint64
	checkpoint [32]byte
}

func (m nativeManifest) path(anchor string) string {
	if m.segment == [16]byte{} {
		return anchor
	}
	return anchor + ".segment-" + hex.EncodeToString(m.segment[:])
}
func (m nativeManifest) encode() []byte {
	raw := append([]byte(systemManifestMagic), m.binding[:]...)
	raw = append(raw, m.segment[:]...)
	raw = binary.BigEndian.AppendUint64(raw, m.base)
	raw = append(raw, m.checkpoint[:]...)
	sum := sha256.Sum256(raw)
	return append(raw, sum[:]...)
}
func decodeNativeManifest(raw []byte, binding [32]byte) (nativeManifest, error) {
	var result nativeManifest
	if len(raw) != systemManifestBytes || string(raw[:len(systemManifestMagic)]) != systemManifestMagic {
		return result, ErrInvalidRevision
	}
	digest := sha256.Sum256(raw[:len(raw)-32])
	if !bytes.Equal(digest[:], raw[len(raw)-32:]) {
		return result, ErrInvalidRevision
	}
	offset := len(systemManifestMagic)
	copy(result.binding[:], raw[offset:offset+32])
	offset += 32
	copy(result.segment[:], raw[offset:offset+16])
	offset += 16
	result.base = binary.BigEndian.Uint64(raw[offset : offset+8])
	offset += 8
	copy(result.checkpoint[:], raw[offset:offset+32])
	if result.binding != binding || result.binding == [32]byte{} || result.base >= math.MaxUint64-1 ||
		(result.segment == [16]byte{} && (result.base != 0 || result.checkpoint != [32]byte{})) ||
		(result.segment != [16]byte{} && result.checkpoint == [32]byte{}) {
		return nativeManifest{}, ErrInvalidRevision
	}
	return result, nil
}
func readNativeManifest(anchor string, binding [32]byte) (nativeManifest, error) {
	path := anchor + ".current"
	info, err := os.Lstat(path)
	if err != nil {
		return nativeManifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(systemManifestBytes) {
		return nativeManifest{}, ErrInvalidRevision
	}
	file, err := os.Open(path)
	if err != nil {
		return nativeManifest{}, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nativeManifest{}, ErrInvalidRevision
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(systemManifestBytes+1)))
	if err != nil {
		return nativeManifest{}, err
	}
	return decodeNativeManifest(raw, binding)
}
func writeNativeManifest(anchor string, manifest nativeManifest, fresh bool) error {
	target := anchor + ".current"
	var file *os.File
	var err error
	if fresh {
		file, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	} else {
		file, err = os.CreateTemp(filepath.Dir(anchor), filepath.Base(anchor)+".current-tmp-")
	}
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		if temporary != target {
			_ = os.Remove(temporary)
		}
	}()
	raw := manifest.encode()
	if written, err := file.Write(raw); err != nil {
		return err
	} else if written != len(raw) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if !fresh {
		if err := os.Rename(temporary, target); err != nil {
			return err
		}
	}
	return syncSystemDirectory(filepath.Dir(anchor))
}
func syncSystemDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
func newNativeManifest(binding [32]byte, revision *Revision) (nativeManifest, error) {
	if revision == nil || !revision.completeCheckpointHistory() {
		return nativeManifest{}, ErrInvalidRevision
	}
	manifest := nativeManifest{binding: binding, base: revision.sequence - 1, checkpoint: revision.digest}
	if _, err := rand.Read(manifest.segment[:]); err != nil {
		return nativeManifest{}, err
	}
	if manifest.segment == [16]byte{} {
		return nativeManifest{}, ErrInvalidRevision
	}
	return manifest, nil
}
