package backup

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/privatefile"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

// RestoreGraphStoreOnStartup recovers only into a new target. A surviving DB
// is authoritative. A missing or damaged newest recovery point never triggers
// a silent rollback to an earlier observed state.
func RestoreGraphStoreOnStartup(ctx context.Context, target string, expected hlc.NodeID, cfg Config) error {
	if !cfg.RestoreOnStart {
		return nil
	}
	stat, err := os.Lstat(target)
	if err == nil {
		if !stat.IsDir() {
			return errors.New("backup: graph storage target is not a directory")
		}
		if cfg.RestoreRequired {
			f, err := os.Open(target)
			if err != nil {
				return err
			}
			entries, readErr := f.ReadDir(1)
			closeErr := f.Close()
			if len(entries) == 0 {
				return errors.New("backup: required restore needs a new target or an existing initialized database")
			}
			if err := errors.Join(readErr, closeErr); err != nil {
				return err
			}
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	b := &Backupper{cfg: cfg, fs: newReceiptBackupFS()}
	points, err := b.graphPoints(ctx)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		if cfg.RestoreRequired {
			return errors.New("backup: no private graph storage recovery point")
		}
		return nil
	}
	return restoreGraphPoint(ctx, filepath.Join(cfg.Dir, points[0].name), target, cfg.InstanceID, points[0].id, cfg.NamespaceFormat, expected, [32]byte{})
}

// RestoreGraphStore restores an explicitly chosen private point into a new
// directory. expectedCut is the SHA-256 of the cut independently retained by
// the operator. Use server startup discovery to select the current catalog tip.
func RestoreGraphStore(ctx context.Context, source, target, instance string, expected hlc.NodeID, expectedCut [32]byte) error {
	idText, ok := strings.CutPrefix(filepath.Base(filepath.Clean(source)), graphPointPrefix(instance))
	id, valid := parseReceiptBackupSetID(idText)
	if !ok || !valid || instance == "" || expectedCut == ([32]byte{}) {
		return errors.New("backup: expected a canonical point, instance and independent cut digest")
	}
	return restoreGraphPoint(ctx, source, target, instance, id, keyspace.Version, expected, expectedCut)
}

func restoreGraphPoint(ctx context.Context, source, target, instance string, id uint64, namespace string, expected hlc.NodeID, expectedCut [32]byte) error {
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("backup: %s: %w", target, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	cut, raw, _, err := loadGraphPoint(ctx, source, instance, id, namespace)
	if err != nil {
		return err
	}
	if expectedCut != ([32]byte{}) && sha256.Sum256(raw) != expectedCut {
		return errors.New("backup: recovery point differs from independently retained cut digest")
	}
	if expected != (hlc.NodeID{}) && cut.Application.NodeID != expected {
		return graphstore.ErrNodeMismatch
	}
	parent := filepath.Dir(filepath.Clean(target))
	stage, err := os.MkdirTemp(parent, ".graph-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	restored := filepath.Join(stage, nativeImage)
	if err := graphstore.Restore(ctx, filepath.Join(source, nativeImage), restored); err != nil {
		return err
	}
	db, err := graphstore.OpenExisting(ctx, restored, cut.Application.NodeID)
	if err != nil {
		return err
	}
	verifyErr := checkRestoredGraph(ctx, db, cut)
	if err = errors.Join(verifyErr, db.Close()); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, err = renameReceiptBackupExclusive(restored, target); err != nil {
		return err
	}
	return privatefile.SyncDirectory(parent)
}

func checkRestoredGraph(ctx context.Context, db *graphstore.DB, cut graphStoreCut) error {
	view, err := db.Read()
	if err != nil {
		return err
	}
	defer view.Close()
	if view.Revision != cut.Revision || view.Generation != cut.Application.Generation || view.Snapshot.Sequence() != cut.StorageSequence {
		return errors.New("backup: restored graph manifest differs from source cut")
	}
	app, err := dataruntime.ReadTarpBackupWitness(db.NodeID(), view.Generation, view.Snapshot, cut.Namespace)
	if err != nil {
		return err
	}
	if app != cut.Application {
		return errors.New("backup: restored application history differs from source cut")
	}
	hash, err := graphstore.StateDigest(ctx, view.Generation, view.Snapshot)
	if err != nil {
		return err
	}
	if hash != cut.Rows {
		return errors.New("backup: restored graph or service rows differ from source cut")
	}
	return nil
}
