//go:build windows

package mutationreceipt

import (
	"errors"

	"golang.org/x/sys/windows"
)

func syncClockJournalDirectory(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return err
	}
	return errors.Join(windows.FlushFileBuffers(handle), windows.CloseHandle(handle))
}
