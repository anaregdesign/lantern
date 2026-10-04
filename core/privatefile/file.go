// Package privatefile creates and validates private regular files using the
// native platform security model. It has no application-policy semantics.
package privatefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

var ErrUnsafe = errors.New("privatefile: unsafe file security")

// Check verifies the opened file, never a pathname's security descriptor.
func Check(file *os.File) error {
	if file == nil {
		return ErrUnsafe
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ErrUnsafe
	}
	return checkNative(file, info)
}

// Create installs private security before creating an exclusive new file.
// flags may select read/write and append, but cannot replace an existing file.
func Create(path string, flags int) (*os.File, error) {
	if flags & ^(os.O_WRONLY|os.O_RDWR|os.O_APPEND) != 0 || flags&os.O_WRONLY != 0 && flags&os.O_RDWR != 0 {
		return nil, ErrUnsafe
	}
	return createNative(path, flags)
}

// CreateTemp creates an exclusive owner-only file in the supplied directory.
func CreateTemp(dir, prefix string) (*os.File, error) {
	if filepath.Base(prefix) != prefix {
		return nil, ErrUnsafe
	}
	for range 8 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		file, err := Create(filepath.Join(dir, prefix+hex.EncodeToString(random[:])), os.O_RDWR)
		if !errors.Is(err, os.ErrExist) {
			return file, err
		}
	}
	return nil, os.ErrExist
}

// SyncDirectory propagates the platform's native directory flush result.
// It does not substitute a successful file flush for a failed directory flush.
func SyncDirectory(path string) error { return syncDirectoryNative(path) }
