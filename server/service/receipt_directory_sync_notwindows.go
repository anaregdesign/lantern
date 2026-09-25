//go:build !windows

package service

import (
	"errors"
	"os"
)

func syncReceiptDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
