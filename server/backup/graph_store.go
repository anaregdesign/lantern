package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/privatefile"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/tarp"
	"github.com/prometheus/client_golang/prometheus"
)

const privateGraphBackupFormat = "lantern-graph-store-1"
const cutFile = "cut.json"
const nativeImage = "image"

type graphStoreCut struct {
	Format          string
	Instance        string
	ID              uint64
	Namespace       string
	DatabaseID      [16]byte
	StorageSequence uint64
	Revision        uint64
	Application     dataruntime.TarpBackupWitness
	Rows            [32]byte
}

// Seal the application witness as well as the native image so that
// retention cannot count a corrupted cut toward its minimum kept points.
func marshalGraphCut(cut graphStoreCut) ([]byte, error) {
	raw, err := json.Marshal(cut)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		graphStoreCut
		CutChecksum [sha256.Size]byte
	}{cut, sha256.Sum256(raw)})
}

// NewGraphStore uses Tarp's native backup for this certified node's periodic,
// manual and shutdown backups. Public BackupSnapshot remains a graph export.
func NewGraphStore(svc Service, db *graphstore.DB, cfg Config, reg prometheus.Registerer, logger *slog.Logger) *Backupper {
	b := New(svc, cfg, reg, logger)
	b.graphStore = db
	return b
}

func graphPointPrefix(instance string) string {
	return "lantern-tarp-" + receiptBackupSetScope(instance) + "-"
}

type graphPoint struct {
	id   uint64
	name string
}

func (b *Backupper) graphPoints(ctx context.Context) ([]graphPoint, error) {
	prefix := graphPointPrefix(b.cfg.InstanceID)
	var points []graphPoint
	err := b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		suffix, ok := strings.CutPrefix(entry.Name(), prefix)
		if ok {
			if id, ok := parseReceiptBackupSetID(suffix); ok {
				points = append(points, graphPoint{id, entry.Name()})
			}
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(points, func(i, j int) bool { return points[i].id > points[j].id })
	return points, nil
}

func (b *Backupper) backupGraphStore(ctx context.Context, source string, policy ProductionSnapshot) (stats Stats, err error) {
	defer func() {
		if err != nil {
			b.failed()
		}
	}()
	start := b.now()
	if err = b.ensureReceiptBackupDirectory(); err != nil {
		return stats, err
	}
	points, err := b.graphPoints(ctx)
	if err != nil {
		return stats, err
	}
	id := uint64(max(start.UnixNano(), 1))
	if len(points) > 0 && points[0].id >= id {
		if points[0].id == math.MaxUint64 {
			return stats, errors.New("backup: graph storage recovery point ID exhausted")
		}
		id = points[0].id + 1
	}
	if b.cfg.InstanceID == "" {
		return stats, errors.New("backup: graph storage recovery point requires instance ID")
	}
	tick := graphPointPrefix(b.cfg.InstanceID) + receiptBackupSetIDString(id)
	if source == "periodic" {
		b.logger.Info("backup: private graph storage backup started", "source", source, "tick_id", tick, "started_unix_ns", start.UnixNano())
	}
	cut := graphStoreCut{Format: privateGraphBackupFormat, ID: id, Instance: b.cfg.InstanceID, Namespace: b.cfg.NamespaceFormat}
	// Own the complete staging directory. No invalid point or half-written
	// witness can be discovered by another node or a restarting operator.
	stage, err := os.MkdirTemp(b.cfg.Dir, ".graph-backup-")
	if err != nil {
		return stats, err
	}
	defer os.RemoveAll(stage)
	image := filepath.Join(stage, nativeImage)
	bind := func(gen, rev uint64, s *tarp.Snapshot) error {
		app, err := dataruntime.ReadTarpBackupWitness(b.graphStore.NodeID(), gen, s, cut.Namespace)
		if err != nil {
			return err
		}
		checksum, err := graphstore.StateDigest(ctx, gen, s)
		if err != nil {
			return err
		}
		cut.Revision, cut.Application, cut.Rows = rev, app, checksum
		return nil
	}
	info, err := b.graphStore.Backup(ctx, image, false, bind)
	if errors.Is(err, graphstore.ErrBackupCutChanged) {
		if err = os.RemoveAll(image); err != nil {
			return stats, err
		}
		info, err = b.graphStore.Backup(ctx, image, true, bind)
	}
	if err != nil {
		return stats, err
	}
	cut.DatabaseID, cut.StorageSequence = info.DatabaseID, info.LastUserSequence
	raw, err := marshalGraphCut(cut)
	if err != nil {
		return stats, err
	}
	f, err := privatefile.Create(filepath.Join(stage, cutFile), os.O_WRONLY)
	if err != nil {
		return stats, err
	}
	_, writeErr := f.Write(raw)
	if err = errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return stats, err
	}
	if err = privatefile.SyncDirectory(stage); err != nil {
		return stats, err
	}
	size, err := graphPointBytes(ctx, stage)
	if err != nil {
		return stats, err
	}
	final := filepath.Join(b.cfg.Dir, tick)
	if err = ctx.Err(); err != nil {
		return stats, err
	}
	if _, err = renameReceiptBackupExclusive(stage, final); err != nil {
		return stats, err
	}
	if err = privatefile.SyncDirectory(b.cfg.Dir); err != nil {
		return stats, err
	}
	stats.Bytes, stats.Members = size, 2
	if b.metrics != nil {
		b.metrics.observeBackup(stats, b.now().Sub(start), b.now())
	}
	b.logger.Info("backup: wrote private graph storage recovery point", "source", source, "point", tick, "tick_id", tick, "started_unix_ns", start.UnixNano(), "finished_unix_ns", b.now().UnixNano(), "bytes", size, "cut_sha256", fmt.Sprintf("%x", sha256.Sum256(raw)), "local_seq", cut.Application.LocalSeq, "took", b.now().Sub(start))
	// Only independently verified, complete points owned by this node can
	// count towards retention or be selected for removal.
	if policy.Config.Retain > 0 && len(points) >= policy.Config.Retain {
		keep := policy.Config.Retain - 1 // new point is valid and already published
		for _, point := range points {
			dir := filepath.Join(b.cfg.Dir, point.name)
			old, _, _, e := loadGraphPoint(ctx, dir, b.cfg.InstanceID, point.id, b.cfg.NamespaceFormat)
			if e != nil {
				if err := ctx.Err(); err != nil {
					return stats, err
				}
				continue
			}
			if old.Application.NodeID != b.graphStore.NodeID() {
				continue
			}
			if keep > 0 {
				keep--
				continue
			}
			if err = os.RemoveAll(dir); err != nil {
				return stats, err
			}
			if err = privatefile.SyncDirectory(b.cfg.Dir); err != nil {
				return stats, err
			}
		}
	}
	return stats, nil
}

