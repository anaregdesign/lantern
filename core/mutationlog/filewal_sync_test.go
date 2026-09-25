package mutationlog

import (
	"path/filepath"
	"testing"
)

func TestSyncFileWALDirectory(t *testing.T) {
	if err := syncFileWALDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := syncFileWALDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("sync missing FileWAL directory succeeded")
	}
}
