package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *Backupper) nextReceiptBackupSetID(at time.Time) (uint64, error) {
	entries, err := b.fs.readDir(b.cfg.Dir)
	if err != nil {
		return 0, fmt.Errorf("backup: read receipt backup directory %s: %w", b.cfg.Dir, err)
	}
	var maxID uint64
	for _, entry := range entries {
		id, _, _, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if ok && id > maxID {
			maxID = id
		}
	}
	if b.lastSetID > maxID {
		maxID = b.lastSetID
	}
	var candidate uint64 = 1
	if at.UnixNano() > 0 {
		candidate = uint64(at.UnixNano())
	}
	if candidate <= maxID {
		if maxID == ^uint64(0) {
			return 0, errors.New("backup: receipt backup set ID exhausted")
		}
		candidate = maxID + 1
	}
	b.lastSetID = candidate
	return candidate, nil
}

type receiptBackupSetFileKind uint8

const (
	receiptBackupSetUnknownFile receiptBackupSetFileKind = iota
	receiptBackupSetArchiveFile
	receiptBackupSetWALCutFile
	receiptBackupSetRetiredCatalogFile
	receiptBackupSetManifestFile
	receiptBackupSetTempFile
)

func parseOwnReceiptBackupSetName(
	name, instance string,
) (id uint64, base string, kind receiptBackupSetFileKind, ok bool) {
	prefix := receiptBackupSetPrefix + receiptBackupSetScope(instance) + "-"
	if !strings.HasPrefix(name, prefix) {
		return 0, "", receiptBackupSetUnknownFile, false
	}
	suffixes := []struct {
		suffix string
		kind   receiptBackupSetFileKind
	}{
		{receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix, receiptBackupSetTempFile},
		{receiptBackupSetWALCutSuffix + receiptBackupSetTempSuffix, receiptBackupSetTempFile},
		{receiptBackupSetRetiredCatalogSuffix + receiptBackupSetTempSuffix, receiptBackupSetTempFile},
		{receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, receiptBackupSetTempFile},
		{receiptBackupSetArchiveSuffix, receiptBackupSetArchiveFile},
		{receiptBackupSetWALCutSuffix, receiptBackupSetWALCutFile},
		{receiptBackupSetRetiredCatalogSuffix, receiptBackupSetRetiredCatalogFile},
		{receiptBackupSetManifestSuffix, receiptBackupSetManifestFile},
	}
	for _, candidate := range suffixes {
		if !strings.HasSuffix(name, candidate.suffix) {
			continue
		}
		idText := strings.TrimSuffix(strings.TrimPrefix(name, prefix), candidate.suffix)
		if len(idText) != receiptBackupSetCanonicalIDDigits {
			return 0, "", receiptBackupSetUnknownFile, false
		}
		parsed, err := strconv.ParseUint(idText, 10, 64)
		if err != nil || parsed == 0 || receiptBackupSetIDString(parsed) != idText {
			return 0, "", receiptBackupSetUnknownFile, false
		}
		return parsed, prefix + idText, candidate.kind, true
	}
	return 0, "", receiptBackupSetUnknownFile, false
}

func (b *Backupper) collectReceiptBackupSets() ([]receiptBackupSet, error) {
	entries, err := b.fs.readDir(b.cfg.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: read receipt backup directory %s: %w", b.cfg.Dir, err)
	}
	var sets []receiptBackupSet
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, _, kind, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if !ok || kind != receiptBackupSetManifestFile {
			continue
		}
		path := filepath.Join(b.cfg.Dir, entry.Name())
		loaded, loadErr := b.loadReceiptBackupSet(path)
		if loadErr != nil {
			b.logger.Warn("backup: ignoring invalid receipt backup set",
				"manifest", path,
				"err", loadErr)
			continue
		}
		if loaded.id != id {
			b.logger.Warn("backup: ignoring receipt backup set with mismatched ID",
				"manifest", path,
				"filename_id", id,
				"manifest_id", loaded.id)
			continue
		}
		sets = append(sets, loaded.receiptBackupSet)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].id > sets[j].id })
	return sets, nil
}

func (b *Backupper) pruneReceiptBackupSets() error {
	if b.cfg.Retain <= 0 {
		return nil
	}
	sets, err := b.collectReceiptBackupSets()
	if err != nil || len(sets) <= b.cfg.Retain {
		return err
	}
	for _, set := range sets[b.cfg.Retain:] {
		if err := b.fs.remove(set.manifestPath); err != nil {
			return fmt.Errorf("backup: prune receipt backup-set manifest %s: %w", set.manifestPath, err)
		}
		if err := b.fs.syncDir(b.cfg.Dir); err != nil {
			return fmt.Errorf("backup: sync receipt backup directory after pruning manifest %s: %w", set.manifestPath, err)
		}
		var memberErr error
		for _, path := range set.memberPaths {
			if err := b.fs.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				memberErr = errors.Join(memberErr, fmt.Errorf("backup: prune receipt backup-set member %s: %w", path, err))
			}
		}
		if err := b.fs.syncDir(b.cfg.Dir); err != nil {
			memberErr = errors.Join(memberErr, fmt.Errorf("backup: sync receipt backup directory after pruning members: %w", err))
		}
		if memberErr != nil {
			return memberErr
		}
	}
	return nil
}
