package service

import (
	"path/filepath"
	"testing"
)

func TestSyncReceiptDirectory(t *testing.T) {
	if err := syncReceiptDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := syncReceiptDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("sync missing receipt directory succeeded")
	}
}
