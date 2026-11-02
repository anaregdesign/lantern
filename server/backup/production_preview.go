package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	productionPreviewMaxBytes      = 64 << 20
	productionPreviewMaxCandidates = 1024
)

// ErrRetentionPreviewLimit means the connected node cannot provide a complete
// retention preview within its fixed read/validation bounds.
var ErrRetentionPreviewLimit = errors.New("backup: retention preview limit exceeded")

// RetentionPreview describes only this connected producer's valid recovery
// points. It contains no paths or contents and is not a cluster-wide inventory.
// Counts may be partial unless Complete is true. Fingerprint binds the observed
// valid catalog; it does not freeze external filesystem writers.
type RetentionPreview struct {
	Generation            uint64
	Retain                int
	ValidBackups          int
	ValidBytes            int64
	PruneBackups          int
	PruneBytes            int64
	AcknowledgementNeeded bool
	Complete              bool
	Fingerprint           string
}

type retentionPreviewCandidate struct {
	name   string
	stamp  uint64
	size   int64
	digest [sha256.Size]byte
}

// PreviewRetention validates owned candidates without modifying the directory.
// Reduction review must reject an incomplete preview. Keep-all adoption need
// not request a preview and therefore remains possible for a large catalog.
func (b *Backupper) PreviewRetention(ctx context.Context, retain int) (RetentionPreview, error) {
	return b.previewRetention(ctx, retain, productionPreviewMaxBytes, productionPreviewMaxCandidates)
}

func (b *Backupper) previewRetention(ctx context.Context, retain int, maxBytes int64, maxCandidates int) (preview RetentionPreview, err error) {
	select {
	case <-ctx.Done():
		return preview, ctx.Err()
	case b.attempt <- struct{}{}:
	}
	defer func() { <-b.attempt }()
	preview, _, err = b.retentionCatalog(ctx, retain, maxBytes, maxCandidates)
	return preview, err
}

// retentionCatalog requires the existing attempt guard. Production uses the
// same bounded validation as review, without recursively taking that guard.
func (b *Backupper) retentionCatalog(ctx context.Context, retain int, maxBytes int64, maxCandidates int) (preview RetentionPreview, candidates []retentionPreviewCandidate, err error) {
	if err := ctx.Err(); err != nil {
		return preview, nil, err
	}
	current := b.Production()
	next := current.Config
	next.Retain = retain
	preview = RetentionPreview{
		Generation: current.Generation, Retain: retain,
		AcknowledgementNeeded: RetentionAcknowledgementRequired(current, next),
	}
	if retain < 0 {
		return preview, nil, errors.New("backup: retention preview retain is outside supported bounds")
	}
	if strings.TrimSpace(b.cfg.Dir) == "" {
		preview.Complete = true
		preview.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(nil))
		return preview, nil, nil
	}
	remaining := maxBytes
	readDigest := sha256.New()
	// Construct a read-only validator view explicitly: copying Backupper would
	// copy its mutex and accidentally alias scheduler/attempt ownership.
	validator := &Backupper{cfg: b.cfg, fs: b.fs}
	validator.fs.readFile = func(path string, limit int64) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, statErr := b.fs.lstat(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Size() < 0 || info.Size() >= remaining {
			return nil, ErrRetentionPreviewLimit
		}
		readLimit := min(limit, remaining-1)
		raw, readErr := b.fs.readFile(path, readLimit)
		if readErr != nil {
			// The filesystem seam may return no partial bytes on failure;
			// conservatively charge the entire bounded attempted read.
			remaining -= readLimit + 1
			var pathError *os.PathError
			if errors.As(readErr, &pathError) {
				return nil, readErr
			}
			if readLimit < limit {
				return nil, ErrRetentionPreviewLimit
			}
			return nil, readErr
		}
		remaining -= int64(len(raw))
		_, _ = readDigest.Write(raw)
		return raw, readErr
	}
	validated := 0
	visited := false
	err = validator.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		visited = true
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if b.receiptSource != nil {
			id, _, kind, own := parseOwnReceiptBackupSetName(name, b.cfg.InstanceID)
			if !own || kind != receiptBackupSetManifestFile {
				return nil
			}
			validated++
			if validated > maxCandidates {
				return ErrRetentionPreviewLimit
			}
			readDigest.Reset()
			loaded, loadErr := validator.loadReceiptBackupSet(filepath.Join(b.cfg.Dir, name))
			if loadErr != nil {
				return previewCandidateError(loadErr)
			}
			if loaded.id != id {
				return nil
			}
			var digest [sha256.Size]byte
			copy(digest[:], readDigest.Sum(nil))
			candidates = append(candidates, retentionPreviewCandidate{name: name, stamp: id, size: loaded.stats.Bytes, digest: digest})
			return nil
		}
		stamp, canonical := fileStamp(name)
		if !strings.HasPrefix(name, b.ownPrefix()) || !strings.HasSuffix(name, fileSuffix) || !canonical || stamp <= 0 || b.fileName(time.Unix(0, stamp)) != name {
			return nil
		}
		validated++
		if validated > maxCandidates {
			return ErrRetentionPreviewLimit
		}
		path := filepath.Join(b.cfg.Dir, name)
		info, statErr := b.fs.lstat(path)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		raw, readErr := validator.fs.readFile(path, maxBytes)
		if readErr != nil {
			return readErr
		}
		if decodeErr := scanGraph(ctx, bufio.NewReader(bytes.NewReader(raw)), b.cfg.NamespaceFormat, nil); decodeErr != nil {
			return previewCandidateError(decodeErr)
		}
		candidates = append(candidates, retentionPreviewCandidate{name: name, stamp: uint64(stamp), size: int64(len(raw)), digest: sha256.Sum256(raw)})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) && !visited {
		err = nil
	}
	if errors.Is(err, errReceiptBackupDirectoryLimit) {
		err = ErrRetentionPreviewLimit
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].stamp > candidates[j].stamp })
	hash := sha256.New()
	for i, candidate := range candidates {
		preview.ValidBackups++
		preview.ValidBytes += candidate.size
		if retain > 0 && i >= retain {
			preview.PruneBackups++
			preview.PruneBytes += candidate.size
		}
		_, _ = fmt.Fprintf(hash, "%d:%s:%d:%x\n", candidate.stamp, candidate.name, candidate.size, candidate.digest)
	}
	preview.Complete = err == nil
	if preview.Complete {
		preview.Fingerprint = fmt.Sprintf("%x", hash.Sum(nil))
	}
	return preview, candidates, err
}

func previewCandidateError(err error) error {
	var pathError *os.PathError
	if errors.Is(err, ErrRetentionPreviewLimit) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.As(err, &pathError) || errors.Is(err, io.ErrNoProgress) {
		return err
	}
	// Malformed/unsupported contents are neither counted nor prune candidates.
	return nil
}
