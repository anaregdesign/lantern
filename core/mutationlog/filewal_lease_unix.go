//go:build darwin || linux

package mutationlog

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockFileWALLease(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrFileWALLeaseBusy
	}
	if err != nil {
		return fmt.Errorf("mutationlog: lock FileWAL lease: %w", err)
	}
	return nil
}
