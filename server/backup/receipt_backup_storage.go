package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

type receiptBackupSyncFile interface {
	io.Writer
	Sync() error
	Close() error
}

type receiptBackupFS struct {
	mkdirAll      func(string, os.FileMode) error
	openExclusive func(string, int, os.FileMode) (receiptBackupSyncFile, bool, error)
	remove        func(string) error
	readDir       func(string) ([]os.DirEntry, error)
	readFile      func(string, int64) ([]byte, error)
	lstat         func(string) (os.FileInfo, error)
	syncDir       func(string) error
}

func newReceiptBackupFS() receiptBackupFS {
	return receiptBackupFS{
		mkdirAll: os.MkdirAll,
		openExclusive: func(path string, flag int, perm os.FileMode) (receiptBackupSyncFile, bool, error) {
			file, err := os.OpenFile(path, flag, perm)
			return file, err == nil, err
		},
		remove:  os.Remove,
		readDir: os.ReadDir,
		readFile: func(path string, limit int64) ([]byte, error) {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			raw, readErr := io.ReadAll(io.LimitReader(f, limit+1))
			closeErr := f.Close()
			if readErr != nil || closeErr != nil {
				return nil, errors.Join(readErr, closeErr)
			}
			if int64(len(raw)) > limit {
				return nil, fmt.Errorf("file exceeds %d-byte limit", limit)
			}
			return raw, nil
		},
		lstat: os.Lstat,
		syncDir: func(path string) error {
			dir, err := os.Open(path)
			if err != nil {
				return err
			}
			return errors.Join(dir.Sync(), dir.Close())
		},
	}
}

func (b *Backupper) backupReceiptSetWithSource(ctx context.Context, source string) (Stats, error) {
	start := b.now()
	tickID := strconv.FormatInt(start.UnixNano(), 10)
	b.logger.Info("backup: dump started",
		"source", source,
		"tick_id", tickID,
		"format", receiptBackupSetFormat,
		"started_unix_ns", start.UnixNano())

	product, err := produceReceiptWholeStateArchive(ctx, b.receiptSource, b.receiptPolicy)
	if err != nil {
		b.failed()
		return Stats{}, err
	}
	archiveRaw, walCutRaw := product.bytes()
	if len(archiveRaw) == 0 || len(walCutRaw) == 0 {
		b.failed()
		return Stats{}, errors.New("backup: receipt archive producer returned an empty product")
	}
	if err := ctx.Err(); err != nil {
		b.failed()
		return Stats{}, err
	}
	if err := b.fs.mkdirAll(b.cfg.Dir, receiptBackupSetDirectoryPerms); err != nil {
		b.failed()
		return Stats{}, fmt.Errorf("backup: mkdir %s: %w", b.cfg.Dir, err)
	}
	setID, err := b.nextReceiptBackupSetID(start)
	if err != nil {
		b.failed()
		return Stats{}, err
	}
	manifest, err := newReceiptBackupSetManifest(
		b.cfg.InstanceID,
		setID,
		start,
		product,
		archiveRaw,
		walCutRaw,
	)
	if err != nil {
		b.failed()
		return Stats{}, err
	}
	manifestRaw, err := encodeReceiptBackupSetManifest(manifest)
	if err != nil {
		b.failed()
		return Stats{}, err
	}
	manifestPath, err := b.persistReceiptBackupSet(
		ctx,
		setID,
		product.nodeID,
		product.generation,
		manifest,
		manifestRaw,
		archiveRaw,
		walCutRaw,
	)
	if err != nil {
		b.failed()
		return Stats{}, err
	}

	stats := product.stats
	stats.Members = len(manifest.Members)
	stats.Bytes = int64(len(archiveRaw) + len(walCutRaw) + len(manifestRaw))
	finished := b.now()
	took := finished.Sub(start)
	if took < 0 {
		took = 0
	}
	if b.metrics != nil {
		b.metrics.observeBackup(stats, took, finished)
	}
	b.logger.Info("backup: wrote dump",
		"source", source,
		"tick_id", tickID,
		"format", receiptBackupSetFormat,
		"set_id", manifest.SetID,
		"started_unix_ns", start.UnixNano(),
		"finished_unix_ns", finished.UnixNano(),
		"file", manifestPath,
		"manifest", manifestPath,
		"node_id", manifest.NodeID,
		"generation", manifest.Generation,
		"vertices", stats.Vertices,
		"edges", stats.Edges,
		"receipts", stats.Receipts,
		"origins", stats.Origins,
		"members", stats.Members,
		"bytes", stats.Bytes,
		"took", took)
	if err := b.pruneReceiptBackupSets(); err != nil {
		b.failed()
		return stats, err
	}
	return stats, nil
}

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
		{receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, receiptBackupSetTempFile},
		{receiptBackupSetArchiveSuffix, receiptBackupSetArchiveFile},
		{receiptBackupSetWALCutSuffix, receiptBackupSetWALCutFile},
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

