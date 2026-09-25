package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	receiptBackupDirectoryReadBatch  = 128
	receiptBackupDirectoryMaxEntries = 100_000
)

var errReceiptBackupDirectoryLimit = errors.New("backup: receipt backup directory entry limit exceeded")

func (b *Backupper) ensureReceiptBackupDirectory() error {
	components, err := receiptBackupDirectoryComponents(b.cfg.Dir)
	if err != nil {
		return err
	}
	var missing []string
	var nearestExisting string
	for _, path := range components {
		info, err := b.fs.lstat(path)
		switch {
		case err == nil:
			if !info.IsDir() {
				return fmt.Errorf("backup: receipt backup directory component %s is not a directory", path)
			}
			if len(missing) != 0 {
				return fmt.Errorf("backup: receipt backup directory ancestry changed while inspecting %s", b.cfg.Dir)
			}
			nearestExisting = path
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("backup: inspect receipt backup directory component %s: %w", path, err)
		default:
			missing = append(missing, path)
		}
	}
	if nearestExisting == "" {
		return fmt.Errorf("backup: receipt backup directory has no existing ancestor: %s", b.cfg.Dir)
	}

	nearestParent := filepath.Dir(nearestExisting)
	if nearestParent != nearestExisting {
		if err := b.fs.syncDir(nearestParent); err != nil {
			return fmt.Errorf(
				"backup: sync receipt backup directory parent %s before publishing through %s: %w",
				nearestParent,
				nearestExisting,
				err,
			)
		}
	}
	for _, path := range missing {
		parent := filepath.Dir(path)
		if err := b.fs.mkdir(path, receiptBackupSetDirectoryPerms); err != nil &&
			!errors.Is(err, os.ErrExist) {
			return fmt.Errorf("backup: create receipt backup directory %s: %w", path, err)
		}
		info, err := b.fs.lstat(path)
		if err != nil {
			return fmt.Errorf("backup: inspect created receipt backup directory %s: %w", path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("backup: receipt backup directory component %s is not a directory", path)
		}
		if err := b.fs.syncDir(parent); err != nil {
			return fmt.Errorf(
				"backup: sync receipt backup directory parent %s after creating %s: %w",
				parent,
				path,
				err,
			)
		}
	}
	return nil
}

func receiptBackupDirectoryComponents(target string) ([]string, error) {
	absolute, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return nil, fmt.Errorf("backup: resolve receipt backup directory %s: %w", target, err)
	}
	volume := filepath.VolumeName(absolute)
	root := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(absolute, root)
	if relative == "" {
		return []string{root}, nil
	}
	parts := strings.Split(relative, string(filepath.Separator))
	first := filepath.Join(root, parts[0])
	firstInfo, err := os.Lstat(first)
	anchor := first
	switch {
	case err == nil:
		if firstInfo.Mode()&os.ModeSymlink != 0 {
			anchor, err = filepath.EvalSymlinks(first)
			if err != nil {
				return nil, fmt.Errorf("backup: resolve receipt backup directory anchor %s: %w", first, err)
			}
			if !isTrustedReceiptBackupDirectoryRootAlias(first, anchor) {
				return nil, fmt.Errorf(
					"backup: receipt backup directory component %s is not a directory",
					first,
				)
			}
		} else if !firstInfo.IsDir() {
			return nil, fmt.Errorf("backup: receipt backup directory component %s is not a directory", first)
		}
		parts = parts[1:]
	case errors.Is(err, os.ErrNotExist):
		anchor = root
	default:
		return nil, fmt.Errorf("backup: resolve receipt backup directory anchor %s: %w", first, err)
	}
	components := []string{anchor}
	current := anchor
	for _, part := range parts {
		current = filepath.Join(current, part)
		components = append(components, current)
	}
	return components, nil
}

func isTrustedReceiptBackupDirectoryRootAlias(path, target string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	name := filepath.Base(path)
	switch name {
	case "etc", "tmp", "var":
		return filepath.Clean(target) == filepath.Join(string(filepath.Separator), "private", name)
	default:
		return false
	}
}

func (b *Backupper) scanReceiptBackupDirectory(visit func(os.DirEntry) error) (err error) {
	dir, err := b.fs.openDir(b.cfg.Dir)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, dir.Close())
	}()

	scanned := 0
	for {
		entries, readErr := dir.ReadDir(receiptBackupDirectoryReadBatch)
		for _, entry := range entries {
			if scanned == receiptBackupDirectoryMaxEntries {
				return errReceiptBackupDirectoryLimit
			}
			scanned++
			if err := visit(entry); err != nil {
				return err
			}
		}
		switch {
		case errors.Is(readErr, io.EOF):
			return nil
		case readErr != nil:
			return readErr
		case len(entries) == 0:
			return io.ErrNoProgress
		}
	}
}
