package service

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errReceiptBaselineSidecar = errors.New("service: invalid RECEIPT_V1 baseline sidecar")

type receiptBaselineSidecarFaultPoint string

const (
	receiptBaselineBeforeRename  receiptBaselineSidecarFaultPoint = "before-sidecar-rename"
	receiptBaselineAfterRename   receiptBaselineSidecarFaultPoint = "after-sidecar-rename"
	receiptBaselineBeforeCleanup receiptBaselineSidecarFaultPoint = "before-sidecar-cleanup"
)

type receiptBaselineSidecarStore struct {
	walPath string
	fault   func(receiptBaselineSidecarFaultPoint) error
}

func (s receiptBaselineSidecarStore) persist(raw []byte) ([sha256.Size]byte, string, error) {
	var zero [sha256.Size]byte
	if len(raw) == 0 || len(raw) > maxReceiptBaselineBytes {
		return zero, "", fmt.Errorf("%w: invalid byte length %d", errReceiptBaselineSidecar, len(raw))
	}
	digest := sha256.Sum256(raw)
	finalPath := receiptBaselineSidecarPath(s.walPath, digest)
	if existing, err := s.load(digest, uint64(len(raw))); err == nil {
		if !bytes.Equal(existing, raw) {
			return zero, "", fmt.Errorf("%w: content-addressed file differs", errReceiptBaselineSidecar)
		}
		if err := syncReceiptBaselineDirectory(filepath.Dir(s.walPath)); err != nil {
			return zero, "", err
		}
		return digest, finalPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, "", err
	}

	dir := filepath.Dir(s.walPath)
	var nonce [8]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return zero, "", fmt.Errorf("service: create baseline nonce: %w", err)
	}
	tempPath := receiptBaselineTempPrefix(s.walPath, digest) + hex.EncodeToString(nonce[:])
	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return zero, "", fmt.Errorf("service: create baseline sidecar: %w", err)
	}
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := writeReceiptBaselineFile(file, raw); err != nil {
		return zero, "", err
	}
	if err := file.Sync(); err != nil {
		return zero, "", fmt.Errorf("service: sync baseline sidecar: %w", err)
	}
	if err := file.Close(); err != nil {
		return zero, "", fmt.Errorf("service: close baseline sidecar: %w", err)
	}
	if err := s.inject(receiptBaselineBeforeRename); err != nil {
		return zero, "", err
	}
	if _, err := os.Lstat(finalPath); err == nil {
		existing, loadErr := s.load(digest, uint64(len(raw)))
		if loadErr != nil || !bytes.Equal(existing, raw) {
			return zero, "", errors.Join(errReceiptBaselineSidecar, loadErr)
		}
		if err := syncReceiptBaselineDirectory(dir); err != nil {
			return zero, "", err
		}
		return digest, finalPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, "", fmt.Errorf("service: inspect baseline sidecar target: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return zero, "", fmt.Errorf("service: publish baseline sidecar: %w", err)
	}
	removeTemp = false
	if err := s.inject(receiptBaselineAfterRename); err != nil {
		return zero, "", err
	}
	if err := syncReceiptBaselineDirectory(dir); err != nil {
		return zero, "", err
	}
	return digest, finalPath, nil
}

func syncReceiptBaselineDirectory(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("service: open baseline directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return fmt.Errorf("service: sync baseline directory: %w", errors.Join(syncErr, closeErr))
	}
	return nil
}

func writeReceiptBaselineFile(file *os.File, raw []byte) error {
	for len(raw) != 0 {
		n, err := file.Write(raw)
		if err != nil {
			return fmt.Errorf("service: write baseline sidecar: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("service: write baseline sidecar: %w", io.ErrShortWrite)
		}
		raw = raw[n:]
	}
	return nil
}

func (s receiptBaselineSidecarStore) inject(point receiptBaselineSidecarFaultPoint) error {
	if s.fault == nil {
		return nil
	}
	if err := s.fault(point); err != nil {
		return fmt.Errorf("service: baseline fault at %s: %w", point, err)
	}
	return nil
}

func (s receiptBaselineSidecarStore) load(digest [sha256.Size]byte, size uint64) ([]byte, error) {
	if digest == ([sha256.Size]byte{}) || size == 0 || size > maxReceiptBaselineBytes {
		return nil, fmt.Errorf("%w: invalid digest or size", errReceiptBaselineSidecar)
	}
	path := receiptBaselineSidecarPath(s.walPath, digest)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || uint64(before.Size()) != size {
		return nil, fmt.Errorf("%w: sidecar is not a regular file of the committed size", errReceiptBaselineSidecar)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("service: open baseline sidecar: %w", err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("service: stat baseline sidecar: %w", err)
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%w: sidecar identity changed while opening", errReceiptBaselineSidecar)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(size)+1))
	if err != nil {
		return nil, fmt.Errorf("service: read baseline sidecar: %w", err)
	}
	if uint64(len(raw)) != size || sha256.Sum256(raw) != digest {
		return nil, fmt.Errorf("%w: sidecar length or digest mismatch", errReceiptBaselineSidecar)
	}
	return raw, nil
}

func (s receiptBaselineSidecarStore) cleanup(keep [sha256.Size]byte) error {
	if err := s.inject(receiptBaselineBeforeCleanup); err != nil {
		return err
	}
	dir := filepath.Dir(s.walPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("service: list baseline sidecars: %w", err)
	}
	finalPrefix := filepath.Base(s.walPath) + ".receipt-v1."
	tempPrefix := "." + finalPrefix
	var keepName string
	if keep != ([sha256.Size]byte{}) {
		keepName = filepath.Base(receiptBaselineSidecarPath(s.walPath, keep))
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if keepName != "" && name == keepName {
			continue
		}
		if !recognizedReceiptBaselineSidecarName(name, finalPrefix, tempPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("service: remove orphan baseline %q: %w", name, err)
		}
		removed = true
	}
	if removed {
		if err := syncReceiptBaselineDirectory(dir); err != nil {
			return fmt.Errorf("service: persist orphan baseline cleanup: %w", err)
		}
	}
	return nil
}

func recognizedReceiptBaselineSidecarName(name, finalPrefix, tempPrefix string) bool {
	if strings.HasPrefix(name, finalPrefix) && strings.HasSuffix(name, ".baseline") {
		digest := strings.TrimSuffix(strings.TrimPrefix(name, finalPrefix), ".baseline")
		return len(digest) == sha256.Size*2 && validLowerHex(digest)
	}
	if strings.HasPrefix(name, tempPrefix) {
		rest := strings.TrimPrefix(name, tempPrefix)
		digest, suffix, ok := strings.Cut(rest, ".tmp-")
		return ok && len(digest) == sha256.Size*2 && validLowerHex(digest) && suffix != ""
	}
	return false
}

func validLowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func receiptBaselineSidecarPath(walPath string, digest [sha256.Size]byte) string {
	return walPath + ".receipt-v1." + hex.EncodeToString(digest[:]) + ".baseline"
}

func receiptBaselineTempPrefix(walPath string, digest [sha256.Size]byte) string {
	return filepath.Join(
		filepath.Dir(walPath),
		"."+filepath.Base(walPath)+".receipt-v1."+hex.EncodeToString(digest[:])+".tmp-",
	)
}
