package membershippublisher

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/privatefile"
)

// Paths are pinned by name, not inode: a publisher replaces complete files.
func canonical(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	return err == nil && dir == filepath.Dir(path)
}

func readPrivate(path string, limit int64) ([]byte, error) {
	if !canonical(path) {
		return nil, ErrState
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, ErrState
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrState
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || privatefile.Check(f) != nil {
		return nil, ErrState
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, ErrState
	}
	return raw, nil
}

type durableFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}
type fileOps struct {
	temp      func(string, string) (durableFile, error)
	rename    func(string, string) error
	directory func(string) error
	retained  func(string) error
}

func systemFiles() fileOps {
	return fileOps{
		temp:   func(dir, prefix string) (durableFile, error) { return privatefile.CreateTemp(dir, prefix) },
		rename: os.Rename, directory: privatefile.SyncDirectory, retained: syncRetained,
	}
}

// No caller may count publication as durable before the final directory sync.
func (f fileOps) replace(path string, raw []byte) (err error) {
	if !canonical(path) {
		return ErrState
	}
	dir := filepath.Dir(path)
	tmp, err := f.temp(dir, ".membership-")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, tmp.Close())
		}
		_ = os.Remove(tmp.Name())
	}()
	n, err := tmp.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	closed = true
	if err = tmp.Close(); err != nil {
		return err
	}
	// Recheck parent aliases immediately before the only namespace mutation.
	if !canonical(path) {
		return ErrState
	}
	if err = f.rename(tmp.Name(), path); err != nil {
		return err
	}
	return f.directory(dir)
}

// Resume can finish a previously ambiguous file/directory flush, but only
// after verifying the entire retained record. It never reconstructs lost bytes.
func syncRetained(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return ErrState
	}
	defer func() { _ = f.Close() }()
	if privatefile.Check(f) != nil {
		return ErrState
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return privatefile.SyncDirectory(filepath.Dir(path))
}

// Immutable material may be shared between streams, but configs must be
// distinct from every other role and each mutable path must be unaliased.
func safePaths(configs, inputs []string, state string, targets []string) bool {
	alias := func(a, b string) bool {
		if a == b {
			return true
		}
		x, e1 := os.Stat(a)
		y, e2 := os.Stat(b)
		return e1 == nil && e2 == nil && os.SameFile(x, y)
	}
	for i, path := range configs {
		for j, other := range inputs {
			if i != j && alias(path, other) {
				return false
			}
		}
	}
	writable := []string{state, state + ".lease"}
	for _, target := range targets {
		writable = append(writable, target, target+".publisher.lease")
	}
	for i, path := range writable {
		if !canonical(path) {
			return false
		}
		for _, other := range append(append([]string{}, inputs...), writable[:i]...) {
			if alias(path, other) {
				return false
			}
		}
	}
	return true
}
