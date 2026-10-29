//go:build linux || darwin

package benchheap

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Store confines all operations to one already-owned physical directory. The
// platform wiring supplies a kernel atomic no-replace rename; never os.Rename.
type Store struct {
	root      *os.Root
	directory *os.File
	identity  os.FileInfo
	rename    func(int, string, string) error
}

func OpenStore(path string, rename func(int, string, string) error) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil || physical != absolute || rename == nil {
		return nil, ErrInput
	}
	fd, err := syscall.Open(absolute, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), absolute)
	info, err := dir.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		dir.Close()
		return nil, ErrInput
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		dir.Close()
		return nil, err
	}
	store := &Store{root: root, directory: dir, identity: info, rename: rename}
	if err := store.Check(); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Check() error {
	for _, infoErr := range []func() (os.FileInfo, error){s.directory.Stat, func() (os.FileInfo, error) { return os.Lstat(s.directory.Name()) }, func() (os.FileInfo, error) { return s.root.Stat(".") }} {
		info, err := infoErr()
		if err != nil || !os.SameFile(s.identity, info) || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return ErrInput
		}
	}
	return nil
}
func (s *Store) Close() error { return errors.Join(s.root.Close(), s.directory.Close()) }

func fileOK(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0600 && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1
}
func (s *Store) Read(name string, value any, limit int) (readErr error) {
	if filepath.Base(name) != name || name == "." || s.Check() != nil {
		return ErrInput
	}
	// Root.OpenFile may resolve a final symlink inside its confined root before
	// opening the file. Check the directory entry itself, then bind the opened
	// descriptor and the final entry to that same owned regular file.
	entry, err := s.root.Lstat(name)
	if err != nil || !fileOK(entry) || entry.Size() > int64(limit) {
		return ErrInput
	}
	file, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { readErr = errors.Join(readErr, file.Close()) }()
	before, err := file.Stat()
	if err != nil || !fileOK(before) || !os.SameFile(entry, before) ||
		entry.Size() != before.Size() || !entry.ModTime().Equal(before.ModTime()) {
		return ErrInput
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil || !fileOK(after) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(raw) != int(after.Size()) || s.Check() != nil {
		return ErrInput
	}
	final, err := s.root.Lstat(name)
	if err != nil || !fileOK(final) || !os.SameFile(before, final) ||
		before.Size() != final.Size() || !before.ModTime().Equal(final.ModTime()) {
		return ErrInput
	}
	return Decode(raw, value, limit)
}
func (s *Store) Publish(name string, value any) (publishErr error) {
	if filepath.Base(name) != name || name == "." || s.Check() != nil {
		return ErrInput
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > MaxOutputBytes {
		return ErrInput
	}
	temp := "." + name + ".tmp"
	// Root.OpenFile may follow a final symlink before applying O_EXCL. Admit
	// only absent entries, and bind the created descriptor to its own regular
	// directory entry before writing any bytes.
	for _, entry := range []string{temp, name} {
		if _, err := s.root.Lstat(entry); !errors.Is(err, os.ErrNotExist) {
			return ErrInput
		}
	}
	file, err := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer func() { publishErr = errors.Join(publishErr, file.Close()) }()
	created, err := file.Stat()
	if err != nil || !fileOK(created) || created.Size() != 0 {
		return ErrInput
	}
	matches := func(entry string, expected os.FileInfo) bool {
		actual, err := s.root.Lstat(entry)
		return err == nil && fileOK(actual) && os.SameFile(expected, actual) &&
			expected.Size() == actual.Size() && expected.ModTime().Equal(actual.ModTime())
	}
	if !matches(temp, created) || s.Check() != nil {
		return ErrInput
	}
	_, writeErr := file.Write(append(raw, '\n'))
	if err := errors.Join(writeErr, file.Sync()); err != nil {
		return err
	}
	written, err := file.Stat()
	if err != nil || !fileOK(written) || !os.SameFile(created, written) ||
		written.Size() != int64(len(raw)+1) || !matches(temp, written) || s.Check() != nil {
		return ErrInput
	}
	if err := s.rename(int(s.directory.Fd()), temp, name); err != nil {
		return err
	}
	if !matches(name, written) {
		return ErrInput
	}
	return errors.Join(s.directory.Sync(), s.Check())
}
