package backup

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	validated := 0
	visited := false
	err = b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		visited = true
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.graphStore == nil {
			return ErrProductionUnavailable
		}
		prefix := graphPointPrefix(b.cfg.InstanceID)
		suffix, own := strings.CutPrefix(entry.Name(), prefix)
		id, valid := parseGraphPointID(suffix)
		if !own || !valid || !entry.IsDir() {
			return nil
		}
		validated++
		if validated > maxCandidates {
			return ErrRetentionPreviewLimit
		}
		path := filepath.Join(b.cfg.Dir, entry.Name())
		size, err := graphPointBytes(ctx, path)
		if err != nil {
			return err
		}
		if size > remaining {
			return ErrRetentionPreviewLimit
		}
		remaining -= size
		cut, raw, _, err := loadGraphPoint(ctx, path, b.cfg.InstanceID, id, b.cfg.NamespaceFormat)
		if err != nil {
			return previewCandidateError(err)
		}
		if cut.Application.NodeID != b.graphStore.NodeID() {
			return nil
		}
		candidates = append(candidates, retentionPreviewCandidate{name: entry.Name(), stamp: id, size: size, digest: sha256.Sum256(raw)})
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
