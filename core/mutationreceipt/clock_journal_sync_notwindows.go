//go:build !windows

package mutationreceipt

import (
	"errors"
	"os"
)

func syncClockJournalDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
