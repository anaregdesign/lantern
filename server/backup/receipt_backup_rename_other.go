//go:build !darwin && !linux

package backup

import "os"

func renameReceiptBackupExclusive(oldPath, newPath string) (bool, error) {
	// A same-directory hard link is the portable atomic no-replace operation.
	// Removing the staging name then completes the rename without a race.
	if err := os.Link(oldPath, newPath); err != nil {
		return false, err
	}
	if err := os.Remove(oldPath); err != nil {
		return true, err
	}
	return true, nil
}
