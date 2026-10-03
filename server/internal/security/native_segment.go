package security

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

// nativeSegmentOwner closes the Log before its WAL and lower-bound journal.
// The stable anchor lease belongs to NativeStore and survives segment rotation.
type nativeSegmentOwner struct {
	log *mutationlog.Log
	wal *mutationlog.FileWAL
	tip *mutationlog.FileWALTipJournal
}

func (o *nativeSegmentOwner) Close() error {
	return errors.Join(o.log.Close(), o.wal.Close(), o.tip.Close())
}

func createNativeSegment(path string, binding [32]byte) (_ *mutationlog.Log, _ io.Closer, err error) {
	wal, err := mutationlog.CreateFileWAL(path, encodeSystemRevision)
	if err != nil {
		return nil, nil, err
	}
	var tip *mutationlog.FileWALTipJournal
	complete := false
	defer func() {
		if !complete {
			err = errors.Join(err, wal.Close())
			if tip != nil {
				err = errors.Join(err, tip.Close())
			}
		}
	}()
	tip, err = mutationlog.CreateFileWALTipJournal(path, binding)
	if err != nil {
		return nil, nil, err
	}
	unexpected := func([]byte) (mutationlog.MutationOp, error) { return nil, ErrInvalidRevision }
	if err = tip.VerifyAndCatchUp(path, unexpected, func(mutationlog.Entry) error { return ErrInvalidRevision }); err != nil {
		return nil, nil, err
	}
	if err = wal.BindTipJournal(tip); err != nil {
		return nil, nil, err
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 1, WAL: wal})
	complete = true
	return log, &nativeSegmentOwner{log: log, wal: wal, tip: tip}, nil
}

// cleanupNativeSegments removes only obsolete files owned by this anchor.
// It runs after the selected complete history is proved and before any later
// revision can publish, so a stale selector cannot reopen acknowledged grants.
func cleanupNativeSegments(anchor, selected string) error {
	entries, err := os.ReadDir(filepath.Dir(anchor))
	if err != nil {
		return err
	}
	base := filepath.Base(anchor)
	selectedBase := filepath.Base(selected)
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		owned := name == base || name == base+".tip"
		if suffix, ok := strings.CutPrefix(name, base+".segment-"); ok {
			suffix = strings.TrimSuffix(suffix, ".tip")
			decoded, err := hex.DecodeString(suffix)
			owned = err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == suffix
		}
		if strings.HasPrefix(name, base+".current-tmp-") {
			owned = true
		}
		if !owned || name == selectedBase || name == selectedBase+".tip" {
			continue
		}
		path := filepath.Join(filepath.Dir(anchor), name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrInvalidRevision
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return syncSystemDirectory(filepath.Dir(anchor))
	}
	return nil
}

// Explicit genesis must not reinterpret a missing selector as a fresh database.
// Even an orphan from an interrupted create requires operator recovery review.
func requireFreshNativeFamily(anchor string) error {
	entries, err := os.ReadDir(filepath.Dir(anchor))
	if err != nil {
		return err
	}
	base := filepath.Base(anchor)
	for _, entry := range entries {
		name := entry.Name()
		if name == base || name == base+".tip" || name == base+".current" ||
			strings.HasPrefix(name, base+".segment-") || strings.HasPrefix(name, base+".current-tmp-") {
			return ErrInvalidRevision
		}
	}
	return nil
}
