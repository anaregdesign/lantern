// Package backup is the server-internal snapshot-durability engine (#770):
// a goroutine that periodically dumps the whole graph to a mounted volume,
// plus restore-on-startup that re-seeds the in-memory graph from the newest
// dump before the server begins serving. It is the automation layer on top
// of the on-demand backup/restore primitives shipped in #685 — the
// rolling-update insurance for single-instance (Tier B) Cloud Run / ACA
// deploys where the in-memory graph would otherwise be lost on every
// revision rotation.
//
// Reuse, not reinvention: the periodic dump drives the existing
// LanternService.BackupSnapshot RPC verbatim through a file-backed Sender,
// so files are byte-identical to `lantern-cli dump --format proto` and
// interchange with the CLI. Restore replays frames through the in-process
// PutVertices / PutEdges, so HLC stamping and the born-expired skip (#698)
// apply automatically — an entry whose TTL already elapsed since the dump
// is correctly NOT resurrected.
//
// Shared storage is never assumed safe for concurrent writes (Cloud Run
// GCS FUSE / NFS have no file locking; ACA Azure Files leaves multi-writer
// coordination to the app), so every writer owns a per-instance file
// (name carries InstanceID) and restore selects the newest valid file.
// Writes are atomic via a temp file + rename; a half-written file is never
// a restore candidate.
package backup

import (
	"bufio"
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

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protodelim"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

const (
	filePrefix = "lantern-backup-"
	fileSuffix = ".lbk"
	tmpSuffix  = ".lbk.tmp"

	restoreChunkSize = 1000
)

// Config is the resolved backup configuration. It lives in this package
// (not provider) so the dependency direction stays provider → backup. The
// provider's loader resolves RestoreOnStart from the LANTERN_BACKUP_* env
// before constructing the Backupper.
type Config struct {
	// Enabled gates the periodic dump loop. Resolved to false when
	// LANTERN_BACKUP_DIR is empty even if LANTERN_BACKUP_ENABLED is set.
	Enabled bool
	// Dir is the mounted directory backups are written to and read from.
	Dir string
	// Interval is the dump cadence.
	Interval time.Duration
	// Retain caps how many of THIS instance's own dumps are kept (newest
	// first). 0 keeps all.
	Retain int
	// InstanceID is the per-instance filename token (collision-free writes
	// on shared storage). Defaults to the hostname.
	InstanceID string
	// RestoreOnStart is the resolved decision to restore the newest dump on
	// boot as a baseline (already factors enabled, dir, and the env knob);
	// peer bootstrap later overlays that baseline via HLC.
	RestoreOnStart bool
	// RestoreRequired makes a restore error fail boot instead of warning.
	RestoreRequired bool
}

// Service is the subset of *service.LanternService the Backupper drives.
// Declared as an interface so tests can inject a fake without standing up a
// full GraphCache.
type Service interface {
	BackupSnapshot(ctx context.Context, req *pb.BackupSnapshotRequest, stream service.Sender[pb.BackupSnapshotResponse]) error
	PutVertices(ctx context.Context, req *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error)
	PutEdges(ctx context.Context, req *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error)
}

// Stats counts the vertices and edges in a single backup or restore.
type Stats struct {
	Vertices int
	Edges    int
}

// Backupper owns the periodic-dump loop and restore-on-startup.
type Backupper struct {
	svc     Service
	cfg     Config
	logger  *slog.Logger
	metrics *metrics
	now     func() time.Time
}

// New constructs a Backupper. reg may be nil (metrics unregistered but the
// gauges still function). A disabled config (Enabled=false) yields a
// Backupper whose Run / RestoreOnStartup are no-ops, so callers never have
// to nil-check.
func New(svc Service, cfg Config, reg prometheus.Registerer, logger *slog.Logger) *Backupper {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backupper{svc: svc, cfg: cfg, metrics: newMetrics(reg), logger: logger, now: time.Now}
}

// RestoreOnStartup loads the newest valid dump from the backup directory and
// replays it through PutVertices / PutEdges. It is a no-op (zero, nil) when
// restore is disabled or the directory is empty/absent — a fresh start is
// not an error. A directory that contains only corrupt files returns an
// error so RestoreRequired callers can fail boot.
func (b *Backupper) RestoreOnStartup(ctx context.Context) (Stats, error) {
	if !b.cfg.RestoreOnStart {
		return Stats{}, nil
	}
	candidates, err := b.listBackups()
	if err != nil {
		return Stats{}, err
	}
	if len(candidates) == 0 {
		b.logger.Info("backup: restore-on-startup found no dumps; starting fresh",
			slog.String("dir", b.cfg.Dir))
		return Stats{}, nil
	}

	var lastErr error
	for _, path := range candidates {
		vertices, edges, derr := decodeFile(path)
		if derr != nil {
			b.logger.Warn("backup: dump failed to decode; trying older file",
				slog.String("file", path), slog.Any("err", derr))
			lastErr = derr
			continue
		}
		stats, aerr := b.apply(ctx, vertices, edges)
		if aerr != nil {
			return stats, aerr
		}
		b.logger.Info("backup: restored from dump",
			slog.String("file", path),
			slog.Int("vertices", stats.Vertices),
			slog.Int("edges", stats.Edges))
		if b.metrics != nil {
			b.metrics.observeRestore(stats, b.now())
		}
		return stats, nil
	}
	return Stats{}, fmt.Errorf("backup: all %d dump(s) in %s failed to decode: %w",
		len(candidates), b.cfg.Dir, lastErr)
}

// Run drives the periodic dump loop until ctx is cancelled. It is a no-op
// when backups are disabled. On graceful cancellation it takes one final
// best-effort dump so the latest state is captured before exit.
func (b *Backupper) Run(ctx context.Context) error {
	if !b.cfg.Enabled || b.cfg.Interval <= 0 {
		return nil
	}
	b.logger.Info("backup: periodic dump loop started",
		slog.String("dir", b.cfg.Dir),
		slog.Duration("interval", b.cfg.Interval),
		slog.Int("retain", b.cfg.Retain),
		slog.String("instance", b.cfg.InstanceID))

	ticker := time.NewTicker(b.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final best-effort dump on graceful shutdown, bounded so it
			// never blocks the drain budget.
			finalCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := b.backupOnce(finalCtx); err != nil {
				b.logger.Warn("backup: final dump on shutdown failed", slog.Any("err", err))
			}
			cancel()
			return nil
		case <-ticker.C:
			if _, err := b.backupOnce(ctx); err != nil {
				b.logger.Warn("backup: periodic dump failed", slog.Any("err", err))
			}
		}
	}
}

