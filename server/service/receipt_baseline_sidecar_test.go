package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReceiptBaselineSidecarPersistsVerifiesAndCleansOrphans(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "receipts.wal")
	store := receiptBaselineSidecarStore{walPath: walPath}
	raw := []byte("canonical combined baseline")
	digest, path, err := store.persist(ReceiptBaselineFormatCombined, raw)
	if err != nil {
		t.Fatal(err)
	}
	expectedPath := walPath + ".receipt." + hex.EncodeToString(digest[:]) + ".baseline"
	if digest != sha256.Sum256(raw) ||
		path != expectedPath ||
		path != receiptBaselineSidecarPath(walPath, digest) {
		t.Fatalf("persisted sidecar = %x %q", digest, path)
	}
	got, err := store.load(ReceiptBaselineFormatCombined, digest, uint64(len(raw)))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("loaded sidecar = %q, %v", got, err)
	}
	if secondDigest, secondPath, err := store.persist(ReceiptBaselineFormatCombined, raw); err != nil ||
		secondDigest != digest || secondPath != path {
		t.Fatalf("idempotent persist = %x %q, %v", secondDigest, secondPath, err)
	}

	orphanRaw := []byte("orphan")
	orphanDigest, orphanPath, err := store.persist(ReceiptBaselineFormatCombined, orphanRaw)
	if err != nil {
		t.Fatal(err)
	}
	tempPrefix := receiptBaselineTempPrefix(walPath, orphanDigest)
	expectedTempPrefix := filepath.Join(
		filepath.Dir(walPath),
		"."+filepath.Base(walPath)+".receipt."+hex.EncodeToString(orphanDigest[:])+".tmp-",
	)
	if tempPrefix != expectedTempPrefix {
		t.Fatalf("temporary sidecar prefix = %q, want %q", tempPrefix, expectedTempPrefix)
	}
	temp := tempPrefix + "stale"
	if err := os.WriteFile(temp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrecognized := filepath.Join(filepath.Dir(walPath), filepath.Base(walPath)+".neighbor.keep")
	if err := os.WriteFile(unrecognized, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.cleanup(receiptBaselineReference{
		Format: ReceiptBaselineFormatCombined,
		Digest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("selected sidecar removed: %v", err)
	}
	for _, removed := range []string{orphanPath, temp} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan %q remains: %v", removed, err)
		}
	}
	if _, err := os.Stat(unrecognized); err != nil {
		t.Fatalf("unrecognized neighbor removed: %v", err)
	}

	zeroDigestPath := receiptBaselineSidecarPath(
		walPath,
		[sha256.Size]byte{},
	)
	if err := os.WriteFile(zeroDigestPath, []byte("zero orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.cleanup(receiptBaselineReference{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(zeroDigestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("zero-digest orphan remains after no-marker cleanup: %v", err)
	}
}

func TestReceiptBaselineSidecarFailsClosedOnDamageAndFaults(t *testing.T) {
	t.Run("before rename", func(t *testing.T) {
		walPath := filepath.Join(t.TempDir(), "receipts.wal")
		store := receiptBaselineSidecarStore{
			walPath: walPath,
			fault: func(point receiptBaselineSidecarFaultPoint) error {
				if point == receiptBaselineBeforeRename {
					return errors.New("injected")
				}
				return nil
			},
		}
		raw := []byte("before")
		digest := sha256.Sum256(raw)
		if _, _, err := store.persist(ReceiptBaselineFormatCombined, raw); err == nil {
			t.Fatal("before-rename fault succeeded")
		}
		if _, err := os.Stat(receiptBaselineSidecarPath(
			walPath,
			digest,
		)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("before-rename fault published sidecar: %v", err)
		}
	})

	t.Run("after rename", func(t *testing.T) {
		walPath := filepath.Join(t.TempDir(), "receipts.wal")
		store := receiptBaselineSidecarStore{
			walPath: walPath,
			fault: func(point receiptBaselineSidecarFaultPoint) error {
				if point == receiptBaselineAfterRename {
					return errors.New("injected")
				}
				return nil
			},
		}
		raw := []byte("after")
		digest := sha256.Sum256(raw)
		if _, _, err := store.persist(ReceiptBaselineFormatCombined, raw); err == nil {
			t.Fatal("after-rename fault succeeded")
		}
		if got, err := (receiptBaselineSidecarStore{walPath: walPath}).load(
			ReceiptBaselineFormatCombined,
			digest,
			uint64(len(raw)),
		); err != nil ||
			!bytes.Equal(got, raw) {
			t.Fatalf("renamed orphan is not valid: %q, %v", got, err)
		}
		retry := receiptBaselineSidecarStore{walPath: walPath}
		if gotDigest, gotPath, err := retry.persist(ReceiptBaselineFormatCombined, raw); err != nil ||
			gotDigest != digest ||
			gotPath != receiptBaselineSidecarPath(walPath, digest) {
			t.Fatalf("retry did not durably adopt renamed sidecar: %x %q, %v", gotDigest, gotPath, err)
		}
	})

	t.Run("corrupt truncate oversized and symlink", func(t *testing.T) {
		walPath := filepath.Join(t.TempDir(), "receipts.wal")
		store := receiptBaselineSidecarStore{walPath: walPath}
		raw := []byte("durable")
		digest, path, err := store.persist(ReceiptBaselineFormatCombined, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw[:3], 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.load(ReceiptBaselineFormatCombined, digest, uint64(len(raw))); !errors.Is(err, errReceiptBaselineSidecar) {
			t.Fatalf("truncated sidecar = %v", err)
		}
		if _, err := store.load(
			ReceiptBaselineFormatCombined,
			digest,
			maxCombinedReceiptBaselineBytes+1,
		); !errors.Is(err, errReceiptBaselineSidecar) {
			t.Fatalf("oversized marker = %v", err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(filepath.Dir(path), "target")
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := store.load(ReceiptBaselineFormatCombined, digest, uint64(len(raw))); !errors.Is(err, errReceiptBaselineSidecar) {
			t.Fatalf("symlink sidecar = %v", err)
		}
	})
}

func TestReceiptBaselineSidecarRejectsRetiredVersionedPath(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "receipts.wal")
	raw := []byte("retired baseline")
	digest := sha256.Sum256(raw)
	retiredPath := walPath + ".receipt-" + "v2." + hex.EncodeToString(digest[:]) + ".baseline"
	if err := os.WriteFile(retiredPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	store := receiptBaselineSidecarStore{walPath: walPath}
	if got, err := store.load(ReceiptBaselineFormatCombined, digest, uint64(len(raw))); got != nil ||
		!errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired versioned sidecar loaded: %q, %v", got, err)
	}
	if recognizedReceiptBaselineSidecarName(filepath.Base(retiredPath), filepath.Base(walPath)) {
		t.Fatal("retired versioned sidecar recognized as canonical")
	}
	if err := store.cleanup(receiptBaselineReference{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(retiredPath); err != nil {
		t.Fatalf("retired versioned sidecar was removed or migrated: %v", err)
	}
}
