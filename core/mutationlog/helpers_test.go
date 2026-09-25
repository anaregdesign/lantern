package mutationlog

import (
	"crypto/sha256"
	"path/filepath"
	"testing"
)

func fileWALTipFixture(t *testing.T) (string, *FileWALLease, *FileWAL, *FileWALTipJournal, [sha256.Size]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mutations.wal")
	lease, err := AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	wal, err := CreateFileWAL(lease.Path(), fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wal.Close() })
	binding := sha256.Sum256([]byte("receipt epoch and policy"))
	journal, err := CreateFileWALTipJournal(lease.Path(), binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.VerifyAndCatchUp(lease.Path(), fileWALStringDecode, fileWALCutValidEntry); err != nil {
		t.Fatal(err)
	}
	if err := wal.BindTipJournal(journal); err != nil {
		t.Fatal(err)
	}
	return path, lease, wal, journal, binding
}
