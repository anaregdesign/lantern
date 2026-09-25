//go:build !windows

package backup

import (
	"errors"
	"os"
)

func syncReceiptBackupDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
