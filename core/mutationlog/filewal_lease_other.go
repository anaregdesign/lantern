//go:build !darwin && !linux && !windows

package mutationlog

import (
	"errors"
	"os"
)

func lockFileWALLease(*os.File) error {
	return errors.New("mutationlog: FileWAL lease is unsupported on this platform")
}
