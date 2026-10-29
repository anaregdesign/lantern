//go:build lantern_bench_heap && darwin

package main

import "golang.org/x/sys/unix"

func benchHeapPublish(fd int, old, new string) error {
	return unix.RenameatxNp(fd, old, fd, new, unix.RENAME_EXCL)
}
