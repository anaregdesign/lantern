package security

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestFreshAuthorityStorageRefusesRetainedFamilies(t *testing.T) {
	for _, suffix := range []string{"", ".tip", ".current", ".segment-00000000000000000000000000000001", ".segment-orphan", ".current-tmp-interrupted"} {
		t.Run("suffix="+suffix, func(t *testing.T) {
			anchor := filepath.Join(t.TempDir(), "authority.wal")
			path := anchor + suffix
			original := []byte("retained authority state must survive refusal")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := requireFreshAuthorityStorage(anchor); !errors.Is(err, os.ErrExist) {
				t.Fatalf("retained family accepted: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(got) != sha256.Sum256(original) {
				t.Fatal("refusal changed retained bytes", err)
			}
		})
	}
	t.Run("unrelated siblings and empty lease are not state", func(t *testing.T) {
		anchor := filepath.Join(t.TempDir(), "authority.wal")
		for _, path := range []string{anchor + ".lease", anchor + ".segment", anchor + ".currently", anchor + "-other.current"} {
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := requireFreshAuthorityStorage(anchor); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("symlink selector is retained without following it", func(t *testing.T) {
		dir := t.TempDir()
		anchor, target := filepath.Join(dir, "authority.wal"), filepath.Join(dir, "retained")
		original := []byte("private retained target")
		if err := os.WriteFile(target, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, anchor+".current"); err != nil {
			t.Fatal(err)
		}
		if err := requireFreshAuthorityStorage(anchor); !errors.Is(err, os.ErrExist) {
			t.Fatal("symlink selector accepted", err)
		}
		got, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(got, original) {
			t.Fatal("symlink target changed", err)
		}
		if link, err := os.Readlink(anchor + ".current"); err != nil || link != target {
			t.Fatal("selector link changed", err)
		}
	})
}

func TestAuthorityStorageCreatorsRefuseRotatedState(t *testing.T) {
	genesis, _, _, _ := s2TestHistory(t)
	for _, kind := range []string{"protocol", "materialized"} {
		t.Run(kind, func(t *testing.T) {
			anchor := filepath.Join(t.TempDir(), "authority.wal")
			retained := map[string][]byte{
				anchor + ".current":       []byte("selected retired segment"),
				anchor + ".segment-old":   []byte("retained retired history"),
				anchor + ".current-tmp-x": []byte("interrupted selector"),
			}
			for path, raw := range retained {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if kind == "protocol" {
				var owner *s2cJournal
				owner, err = createS2CJournal(anchor, [32]byte{1}, s2cJournalPolicy{8192, 32}, []byte("genesis"))
				if owner != nil {
					_ = owner.close()
					t.Fatal("protocol owner adopted retired family")
				}
			} else {
				var owner *s2LocalOwner
				owner, err = createS2Local(anchor, s2OwnerTestScope(genesis), genesis)
				if owner != nil {
					_ = owner.Close()
					t.Fatal("materialized owner adopted retired family")
				}
			}
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("wrong refusal: %v", err)
			}
			for path, original := range retained {
				got, err := os.ReadFile(path)
				if err != nil || sha256.Sum256(got) != sha256.Sum256(original) {
					t.Fatal("creator changed retained bytes", err)
				}
			}
			for _, path := range []string{anchor, anchor + ".tip"} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refusal created a new state family", path, err)
				}
			}
			lease, err := mutationlog.AcquireFileWALLease(anchor)
			if err != nil {
				t.Fatal("refusal retained ownership", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
