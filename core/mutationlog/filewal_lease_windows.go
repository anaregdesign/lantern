//go:build windows

package mutationlog

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockFileWALLease(file *os.File) error {
	err := windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrFileWALLeaseBusy
	}
	if err != nil {
		return fmt.Errorf("mutationlog: lock FileWAL lease: %w", err)
	}
	return nil
}