type receiptBackupAttempt struct {
	manifestFinal string
	memberFinals  []string
	owned         map[string]bool
}

// Final names are written directly because O_EXCL is available on supported
// mounted backends while atomic no-replace rename and hard links are not. A set
// commits only when the manifest written last passes full validation.
func (b *Backupper) persistReceiptBackupSet(
	ctx context.Context,
	setID uint64,
	nodeID hlc.NodeID,
	generation [16]byte,
	manifest receiptBackupSetManifest,
	manifestRaw, archiveRaw, walCutRaw []byte,
) (manifestPath string, err error) {
	if manifest.SetID != receiptBackupSetIDString(setID) {
		return "", errors.New("backup: receipt backup-set persistence ID mismatch")
	}
	if err := validateReceiptBackupSetManifestIdentity(manifest, nodeID, generation); err != nil {
		return "", err
	}
	base := receiptBackupSetBase(b.cfg.InstanceID, setID)
	attempt := receiptBackupAttempt{
		manifestFinal: filepath.Join(b.cfg.Dir, base+receiptBackupSetManifestSuffix),
		memberFinals: []string{
			filepath.Join(b.cfg.Dir, manifest.Members[0].Name),
			filepath.Join(b.cfg.Dir, manifest.Members[1].Name),
		},
		owned: make(map[string]bool, 3),
	}
	for _, path := range append(
		append([]string(nil), attempt.memberFinals...),
		attempt.manifestFinal,
	) {
		if _, statErr := b.fs.lstat(path); statErr == nil {
			return "", fmt.Errorf("backup: receipt backup set path already exists: %s", path)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("backup: inspect receipt backup set path %s: %w", path, statErr)
		}
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, b.cleanupReceiptBackupAttempt(attempt))
		}
	}()

	members := [][]byte{archiveRaw, walCutRaw}
	for i, raw := range members {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		created, writeErr := b.writeReceiptBackupExclusive(ctx, attempt.memberFinals[i], raw)
		if created {
			attempt.owned[attempt.memberFinals[i]] = true
		}
		if writeErr != nil {
			return "", fmt.Errorf("backup: write receipt backup member %s: %w", attempt.memberFinals[i], writeErr)
		}
	}
	if syncErr := b.fs.syncDir(b.cfg.Dir); syncErr != nil {
		return "", fmt.Errorf("backup: sync receipt backup directory after members: %w", syncErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}

	created, writeErr := b.writeReceiptBackupExclusive(ctx, attempt.manifestFinal, manifestRaw)
	if created {
		attempt.owned[attempt.manifestFinal] = true
	}
	if writeErr != nil {
		return "", fmt.Errorf("backup: write receipt backup-set manifest %s: %w", attempt.manifestFinal, writeErr)
	}
	if syncErr := b.fs.syncDir(b.cfg.Dir); syncErr != nil {
		return "", fmt.Errorf("backup: sync receipt backup directory after manifest: %w", syncErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	return attempt.manifestFinal, nil
}

func (b *Backupper) writeReceiptBackupExclusive(
	ctx context.Context,
	path string,
	raw []byte,
) (created bool, err error) {
	file, created, err := b.fs.openExclusive(
		path,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		receiptBackupSetFilePermissions,
	)
	if err != nil {
		return created, err
	}
	if !created || file == nil {
		return created, errors.New("backup: exclusive receipt backup create returned no owned file")
	}
	writeErr := writeAllReceiptBackupBytes(file, raw)
	if writeErr == nil {
		writeErr = ctx.Err()
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = ctx.Err()
	}
	return created, errors.Join(writeErr, closeErr)
}

func writeAllReceiptBackupBytes(w io.Writer, raw []byte) error {
	for len(raw) != 0 {
		n, err := w.Write(raw)
		if n < 0 || n > len(raw) {
			return errors.New("backup: receipt backup write returned an invalid byte count")
		}
		raw = raw[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (b *Backupper) cleanupReceiptBackupAttempt(attempt receiptBackupAttempt) error {
	var cleanupErr error
	remove := func(path string) bool {
		if !attempt.owned[path] {
			return false
		}
		if err := b.fs.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("backup: clean attempted receipt backup path %s: %w", path, err))
			return false
		}
		delete(attempt.owned, path)
		return true
	}

	markerRemoved := remove(attempt.manifestFinal)
	if markerRemoved {
		if err := b.fs.syncDir(b.cfg.Dir); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("backup: sync receipt backup directory after marker cleanup: %w", err))
		}
	}
	membersRemoved := false
	for _, path := range attempt.memberFinals {
		membersRemoved = remove(path) || membersRemoved
	}
	if membersRemoved {
		if err := b.fs.syncDir(b.cfg.Dir); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("backup: sync receipt backup directory after member cleanup: %w", err))
		}
	}
	return cleanupErr
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
