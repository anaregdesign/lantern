//go:build !windows

package privatefile

import (
	"errors"
	"os"
)

func checkNative(_ *os.File, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return ErrUnsafe
	}
	return nil
}

func createNative(path string, flags int) (*os.File, error) {
	return os.OpenFile(path, flags|os.O_CREATE|os.O_EXCL, 0600)
}

func syncDirectoryNative(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return errors.Join(ErrUnsafe, file.Close())
	}
	return errors.Join(file.Sync(), file.Close())
}
