//go:build !windows

package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixModeGuards(t *testing.T) {
	file, err := Create(filepath.Join(t.TempDir(), "private"), os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	for _, mode := range []os.FileMode{0600, 0400, 0640, 0604, 0666} {
		if err := file.Chmod(mode); err != nil {
			t.Fatal(err)
		}
		err := Check(file)
		if mode&0077 == 0 && err != nil || mode&0077 != 0 && !errors.Is(err, ErrUnsafe) {
			t.Fatal("owner-only mode contract changed", mode, err)
		}
	}
}
