//go:build darwin || linux

package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorityTimeNativeFileRefusesMissingAndOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured-time-input")
	if _, err := readAuthorityTimeFile(path, 4096); err == nil {
		t.Fatal("missing native input admitted")
	}
	for _, size := range []int{0, 4096, 4097} {
		if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
		raw, err := readAuthorityTimeFile(path, 4096)
		if size == 4096 {
			if err != nil || len(raw) != size {
				t.Fatal("bounded input rejected", err)
			}
		} else if err == nil {
			t.Fatal("empty/oversized native input admitted")
		}
	}
}