// BackupNow writes one whole-graph dump immediately and returns its stats.
// It is the one-shot the periodic loop runs each tick, exposed so an
// operator- or test-driven backup reuses the same atomic-write + retention
// path.
func (b *Backupper) BackupNow(ctx context.Context) (Stats, error) {
	return b.backupOnce(ctx)
}

// backupOnce writes one whole-graph dump atomically (temp file + rename),
// then prunes to Retain newest of this instance's own dumps.
func (b *Backupper) backupOnce(ctx context.Context) (Stats, error) {
	start := b.now()
	if err := os.MkdirAll(b.cfg.Dir, 0o755); err != nil {
		b.failed()
		return Stats{}, fmt.Errorf("backup: mkdir %s: %w", b.cfg.Dir, err)
	}

	final := filepath.Join(b.cfg.Dir, b.fileName(start))
	tmp := final[:len(final)-len(fileSuffix)] + tmpSuffix

	f, err := os.Create(tmp)
	if err != nil {
		b.failed()
		return Stats{}, fmt.Errorf("backup: create %s: %w", tmp, err)
	}
	sender := &protoFileSender{w: bufio.NewWriter(f)}
	serr := b.svc.BackupSnapshot(ctx, &pb.BackupSnapshotRequest{}, sender)
	if serr == nil {
		serr = sender.w.Flush()
	}
	if cerr := f.Close(); serr == nil {
		serr = cerr
	}
	if serr != nil {
		_ = os.Remove(tmp)
		b.failed()
		return Stats{}, fmt.Errorf("backup: write %s: %w", tmp, serr)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		b.failed()
		return Stats{}, fmt.Errorf("backup: rename %s: %w", final, err)
	}

	stats := Stats{Vertices: sender.vertices, Edges: sender.edges}
	if b.metrics != nil {
		b.metrics.observeBackup(stats, b.now().Sub(start), b.now())
	}
	b.logger.Info("backup: wrote dump",
		slog.String("file", final),
		slog.Int("vertices", stats.Vertices),
		slog.Int("edges", stats.Edges),
		slog.Duration("took", b.now().Sub(start)))
	b.prune()
	return stats, nil
}

func (b *Backupper) failed() {
	if b.metrics != nil {
		b.metrics.failures.Inc()
	}
}

// apply replays decoded vertices then edges through the service in chunks.
// Vertices first so PutEdges' auto-created endpoints don't shadow real
// vertex values.
func (b *Backupper) apply(ctx context.Context, vertices []*pb.Vertex, edges []*pb.Edge) (Stats, error) {
	var stats Stats
	for i := 0; i < len(vertices); i += restoreChunkSize {
		end := min(i+restoreChunkSize, len(vertices))
		if _, err := b.svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: vertices[i:end]}); err != nil {
			return stats, fmt.Errorf("backup: restore PutVertices: %w", err)
		}
		stats.Vertices += end - i
	}
	for i := 0; i < len(edges); i += restoreChunkSize {
		end := min(i+restoreChunkSize, len(edges))
		if _, err := b.svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: edges[i:end]}); err != nil {
			return stats, fmt.Errorf("backup: restore PutEdges: %w", err)
		}
		stats.Edges += end - i
	}
	return stats, nil
}

