//go:build linux || darwin

package main

import (
	"os"
	"syscall"
)

const retainedEvidenceSupported = true

func openRetainedEvidence(path string) (*os.File, error) {
	return openRetainedEvidenceWith(path, os.OpenFile)
}

func openRetainedEvidenceWith(path string, open func(string, int, os.FileMode) (*os.File, error)) (*os.File, error) {
	return open(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func retainedEvidenceMetadataOK(info os.FileInfo) bool {
	metadata, ok := info.Sys().(*syscall.Stat_t)
	return ok && metadata.Uid == uint32(os.Getuid()) && metadata.Nlink == 1
}

func retainedEvidenceMetadataEqual(before, after os.FileInfo) bool {
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	return aok && bok && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink
}
