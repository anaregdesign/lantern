//go:build linux || darwin

package replication

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"golang.org/x/sys/unix"
)

// Walk from an owned root descriptor, never following a component symlink.
// Root-owned sticky ancestors permit an explicitly configured private child
// under a system temporary directory; they never select such a path implicitly.
func openSnapshotScratchDirectory(path string) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), "Snapshot staging root")
	components := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for i, component := range components {
		if component == "" {
			continue
		}
		nextFD, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("Snapshot staging directory requires a real resolved private path: %w", err), current.Close())
		}
		next := os.NewFile(uintptr(nextFD), "Snapshot staging directory")
		closeErr := current.Close()
		current = next
		var stat unix.Stat_t
		if err := unix.Fstat(int(current.Fd()), &stat); err != nil {
			return nil, errors.Join(err, current.Close(), closeErr)
		}
		final := i == len(components)-1
		owned := stat.Uid == uint32(os.Geteuid())
		trusted := owned || stat.Uid == 0
		writable := stat.Mode&0022 != 0
		stickyRoot := stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0
		if closeErr != nil || !trusted || final && (!owned || stat.Mode&07777 != 0700) || !final && writable && !stickyRoot {
			return nil, errors.Join(errors.New("Snapshot staging directory is not private or has an untrusted ancestor"), closeErr, current.Close())
		}
	}
	// A root path is never an owner-private staging directory.
	if filepath.Clean(path) == "/" {
		return nil, errors.Join(errors.New("Snapshot staging directory cannot be the filesystem root"), current.Close())
	}
	return current, nil
}

func checkSnapshotScratchRegular(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	return checkSnapshotScratchStat(stat)
}

func checkSnapshotScratchStat(stat unix.Stat_t) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("Snapshot scratch artifact is not an owned private single-link regular file")
	}
	return nil
}

func checkSnapshotScratchName(dir, file *os.File, name string) error {
	var opened, named unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &opened); err != nil {
		return err
	}
	if err := unix.Fstatat(int(dir.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if opened.Dev != named.Dev || opened.Ino != named.Ino {
		return errors.New("Snapshot scratch artifact identity changed")
	}
	return nil
}

func lockSnapshotScratchOwner(dir *os.File) (*os.File, error) {
	var prior unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), snapshotScratchOwnerName, &prior, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if err := checkSnapshotScratchStat(prior); err != nil {
			return nil, err
		}
		if prior.Size != 0 {
			return nil, errors.New("Snapshot staging owner file is not an empty reserved lease")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return nil, err
	}
	fd, err := unix.Openat(int(dir.Fd()), snapshotScratchOwnerName, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "Snapshot staging owner")
	if err := checkSnapshotScratchRegular(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if info, err := file.Stat(); err != nil || info.Size() != 0 {
		return nil, errors.Join(errors.New("Snapshot staging owner file is not an empty reserved lease"), err, file.Close())
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("Snapshot scratch directory is already owned"))
		}
		return nil, err
	}
	if err := checkSnapshotScratchName(dir, file, snapshotScratchOwnerName); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	// This pathname is stable across owners. Never unlink it on Close.
	return file, nil
}

func snapshotScratchSlotNumber(name string) (int, bool) {
	const prefix = ".lantern-snapshot-slot-"
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+3 {
		return 0, false
	}
	n, err := strconv.Atoi(name[len(prefix):])
	return n, err == nil && n >= 0 && n < snapshotScratchSlots && name == snapshotScratchSlotName(n)
}

func recoverSnapshotScratchSlots(dir *os.File) error {
	var present [snapshotScratchSlots]bool
	// Read bounded batches; unknown content is rejected before deleting any slot.
	for {
		entries, err := dir.ReadDir(16)
		for _, entry := range entries {
			if entry.Name() == snapshotScratchOwnerName {
				continue
			}
			slot, valid := snapshotScratchSlotNumber(entry.Name())
			if !valid {
				return errors.New("Snapshot staging directory contains an unowned artifact")
			}
			present[slot] = true
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	for slot, exists := range present {
		if !exists {
			continue
		}
		name := snapshotScratchSlotName(slot)
		var prior unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), name, &prior, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if err := checkSnapshotScratchStat(prior); err != nil {
			return err
		}
		// A legitimate creation-to-unlink crash can leave only an empty slot:
		// all payload writes happen after its pathname has been removed.
		if prior.Size != 0 {
			return errors.New("Snapshot staging reserved orphan contains unowned payload")
		}
		fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(fd), "orphaned Snapshot scratch slot")
		if err := checkSnapshotScratchRegular(file); err != nil {
			return errors.Join(err, file.Close())
		}
		if info, err := file.Stat(); err != nil || info.Size() != 0 {
			return errors.Join(errors.New("Snapshot staging reserved orphan is not empty"), err, file.Close())
		}
		if err := checkSnapshotScratchName(dir, file, name); err != nil {
			return errors.Join(err, file.Close())
		}
		err = unix.Unlinkat(int(dir.Fd()), name, 0)
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
	}
	return nil
}

func createSnapshotScratchSlot(dir *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "Snapshot scratch slot")
	if err := checkSnapshotScratchRegular(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := checkSnapshotScratchName(dir, file, name); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
