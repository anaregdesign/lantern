package peerauth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointOwnsPrivateClassifiedAtomicImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "membership")
	raw := []byte("first")
	if err := writeCheckpoint(p, raw, true); err != nil {
		t.Fatal(err)
	}
	if got, err := readCheckpoint(p); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("first cut", err)
	}
	if err := writeCheckpoint(p, []byte("second"), false); err != nil {
		t.Fatal(err)
	}
	if got, err := readCheckpoint(p); err != nil || string(got) != "second" {
		t.Fatal("atomic replacement", err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCheckpoint(p); err == nil {
		t.Fatal("public state permissions accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCheckpoint(link); err == nil {
		t.Fatal("symlink accepted")
	}
}