func graphPointBytes(ctx context.Context, root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("backup: unexpected non-regular private backup member")
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Size() < 0 || info.Size() > math.MaxInt64-size {
			return errors.New("backup: invalid private backup size")
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func loadGraphPoint(ctx context.Context, dir, instance string, id uint64, namespace string) (graphStoreCut, []byte, tarp.BackupInfo, error) {
	var cut graphStoreCut
	var empty tarp.BackupInfo
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return cut, nil, empty, err
	}
	if !dirInfo.IsDir() {
		return cut, nil, empty, errors.New("backup: private backup is not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return cut, nil, empty, err
	}
	if len(entries) != 2 || entries[0].Name() != cutFile || !entries[0].Type().IsRegular() || entries[1].Name() != nativeImage || !entries[1].IsDir() {
		return cut, nil, empty, errors.New("backup: unexpected private backup members")
	}
	raw, err := newReceiptBackupFS().readFile(filepath.Join(dir, cutFile), 8192)
	if err != nil {
		return cut, nil, empty, err
	}
	if err = json.Unmarshal(raw, &cut); err != nil {
		return cut, nil, empty, err
	}
	match, err := marshalGraphCut(cut)
	if err != nil || !bytes.Equal(raw, match) || cut.Format != privateGraphBackupFormat || cut.Instance != instance || cut.ID != id || id == 0 || cut.Namespace != namespace || cut.Application.Generation == 0 || cut.Application.NodeID == (hlc.NodeID{}) || cut.Rows == ([32]byte{}) {
		return cut, nil, empty, errors.New("backup: invalid private backup cut")
	}
	info, err := tarp.VerifyBackup(ctx, filepath.Join(dir, nativeImage))
	if err != nil {
		return cut, nil, empty, err
	}
	if info.DatabaseID != cut.DatabaseID || info.LastUserSequence != cut.StorageSequence {
		return cut, nil, empty, errors.New("backup: native backup belongs to a different cut")
	}
	return cut, raw, info, nil
}
