//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRetainedEvidenceReplacementFIFORefusesBeforeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, []byte(`{"owned":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := readRetainedFileWith(path, 4096, func(name string) (*os.File, error) {
		return openRetainedEvidenceWith(name, func(candidate string, flags int, mode os.FileMode) (*os.File, error) {
			// Assert before actually opening the FIFO, so a regression fails instead
			// of blocking this test while waiting for a nonexistent FIFO writer.
			if flags&syscall.O_NONBLOCK == 0 || flags&syscall.O_NOFOLLOW == 0 {
				t.Fatal("custody open lacks nonblocking/no-follow flags")
			}
			called = true
			if err := os.Remove(candidate); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(candidate, 0o600); err != nil {
				t.Fatal(err)
			}
			return os.OpenFile(candidate, flags, mode)
		})
	})
	if !called || err == nil {
		t.Fatalf("replacement FIFO accepted: called=%t err=%v", called, err)
	}
}

func TestRetainedEvidenceOpenedModeAndLinksAreChecked(t *testing.T) {
	for _, change := range []string{"mode", "link"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "evidence.json")
			if err := os.WriteFile(path, []byte(`{"owned":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readRetainedFileWith(path, 4096, func(name string) (*os.File, error) {
				if change == "mode" {
					if err := os.Chmod(name, 0o644); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Link(name, name+".link"); err != nil {
					t.Fatal(err)
				}
				return openRetainedEvidence(name)
			})
			if err == nil {
				t.Fatal("opened metadata drift accepted")
			}
		})
	}
}
