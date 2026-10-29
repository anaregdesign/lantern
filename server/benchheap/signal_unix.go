//go:build linux || darwin

package benchheap

import (
	"os"
	"syscall"
)

func Signal() (os.Signal, error) { return syscall.SIGUSR1, nil }
