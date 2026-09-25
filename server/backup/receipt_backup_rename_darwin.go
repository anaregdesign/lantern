//go:build darwin

package backup

import "golang.org/x/sys/unix"

func renameReceiptBackupExclusive(oldPath, newPath string) (bool, error) {
	if err := unix.RenameatxNp(
		unix.AT_FDCWD,
		oldPath,
		unix.AT_FDCWD,
		newPath,
		unix.RENAME_EXCL,
	); err != nil {
		return false, err
	}
	return true, nil
}