// fileName builds this instance's dump filename for the given instant. The
// nanosecond stamp keeps names unique even under a tight test loop.
func (b *Backupper) fileName(ts time.Time) string {
	return fmt.Sprintf("%s%s-%d%s", filePrefix, b.cfg.InstanceID, ts.UnixNano(), fileSuffix)
}

// ownPrefix is the filename prefix of this instance's dumps, used to scope
// retention pruning so an instance never deletes a peer's files.
func (b *Backupper) ownPrefix() string {
	return filePrefix + b.cfg.InstanceID + "-"
}

// listBackups returns every finalised dump in the directory (any instance),
// newest first. Ordering uses the nanosecond stamp embedded in the filename
// (deterministic, independent of filesystem mtime granularity), falling back
// to mtime. Temp files and non-dump entries are ignored.
func (b *Backupper) listBackups() ([]string, error) {
	files, err := b.collect(filePrefix)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths, nil
}

// prune deletes this instance's own dumps beyond Retain (newest kept). Only
// files carrying this instance's token are eligible, so concurrent writers
// on shared storage never race to delete each other's dumps.
func (b *Backupper) prune() {
	if b.cfg.Retain <= 0 {
		return
	}
	own, err := b.collect(b.ownPrefix())
	if err != nil || len(own) <= b.cfg.Retain {
		return
	}
	for _, f := range own[b.cfg.Retain:] {
		if rerr := os.Remove(f.path); rerr != nil {
			b.logger.Warn("backup: prune failed", slog.String("file", f.path), slog.Any("err", rerr))
		}
	}
}

// backupFile is a finalised dump plus its sort keys.
type backupFile struct {
	path  string
	stamp int64
	mod   time.Time
}

// collect returns the finalised dumps whose filename starts with prefix,
// sorted newest first by embedded stamp (mtime tiebreak).
func (b *Backupper) collect(prefix string) ([]backupFile, error) {
	entries, err := os.ReadDir(b.cfg.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: read dir %s: %w", b.cfg.Dir, err)
	}
	var out []backupFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, fileSuffix) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		stamp, _ := fileStamp(name)
		out = append(out, backupFile{path: filepath.Join(b.cfg.Dir, name), stamp: stamp, mod: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].stamp != out[j].stamp {
			return out[i].stamp > out[j].stamp
		}
		return out[i].mod.After(out[j].mod)
	})
	return out, nil
}

// fileStamp parses the trailing "-<unixNano>.lbk" stamp from a dump
// filename. The instance token may itself contain '-', so the stamp is the
// final '-'-separated segment.
func fileStamp(name string) (int64, bool) {
	s := strings.TrimSuffix(name, fileSuffix)
	i := strings.LastIndexByte(s, '-')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(s[i+1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// protoFileSender satisfies service.Sender[pb.BackupSnapshotResponse] by
// writing each frame as length-delimited protobuf — byte-identical to the
// SDK / CLI proto dump format.
type protoFileSender struct {
	w        *bufio.Writer
	vertices int
	edges    int
}

func (s *protoFileSender) Send(rec *pb.BackupSnapshotResponse) error {
	if _, err := protodelim.MarshalTo(s.w, rec); err != nil {
		return err
	}
	switch rec.GetRecord().(type) {
	case *pb.BackupSnapshotResponse_Vertex:
		s.vertices++
	case *pb.BackupSnapshotResponse_Edge:
		s.edges++
	}
	return nil
}

// decodeFile reads a whole dump into memory, validating that every frame
// decodes. A mid-stream decode error (a truncated / corrupt file) is
// returned so the caller can fall back to an older dump WITHOUT having
// applied a partial file.
func decodeFile(path string) (vertices []*pb.Vertex, edges []*pb.Edge, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	for {
		rec := &pb.BackupSnapshotResponse{}
		if uerr := protodelim.UnmarshalFrom(r, rec); uerr != nil {
			if errors.Is(uerr, io.EOF) {
				break
			}
			return nil, nil, uerr
		}
		switch x := rec.GetRecord().(type) {
		case *pb.BackupSnapshotResponse_Vertex:
			vertices = append(vertices, x.Vertex)
		case *pb.BackupSnapshotResponse_Edge:
			edges = append(edges, x.Edge)
		}
	}
	return vertices, edges, nil
}
