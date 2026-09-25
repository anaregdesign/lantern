//go:build !windows

package mutationlog

import (
	"errors"
	"os"
)

func syncFileWALDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
