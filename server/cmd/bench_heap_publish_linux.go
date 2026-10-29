//go:build lantern_bench_heap && linux

package main

import "golang.org/x/sys/unix"

func benchHeapPublish(fd int, old, new string) error {
	return unix.Renameat2(fd, old, fd, new, unix.RENAME_NOREPLACE)
}
