package mutationlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrFileWALLeaseBusy means another process owns the path's advisory lock.
var ErrFileWALLeaseBusy = errors.New("mutationlog: FileWAL path is owned by another process")

// ErrFileWALLeaseClosed means the owner released its lock before WithPath.
var ErrFileWALLeaseClosed = errors.New("mutationlog: FileWAL lease is closed")

// FileWALLease holds an advisory process lock for one WAL path. A recovery
// owner must acquire it before the first audit pass and keep it through replay,
// append, and shutdown. Neither ReplayFileWAL nor ResumeFileWAL acquires it
// implicitly, so a caller cannot accidentally release ownership between
// validation passes. All processes using the path must follow this protocol.
//
// The .lease file is intentionally never removed: unlinking it would let a
// second process lock a new inode while the first still owns the old one.
type FileWALLease struct {
	mu       sync.RWMutex
	file     *os.File
	path     string
	closed   bool
	closeErr error
}

// AcquireFileWALLease claims path across processes without reading or changing
// the WAL. A busy path fails immediately. The directory must already exist.
// A successful Close (or process exit) releases the OS lock.
func AcquireFileWALLease(path string) (*FileWALLease, error) {
	if path == "" {
		return nil, errors.New("mutationlog: FileWAL lease path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("mutationlog: resolve FileWAL path: %w", err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, fmt.Errorf("mutationlog: resolve FileWAL directory: %w", err)
	}
	path = filepath.Join(dir, filepath.Base(abs))
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("mutationlog: FileWAL path is a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mutationlog: inspect FileWAL path: %w", err)
	}
	leasePath := path + ".lease"
	if info, err := os.Lstat(leasePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("mutationlog: FileWAL lease is a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mutationlog: inspect FileWAL lease: %w", err)
	}
	file, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mutationlog: open FileWAL lease: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("mutationlog: stat FileWAL lease: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("mutationlog: FileWAL lease is not a regular file")
	}
	if err := lockFileWALLease(file); err != nil {
		return nil, err
	}
	// Detect an inode replacement during acquisition. An operator must not
	// unlink the sidecar while any process may still own its lock.
	current, err := os.Stat(leasePath)
	if err != nil || !os.SameFile(info, current) {
		return nil, errors.New("mutationlog: FileWAL lease path changed during acquisition")
	}
	closeOnError = false
	return &FileWALLease{file: file, path: path}, nil
}

// Path is the canonical WAL path to use for every audit and append while the
// lease is held. It resolves directory aliases and rejects WAL symlinks.
func (l *FileWALLease) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// WithPath holds the in-process lease guard throughout a validation or replay
// pass, so Close cannot release the OS lock partway through it. The callback
// must not call Close on the same lease. A closed lease never calls fn.
func (l *FileWALLease) WithPath(fn func(string) error) error {
	if l == nil || fn == nil {
		return errors.New("mutationlog: FileWAL lease and callback are required")
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return ErrFileWALLeaseClosed
	}
	return fn(l.path)
}

// Close releases ownership. It is safe to call more than once.
func (l *FileWALLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.closeErr
	}
	l.closed = true
	l.closeErr = l.file.Close()
	return l.closeErr
}
