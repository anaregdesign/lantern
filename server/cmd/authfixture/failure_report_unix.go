//go:build unix

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// The shell gives the supervisor sole write access to an unlinked report.
// Mark its descriptor before provisioning or launching any child process.
func openFixtureFailureReport() (*os.File, error) {
	const fd = 3
	if _, err := unix.FcntlInt(fd, unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		return nil, err
	}
	return os.NewFile(fd, "fixture-failure"), nil
}
