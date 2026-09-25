package backup

import (
	"container/heap"
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
	var maxID uint64
	err := b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		id, _, _, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if ok && id > maxID {
			maxID = id
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("backup: scan receipt backup directory %s for next ID: %w", b.cfg.Dir, err)
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
	receiptBackupSetUnsupportedManifestFile
	receiptBackupSetTempFile
)

func parseOwnReceiptBackupSetName(
	name, instance string,
) (id uint64, base string, kind receiptBackupSetFileKind, ok bool) {
	scope := receiptBackupSetScope(instance)
	for _, obsolete := range receiptBackupSetUnsupportedPrefixes {
		prefix := obsolete + scope + "-"
		if strings.HasPrefix(name, prefix) &&
			strings.HasSuffix(name, receiptBackupSetManifestSuffix) {
			id, ok := parseReceiptBackupSetID(
				strings.TrimSuffix(strings.TrimPrefix(name, prefix), receiptBackupSetManifestSuffix),
			)
			if !ok {
				return 0, "", receiptBackupSetUnknownFile, false
			}
			return id, prefix + receiptBackupSetIDString(id), receiptBackupSetUnsupportedManifestFile, true
		}
	}

	prefix := receiptBackupSetPrefix + scope + "-"
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
		parsed, ok := parseReceiptBackupSetID(idText)
		if !ok {
			return 0, "", receiptBackupSetUnknownFile, false
		}
		return parsed, prefix + idText, candidate.kind, true
	}
	return 0, "", receiptBackupSetUnknownFile, false
}

func parseReceiptBackupSetID(value string) (uint64, bool) {
	if len(value) != receiptBackupSetCanonicalIDDigits {
		return 0, false
	}
	id, err := strconv.ParseUint(value, 10, 64)
	return id, err == nil && id != 0 && receiptBackupSetIDString(id) == value
}

func (b *Backupper) collectReceiptBackupSets() ([]receiptBackupSet, error) {
	var sets []receiptBackupSet
	err := b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		id, _, kind, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if !ok || kind != receiptBackupSetManifestFile {
			return nil
		}
		path := filepath.Join(b.cfg.Dir, entry.Name())
		loaded, loadErr := b.loadReceiptBackupSet(path)
		if loadErr != nil {
			b.logger.Warn("backup: ignoring invalid receipt backup set",
				"manifest", path,
				"err", loadErr)
			return nil
		}
		if loaded.id != id {
			b.logger.Warn("backup: ignoring receipt backup set with mismatched ID",
				"manifest", path,
				"filename_id", id,
				"manifest_id", loaded.id)
			return nil
		}
		sets = append(sets, loaded.receiptBackupSet)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: scan receipt backup directory %s: %w", b.cfg.Dir, err)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].id > sets[j].id })
	return sets, nil
}

func (b *Backupper) pruneReceiptBackupSets() error {
	if b.cfg.Retain <= 0 || b.cfg.Retain >= receiptBackupDirectoryMaxEntries {
		return nil
	}
	newest := make(receiptBackupSetMinHeap, 0, b.cfg.Retain)
	err := b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		id, _, kind, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if !ok || kind != receiptBackupSetManifestFile {
			return nil
		}
		set, ok := b.validReceiptBackupSet(entry.Name(), id)
		if !ok {
			return nil
		}
		if len(newest) < b.cfg.Retain {
			heap.Push(&newest, set)
		} else if set.id > newest[0].id {
			heap.Pop(&newest)
			heap.Push(&newest, set)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("backup: scan receipt backup directory %s for retention: %w", b.cfg.Dir, err)
	}
	if len(newest) < b.cfg.Retain {
		return nil
	}
	keepFromID := newest[0].id
	var candidates []receiptBackupSet
	err = b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		id, _, kind, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if !ok || kind != receiptBackupSetManifestFile || id >= keepFromID {
			return nil
		}
		set, valid := b.validReceiptBackupSet(entry.Name(), id)
		if !valid {
			return nil
		}
		candidates = append(candidates, set)
		return nil
	})
	if err != nil {
		return fmt.Errorf("backup: scan receipt backup directory %s while pruning: %w", b.cfg.Dir, err)
	}
	for _, candidate := range candidates {
		name := filepath.Base(candidate.manifestPath)
		set, valid := b.validReceiptBackupSet(name, candidate.id)
		if !valid {
			continue
		}
		if err := b.pruneReceiptBackupSet(set); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backupper) validReceiptBackupSet(name string, id uint64) (receiptBackupSet, bool) {
	path := filepath.Join(b.cfg.Dir, name)
	loaded, err := b.loadReceiptBackupSet(path)
	if err != nil {
		b.logger.Warn("backup: ignoring invalid receipt backup set",
			"manifest", path,
			"err", err)
		return receiptBackupSet{}, false
	}
	if loaded.id != id {
		b.logger.Warn("backup: ignoring receipt backup set with mismatched ID",
			"manifest", path,
			"filename_id", id,
			"manifest_id", loaded.id)
		return receiptBackupSet{}, false
	}
	return loaded.receiptBackupSet, true
}

func (b *Backupper) pruneReceiptBackupSet(set receiptBackupSet) error {
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
	return memberErr
}

type receiptBackupSetMinHeap []receiptBackupSet

func (h receiptBackupSetMinHeap) Len() int           { return len(h) }
func (h receiptBackupSetMinHeap) Less(i, j int) bool { return h[i].id < h[j].id }
func (h receiptBackupSetMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *receiptBackupSetMinHeap) Push(value any) {
	*h = append(*h, value.(receiptBackupSet))
}
func (h *receiptBackupSetMinHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}
