package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeService satisfies the backup Service: BackupSnapshot emits a fixed
// frame list; RestoreVertices / RestoreEdges record what restore replayed.
type fakeService struct {
	frames                []*pb.BackupSnapshotResponse
	putV                  []*pb.Vertex
	putE                  []*pb.Edge
	restoreVertexOutcomes func(*pb.PutVerticesRequest) []pb.PutOutcome
	restoreEdgeOutcomes   func(*pb.PutEdgesRequest) []pb.PutOutcome
	beginRecovery         int
	completeRecovery      int
	completeErr           error
	snapshotErr           error
}

func (f *fakeService) BackupSnapshot(_ context.Context, _ *pb.BackupSnapshotRequest, stream dataruntime.Sender[pb.BackupSnapshotResponse]) error {
	for _, fr := range f.frames {
		if err := stream.Send(fr); err != nil {
			return err
		}
	}
	return f.snapshotErr
}

func (f *fakeService) RestoreVertices(_ context.Context, req *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
	f.putV = append(f.putV, req.GetVertices()...)
	outcomes := make([]pb.PutOutcome, len(req.GetVertices()))
	for i := range outcomes {
		outcomes[i] = pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE
	}
	if f.restoreVertexOutcomes != nil {
		outcomes = f.restoreVertexOutcomes(req)
	}
	return &pb.PutVerticesResponse{Outcomes: outcomes}, nil
}

func (f *fakeService) RestoreEdges(_ context.Context, req *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error) {
	f.putE = append(f.putE, req.GetEdges()...)
	outcomes := make([]pb.PutOutcome, len(req.GetEdges()))
	for i := range outcomes {
		outcomes[i] = pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE
	}
	if f.restoreEdgeOutcomes != nil {
		outcomes = f.restoreEdgeOutcomes(req)
	}
	return &pb.PutEdgesResponse{Outcomes: outcomes}, nil
}

func (f *fakeService) BeginSearchIndexRecovery() { f.beginRecovery++ }
func (f *fakeService) CompleteSearchIndexRecovery() error {
	f.completeRecovery++
	return f.completeErr
}

func vFrame(key string) *pb.BackupSnapshotResponse {
	return &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Vertex{Vertex: &pb.Vertex{Key: key}}}
}

func eFrame(tail, head string, w float32) *pb.BackupSnapshotResponse {
	return &pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Edge{Edge: &pb.Edge{Tail: tail, Head: head, Weight: w}}}
}

func testConfig(dir string) Config {
	return Config{Enabled: true, Dir: dir, Interval: time.Hour, Retain: 3, InstanceID: "node-a", RestoreOnStart: true}
}

func TestBackupperUsesExactExecutorIdentity(t *testing.T) {
	primary := dataruntime.NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	owner := New(primary, Config{Interval: time.Hour}, nil, nil)
	var absent *Backupper
	if !owner.UsesExecutor(primary) || owner.UsesExecutor(nil) || absent.UsesExecutor(primary) {
		t.Fatal("backup executor identity was not retained")
	}
	foreign := dataruntime.NewExecutor(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	if owner.UsesExecutor(foreign) || New(&fakeService{}, Config{}, nil, nil).UsesExecutor(primary) {
		t.Fatal("foreign backup service certified as the mounted executor")
	}
}

func TestBackupper_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := &fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("a"), vFrame("b"), eFrame("a", "b", 1.5)}}
	if _, err := New(src, testConfig(dir), nil, nil).backupOnce(context.Background()); err != nil {
		t.Fatalf("backupOnce: %v", err)
	}

	dst := &fakeService{}
	stats, err := New(dst, testConfig(dir), nil, nil).RestoreOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RestoreOnStartup: %v", err)
	}
	if stats.Vertices != 2 || stats.Edges != 1 {
		t.Fatalf("stats = %+v, want {2 1}", stats)
	}
	if len(dst.putV) != 2 || dst.putV[0].GetKey() != "a" || dst.putV[1].GetKey() != "b" {
		t.Fatalf("restored vertices = %+v", dst.putV)
	}
	if len(dst.putE) != 1 || dst.putE[0].GetTail() != "a" || dst.putE[0].GetHead() != "b" || dst.putE[0].GetWeight() != 1.5 {
		t.Fatalf("restored edges = %+v", dst.putE)
	}
	if dst.beginRecovery != 1 || dst.completeRecovery != 1 {
		t.Fatalf("search recovery lifecycle = begin %d complete %d, want 1/1", dst.beginRecovery, dst.completeRecovery)
	}
}

func TestBackupper_PeriodicTickEmitsCorrelatableStages(t *testing.T) {
	// Completion, rather than a short wall-clock window, defines the sample.
	// The timeout only bounds failure if the real periodic loop stops progressing.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logs := &periodicCompletionLog{cancel: cancel}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	cfg := testConfig(t.TempDir())
	cfg.Interval = 20 * time.Millisecond
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("synthetic")}}, cfg, nil, logger)
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	starts := make(map[string]int64)
	completed := 0
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["source"] != "periodic" {
			continue
		}
		id, _ := record["tick_id"].(string)
		if id == "" {
			t.Fatalf("periodic record lacks tick_id: %v", record)
		}
		switch record["msg"] {
		case "backup: dump started":
			starts[id] = int64(record["started_unix_ns"].(float64))
		case "backup: wrote dump":
			completed++
			if _, ok := starts[id]; !ok {
				t.Fatalf("completed periodic tick %s lacks start", id)
			}
			if record["finished_unix_ns"].(float64) < record["started_unix_ns"].(float64) {
				t.Fatalf("negative backup window: %v", record)
			}
			for _, field := range []string{"materialization_ns", "send_ns", "finalization_ns"} {
				if value, ok := record[field].(float64); !ok || value < 0 {
					t.Fatalf("invalid %s in %v", field, record)
				}
			}
		}
	}
	if completed < 3 {
		t.Fatalf("completed periodic ticks = %d, want >=3", completed)
	}
}

// JSONHandler serializes Write calls; Run has returned before the test reads
// the buffer. Only successful periodic completions count, never shutdown dumps.
type periodicCompletionLog struct {
	bytes.Buffer
	completed int
	cancel    context.CancelFunc
}

func (w *periodicCompletionLog) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if err != nil {
		return n, err
	}
	var record struct {
		Message string `json:"msg"`
		Source  string `json:"source"`
	}
	if err := json.Unmarshal(p, &record); err != nil {
		return n, err
	}
	if record.Message == "backup: wrote dump" && record.Source == "periodic" {
		w.completed++
		if w.completed == 3 {
			w.cancel()
		}
	}
	return n, nil
}

func TestBackupper_RestoreCountsActualWrites(t *testing.T) {
	dir := t.TempDir()
	src := &fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("live"), vFrame("expired"), eFrame("live", "tail", 1)}}
	if _, err := New(src, testConfig(dir), nil, nil).backupOnce(context.Background()); err != nil {
		t.Fatalf("backupOnce: %v", err)
	}
	dst := &fakeService{
		restoreVertexOutcomes: func(*pb.PutVerticesRequest) []pb.PutOutcome {
			return []pb.PutOutcome{
				pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
				pb.PutOutcome_PUT_OUTCOME_EXPIRED,
			}
		},
		restoreEdgeOutcomes: func(*pb.PutEdgesRequest) []pb.PutOutcome {
			return []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_EXPIRED}
		},
	}
	reg := prometheus.NewRegistry()
	b := New(dst, testConfig(dir), reg, nil)
	stats, err := b.RestoreOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RestoreOnStartup: %v", err)
	}
	if stats.Vertices != 1 || stats.Edges != 0 {
		t.Fatalf("actual restore stats = %+v, want {Vertices:1 Edges:0}", stats)
	}
	if got := testutil.ToFloat64(b.metrics.restoreVtx); got != 1 {
		t.Fatalf("lantern_restore_vertices = %v, want 1 actual write", got)
	}
	if got := testutil.ToFloat64(b.metrics.restoreEdges); got != 0 {
		t.Fatalf("lantern_restore_edges = %v, want 0 actual writes", got)
	}
}

func TestBackupper_RestoreRejectsInvalidOutcomeVectors(t *testing.T) {
	tests := []struct {
		name     string
		outcomes []pb.PutOutcome
	}{
		{name: "length mismatch", outcomes: nil},
		{name: "unspecified", outcomes: []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_UNSPECIFIED}},
		{name: "unknown", outcomes: []pb.PutOutcome{pb.PutOutcome(99)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := &fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("v")}}
			if _, err := New(src, testConfig(dir), nil, nil).backupOnce(context.Background()); err != nil {
				t.Fatalf("backupOnce: %v", err)
			}
			dst := &fakeService{restoreVertexOutcomes: func(*pb.PutVerticesRequest) []pb.PutOutcome {
				return tt.outcomes
			}}
			if _, err := New(dst, testConfig(dir), nil, nil).RestoreOnStartup(context.Background()); err == nil {
				t.Fatalf("RestoreOnStartup accepted outcomes %v", tt.outcomes)
			}
		})
	}
}

func TestBackupper_RestoreSkipsCorruptFile(t *testing.T) {
	dir := t.TempDir()

	good := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("good")}}, testConfig(dir), nil, nil)
	good.now = func() time.Time { return time.Unix(0, 100) } // stamp 100
	if _, err := good.backupOnce(context.Background()); err != nil {
		t.Fatalf("backupOnce: %v", err)
	}

	// A NEWER (stamp 200) but corrupt dump: a varint length of 5 with only
	// two bytes following → decode fails. It must be skipped for the older
	// good file rather than aborting the restore.
	corrupt := filepath.Join(dir, fmt.Sprintf("%snode-a-200%s", filePrefix, fileSuffix))
	if err := os.WriteFile(corrupt, []byte{0x05, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}

	dst := &fakeService{}
	stats, err := New(dst, testConfig(dir), nil, nil).RestoreOnStartup(context.Background())
	if err != nil {
		t.Fatalf("RestoreOnStartup should fall back to the good file, got %v", err)
	}
	if stats.Vertices != 1 || len(dst.putV) != 1 || dst.putV[0].GetKey() != "good" {
		t.Fatalf("expected the good dump restored, got stats=%+v putV=%+v", stats, dst.putV)
	}
}

func TestBackupper_RestoreAllCorruptErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, fmt.Sprintf("%snode-a-1%s", filePrefix, fileSuffix))
	if err := os.WriteFile(bad, []byte{0x05, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(&fakeService{}, testConfig(dir), nil, nil).RestoreOnStartup(context.Background()); err == nil {
		t.Fatal("expected an error when every dump is corrupt (so RestoreRequired can fail boot)")
	}
}

func TestBackupper_RestoreEmptyDirIsFreshStart(t *testing.T) {
	stats, err := New(&fakeService{}, testConfig(t.TempDir()), nil, nil).RestoreOnStartup(context.Background())
	if err != nil {
		t.Fatalf("empty dir must not error: %v", err)
	}
	if stats != (Stats{}) {
		t.Fatalf("empty dir restore = %+v, want zero", stats)
	}
}

func TestBackupper_Retention(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.Retain = 2
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("x")}}, cfg, nil, nil)
	var n int64
	b.now = func() time.Time { n++; return time.Unix(0, n) } // strictly increasing stamps

	for i := 0; i < 5; i++ {
		if _, err := b.backupOnce(context.Background()); err != nil {
			t.Fatalf("backupOnce %d: %v", i, err)
		}
	}
	files, err := b.listBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("Retain=2 should leave 2 dumps, got %d", len(files))
	}
	// No temp files should linger.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file lingered: %s", e.Name())
		}
	}
}

func TestBackupperGraphSnapshotIDsPreserveRecoveryPoints(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stamps  []int64
		restart bool
	}{
		{name: "fixed clock", stamps: []int64{100, 100, 100}},
		{name: "backward and repeated clock after restart", stamps: []int64{100, 50, 100}, restart: true},
		{name: "nonpositive clock", stamps: []int64{0, -1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t.TempDir())
			cfg.Retain = 0
			svc := &fakeService{}
			var logs bytes.Buffer
			b := New(svc, cfg, nil, slog.New(slog.NewJSONHandler(&logs, nil)))
			originals := map[string][]byte{}
			var lastID int64
			for i, stamp := range tc.stamps {
				if tc.restart && i == len(tc.stamps)-1 {
					b = New(svc, cfg, nil, slog.New(slog.NewJSONHandler(&logs, nil)))
				}
				svc.frames = []*pb.BackupSnapshotResponse{vFrame(fmt.Sprintf("snapshot-%d", i))}
				b.now = func() time.Time { return time.Unix(0, stamp) }
				if _, err := b.BackupNow(t.Context()); err != nil {
					t.Fatal(err)
				}
				paths, err := b.listBackups()
				if err != nil || len(paths) != i+1 {
					t.Fatalf("distinct recovery points = %v, %v", paths, err)
				}
				id, ok := fileStamp(filepath.Base(paths[0]))
				if !ok || id <= lastID {
					t.Fatalf("newest snapshot ID = %d, previous %d", id, lastID)
				}
				lastID = id
				for path, before := range originals {
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(before, after) {
						t.Fatalf("completed recovery point changed: %s, %v", path, err)
					}
				}
				raw, err := os.ReadFile(paths[0])
				if err != nil {
					t.Fatal(err)
				}
				originals[paths[0]] = raw
			}
			destination := &fakeService{}
			if _, err := New(destination, cfg, nil, nil).RestoreOnStartup(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(destination.putV) != 1 || destination.putV[0].GetKey() != "snapshot-2" {
				t.Fatalf("restore did not choose latest publication: %v", destination.putV)
			}
			preview, err := b.PreviewRetention(t.Context(), 2)
			if err != nil || !preview.Complete || preview.ValidBackups != 3 || preview.PruneBackups != 1 {
				t.Fatalf("logical-ID retention preview = %+v, %v", preview, err)
			}
			if err := b.prune(t.Context(), 2); err != nil {
				t.Fatal(err)
			}
			remaining, err := b.listBackups()
			if err != nil || len(remaining) != 2 {
				t.Fatalf("retained points = %v, %v", remaining, err)
			}
			var wrote int
			for _, line := range bytes.Split(logs.Bytes(), []byte{'\n'}) {
				var record map[string]any
				if len(line) == 0 {
					continue
				}
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if record["msg"] == "backup: wrote dump" {
					if record["tick_id"] != fmt.Sprint(tc.stamps[wrote]) || record["snapshot_id"] == nil {
						t.Fatalf("wall tick and allocated ID were conflated: %v", record)
					}
					wrote++
				}
			}
			if wrote != len(tc.stamps) {
				t.Fatalf("successful publication logs = %d", wrote)
			}
		})
	}
}

func TestBackupperGraphSnapshotIDsReserveCanonicalOwnedEntries(t *testing.T) {
	for _, kind := range []string{"stale staging", "corrupt final", "directory"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testConfig(t.TempDir())
			cfg.Retain = 0
			b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("old")}}, cfg, nil, nil)
			b.now = func() time.Time { return time.Unix(0, 50) }
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(cfg.Dir, b.fileName(time.Unix(0, 50)))
			oldBytes, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			preserved := []string{
				filePrefix + cfg.InstanceID + "-peer-900" + fileSuffix,
				b.ownPrefix() + "090" + fileSuffix,
			}
			reserved := b.fileName(time.Unix(0, 80))
			switch kind {
			case "stale staging":
				preserved = append(preserved, reserved+".tmp")
			case "corrupt final":
				preserved = append(preserved, reserved)
			case "directory":
				if err := os.Mkdir(filepath.Join(cfg.Dir, reserved), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range preserved {
				if err := os.WriteFile(filepath.Join(cfg.Dir, name), []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			b = New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("new")}}, cfg, nil, nil)
			b.now = func() time.Time { return time.Unix(0, 1) }
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			newPath := filepath.Join(cfg.Dir, b.fileName(time.Unix(0, 81)))
			vertices, _, err := decodeFile(newPath)
			if err != nil || len(vertices) != 1 || vertices[0].GetKey() != "new" {
				t.Fatalf("reserved-ID publication = %v, %v", vertices, err)
			}
			after, err := os.ReadFile(oldPath)
			if err != nil || !bytes.Equal(oldBytes, after) {
				t.Fatalf("old timestamp-named point changed: %v", err)
			}
			preview, err := b.PreviewRetention(t.Context(), 1)
			if err != nil || !preview.Complete || preview.ValidBackups != 2 || preview.PruneBackups != 1 {
				t.Fatalf("legacy/logical valid catalog = %+v, %v", preview, err)
			}
			if err := b.prune(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			for _, name := range preserved {
				raw, err := os.ReadFile(filepath.Join(cfg.Dir, name))
				if err != nil || string(raw) != name {
					t.Fatalf("unowned, invalid or staging entry changed: %s, %v", name, err)
				}
			}
			if kind == "directory" {
				if info, err := os.Stat(filepath.Join(cfg.Dir, reserved)); err != nil || !info.IsDir() {
					t.Fatalf("nonregular blocker changed: %v", err)
				}
			}
			if _, err := os.Stat(newPath); err != nil {
				t.Fatalf("newest valid recovery point was pruned: %v", err)
			}
		})
	}
}

type graphBackupReadDirectory struct {
	read     func(int) ([]os.DirEntry, error)
	closeErr error
}

func (d *graphBackupReadDirectory) ReadDir(n int) ([]os.DirEntry, error) { return d.read(n) }
func (d *graphBackupReadDirectory) Close() error                         { return d.closeErr }

func TestBackupperGraphSnapshotAllocationFailuresPreserveRecoveryPoints(t *testing.T) {
	injected := errors.New("graph allocation fault")
	for _, name := range []string{"open", "read", "close", "no progress", "directory bound", "cancel during scan", "ID exhaustion"} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t.TempDir())
			cfg.Retain = 0
			b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("old")}}, cfg, nil, nil)
			b.now = func() time.Time { return time.Unix(0, 100) }
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(cfg.Dir, b.fileName(time.Unix(0, 100)))
			before, err := os.ReadFile(old)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entries, err := os.ReadDir(cfg.Dir)
			if err != nil {
				t.Fatal(err)
			}
			want := injected
			switch name {
			case "open":
				b.fs.openDir = func(string) (receiptBackupReadDir, error) { return nil, injected }
			case "read":
				b.fs.openDir = func(string) (receiptBackupReadDir, error) {
					return &graphBackupReadDirectory{read: func(int) ([]os.DirEntry, error) { return nil, injected }}, nil
				}
			case "close":
				b.fs.openDir = func(string) (receiptBackupReadDir, error) {
					return &graphBackupReadDirectory{read: func(int) ([]os.DirEntry, error) { return nil, io.EOF }, closeErr: injected}, nil
				}
			case "no progress":
				want = io.ErrNoProgress
				b.fs.openDir = func(string) (receiptBackupReadDir, error) {
					return &graphBackupReadDirectory{read: func(int) ([]os.DirEntry, error) { return nil, nil }}, nil
				}
			case "directory bound":
				want = errReceiptBackupDirectoryLimit
				b.fs.openDir = func(string) (receiptBackupReadDir, error) {
					return &graphBackupReadDirectory{read: func(n int) ([]os.DirEntry, error) {
						batch := make([]os.DirEntry, n)
						for i := range batch {
							batch[i] = entries[0]
						}
						return batch, nil
					}}, nil
				}
			case "cancel during scan":
				want = context.Canceled
				b.fs.openDir = func(string) (receiptBackupReadDir, error) {
					return &graphBackupReadDirectory{read: func(int) ([]os.DirEntry, error) { cancel(); return entries, io.EOF }}, nil
				}
			case "ID exhaustion":
				want = nil
				if err := os.WriteFile(filepath.Join(cfg.Dir, b.fileName(time.Unix(0, 1<<63-1))), []byte("reserved"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			created, published := 0, 0
			b.fs.openExclusive = func(string, int, os.FileMode) (receiptBackupSyncFile, bool, error) {
				created++
				return nil, false, injected
			}
			b.fs.renameExclusive = func(string, string) (bool, error) { published++; return false, injected }
			if _, err := b.BackupNow(ctx); err == nil || (want != nil && !errors.Is(err, want)) || (want == nil && !strings.Contains(err.Error(), "ID exhausted")) {
				t.Fatalf("allocation failure = %v, want %v", err, want)
			}
			if created != 0 || published != 0 {
				t.Fatalf("incomplete allocation touched staging/publication: %d/%d", created, published)
			}
			after, err := os.ReadFile(old)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("allocation failure changed old recovery point: %v", err)
			}
		})
	}
}

type graphBackupWriteFaultFile struct {
	receiptBackupSyncFile
	writeErr error
	closeErr error
	cancel   context.CancelFunc
}

func TestBackupperGraphFailedPublicationIDIsNotReusedByOwner(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Retain = 0
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("retry-independent-point")}}, cfg, nil, nil)
	b.now = func() time.Time { return time.Unix(0, 100) }
	base := b.fs
	injected := errors.New("first publication fault")
	b.fs.renameExclusive = func(string, string) (bool, error) { return false, injected }
	if _, err := b.BackupNow(t.Context()); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	b.fs = base
	if _, err := b.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	paths, err := b.listBackups()
	if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != b.fileName(time.Unix(0, 101)) {
		t.Fatalf("failed allocation was reused: %v, %v", paths, err)
	}
}

func (f *graphBackupWriteFaultFile) Write(raw []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.receiptBackupSyncFile.Write(raw)
}

func (f *graphBackupWriteFaultFile) Close() error {
	err := f.receiptBackupSyncFile.Close()
	if f.cancel != nil {
		f.cancel()
	}
	return errors.Join(err, f.closeErr)
}

func TestBackupperGraphPublicationFailuresNeverReplaceRecoveryPoints(t *testing.T) {
	injected := errors.New("graph publication fault")
	for _, name := range []string{"staging collision", "publication collision", "publication false without error", "published then error", "published unreported error", "write", "close", "snapshot", "cancel before publication"} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t.TempDir())
			cfg.Retain = 1
			svc := &fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("old")}}
			b := New(svc, cfg, nil, nil)
			b.now = func() time.Time { return time.Unix(0, 100) }
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(cfg.Dir, b.fileName(time.Unix(0, 100)))
			before, err := os.ReadFile(old)
			if err != nil {
				t.Fatal(err)
			}
			final := filepath.Join(cfg.Dir, b.fileName(time.Unix(0, 101)))
			tmp := final + ".tmp"
			svc.frames = []*pb.BackupSnapshotResponse{vFrame("new")}
			base := b.fs
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := injected
			var publishCalls int
			b.fs.renameExclusive = func(oldPath, newPath string) (bool, error) {
				publishCalls++
				switch name {
				case "publication collision":
					if err := os.WriteFile(newPath, []byte("foreign final"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "publication false without error":
					return false, nil
				}
				renamed, err := base.renameExclusive(oldPath, newPath)
				if err == nil && (name == "published then error" || name == "published unreported error") {
					return name == "published then error", injected
				}
				return renamed, err
			}
			switch name {
			case "staging collision":
				want = os.ErrExist
				b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, bool, error) {
					if flag != os.O_WRONLY|os.O_CREATE|os.O_EXCL || mode != 0o666 {
						t.Fatal("graph staging flags/permissions changed")
					}
					if err := os.WriteFile(path, []byte("foreign staging"), 0o600); err != nil {
						t.Fatal(err)
					}
					return base.openExclusive(path, flag, mode)
				}
			case "publication collision":
				want = os.ErrExist
			case "publication false without error":
				want = nil
			case "write", "close", "cancel before publication":
				if name == "cancel before publication" {
					want = context.Canceled
				}
				b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, bool, error) {
					file, owned, err := base.openExclusive(path, flag, mode)
					if err != nil {
						return file, owned, err
					}
					wrapper := &graphBackupWriteFaultFile{receiptBackupSyncFile: file}
					if name == "write" {
						wrapper.writeErr = injected
					}
					if name == "close" {
						wrapper.closeErr = injected
					}
					if name == "cancel before publication" {
						wrapper.cancel = cancel
					}
					return wrapper, owned, nil
				}
			case "snapshot":
				svc.snapshotErr = injected
			}
			stats, err := b.BackupNow(ctx)
			if err == nil || (want != nil && !errors.Is(err, want)) || stats != (Stats{}) {
				t.Fatalf("failed publication = %+v, %v, want %v", stats, err, want)
			}
			if testutil.ToFloat64(b.metrics.failures) != 1 {
				t.Fatal("failed attempt did not report exactly one failure")
			}
			after, err := os.ReadFile(old)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed publication changed or pruned old point: %v", err)
			}
			if name == "staging collision" {
				raw, err := os.ReadFile(tmp)
				if err != nil || string(raw) != "foreign staging" || publishCalls != 0 {
					t.Fatalf("foreign staging changed: %q, %v", raw, err)
				}
			} else if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned staging remains: %v", err)
			}
			switch name {
			case "publication collision":
				raw, err := os.ReadFile(final)
				if err != nil || string(raw) != "foreign final" {
					t.Fatalf("foreign final changed: %q, %v", raw, err)
				}
			case "published then error", "published unreported error":
				vertices, _, err := decodeFile(final)
				if err != nil || len(vertices) != 1 || vertices[0].GetKey() != "new" {
					t.Fatalf("visible final removed after publication error: %v, %v", vertices, err)
				}
			default:
				if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial final exposed: %v", err)
				}
			}
			if name == "cancel before publication" && publishCalls != 0 {
				t.Fatal("cancelled attempt published")
			}
		})
	}
}

func TestBackupper_DisabledNoOps(t *testing.T) {
	b := New(&fakeService{}, Config{Enabled: false, Dir: t.TempDir(), RestoreOnStart: false}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := b.Run(ctx); err != nil {
		t.Fatalf("disabled Run should stop without producing a final backup, got %v", err)
	}
	stats, err := b.RestoreOnStartup(context.Background())
	if err != nil || stats != (Stats{}) {
		t.Fatalf("disabled restore should be a no-op, got stats=%+v err=%v", stats, err)
	}
}

func TestBackupper_GraphRetentionBoundsPreserveRecoveryPoints(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Retain = 0
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("bounded")}}, cfg, nil, nil)
	var n int64
	b.now = func() time.Time { n++; return time.Unix(0, n) } // strictly increasing stamps
	for range 2 {
		if _, err := b.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	b.fs.readFile = func(string, int64) ([]byte, error) { reads++; return nil, ErrRetentionPreviewLimit }
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 1, Config: ProductionConfig{Interval: time.Hour, Retain: 1}}); err != nil {
		t.Fatal(err)
	}
	stats, err := b.BackupNow(t.Context())
	if !errors.Is(err, ErrRetentionPreviewLimit) || stats.Vertices != 1 || reads != 1 {
		t.Fatalf("bounded post-publication retention = %+v, %v, reads %d", stats, err, reads)
	}
	files, err := b.listBackups()
	if err != nil || len(files) != 3 {
		t.Fatalf("incomplete retention removed recovery points: %v, %v", files, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := b.prune(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retention = %v", err)
	}
	reads = 0
	for _, retain := range []int{0, receiptBackupDirectoryMaxEntries, int(^uint(0) >> 1)} {
		if err := b.prune(t.Context(), retain); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 0 {
		t.Fatal("safe keep-all/large retention unnecessarily validated catalog")
	}
}

func TestBackupper_PerInstanceFileNaming(t *testing.T) {
	dir := t.TempDir()
	// Two instances writing to the same shared dir must not collide nor
	// prune each other.
	a := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("x")}}, Config{Enabled: true, Dir: dir, Retain: 1, InstanceID: "node-a"}, nil, nil)
	bcfg := Config{Enabled: true, Dir: dir, Retain: 1, InstanceID: "node-b"}
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("y")}}, bcfg, nil, nil)
	var na, nb int64
	a.now = func() time.Time { na++; return time.Unix(0, na) }
	b.now = func() time.Time { nb++; return time.Unix(100, nb) }

	for i := 0; i < 3; i++ {
		if _, err := a.backupOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := b.backupOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Retain=1 per instance → one file each survives, two total.
	all, _ := a.listBackups()
	if len(all) != 2 {
		t.Fatalf("expected 1 dump per instance (2 total), got %d", len(all))
	}
}

func TestBackupperReceiptAttemptsSerializePeriodicManualAndShutdown(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
	source.hook = func(context.Context) error {
		first.Do(func() {
			close(entered)
			<-release
		})
		time.Sleep(5 * time.Millisecond)
		return nil
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	cfg := Config{
		Enabled: true, Dir: t.TempDir(), Interval: 10 * time.Millisecond,
		Retain: 0, InstanceID: "scheduler-owner",
	}
	b, err := NewReceipt(&fakeService{}, source, archive.Policy, cfg, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	b.now = func() time.Time { return time.Unix(0, 800).UTC() }

	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- b.Run(runCtx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("periodic receipt backup did not start")
	}
	manualStarted := make(chan struct{})
	manualDone := make(chan error, 1)
	go func() {
		close(manualStarted)
		_, err := b.BackupNow(t.Context())
		manualDone <- err
	}()
	<-manualStarted
	cancelRun()
	close(release)
	select {
	case err := <-manualDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual receipt backup did not finish")
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receipt backup scheduler did not finish")
	}
	if source.max.Load() != 1 || source.calls.Load() != 3 {
		t.Fatalf("scheduler source calls/max active = %d/%d, want 3/1",
			source.calls.Load(), source.max.Load())
	}
	for _, label := range []string{`"source":"periodic"`, `"source":"manual"`, `"source":"shutdown"`} {
		if !strings.Contains(logs.String(), label) {
			t.Fatalf("scheduler logs lack %s: %s", label, logs.String())
		}
	}
	sets, err := b.collectReceiptBackupSets()
	if err != nil || len(sets) != 2 {
		t.Fatalf("manual/final committed sets = %d, %v, want 2", len(sets), err)
	}
}

func TestBackupperReceiptMetricsPreserveExistingSeriesAndAddSetStats(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
	registry := prometheus.NewRegistry()
	b, err := NewReceipt(
		&fakeService{},
		source,
		archive.Policy,
		Config{
			Enabled: true, Dir: t.TempDir(), Interval: time.Hour,
			Retain: 1, InstanceID: "metrics-owner",
		},
		registry,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	b.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	stats, err := b.BackupNow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name string
		got  float64
		want float64
	}{
		{"lantern_backup_vertices", testutil.ToFloat64(b.metrics.vertices), float64(stats.Vertices)},
		{"lantern_backup_edges", testutil.ToFloat64(b.metrics.edges), float64(stats.Edges)},
		{"lantern_backup_receipts", testutil.ToFloat64(b.metrics.receipts), float64(stats.Receipts)},
		{"lantern_backup_origins", testutil.ToFloat64(b.metrics.origins), float64(stats.Origins)},
		{"lantern_backup_set_members", testutil.ToFloat64(b.metrics.setMembers), 3},
		{"lantern_backup_set_bytes", testutil.ToFloat64(b.metrics.setBytes), float64(stats.Bytes)},
		{"lantern_backup_last_success_timestamp_seconds", testutil.ToFloat64(b.metrics.lastSuccess), 1000},
	} {
		if check.got != check.want {
			t.Fatalf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}

func TestBackupperClassifiedPhysicalData(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.NamespaceFormat = "namespaced-v1"
	source := &fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("data:sys:client"), vFrame("data:data:client"), eFrame("data:sys:client", "data:data:client", 1)}}
	producer := New(source, cfg, nil, nil)
	if _, err := producer.backupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	paths, err := producer.listBackups()
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	if _, _, err := decodeFile(paths[0]); err == nil {
		t.Fatal("physical recovery archive accepted as logical export")
	}
	destination := &fakeService{}
	stats, err := New(destination, cfg, nil, nil).RestoreOnStartup(t.Context())
	if err != nil || stats.Vertices != 2 || stats.Edges != 1 {
		t.Fatal(stats, err)
	}
	if destination.putV[0].GetKey() != "data:sys:client" {
		t.Fatal("physical identity was double encoded")
	}
	// An old logical archive must fail before any restore or index effects.
	legacyDir := t.TempDir()
	legacy := testConfig(legacyDir)
	if _, err := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("data:ambiguous")}}, legacy, nil, nil).backupOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	legacy.NamespaceFormat = cfg.NamespaceFormat
	untouched := &fakeService{}
	if _, err := New(untouched, legacy, nil, nil).RestoreOnStartup(t.Context()); err == nil {
		t.Fatal("unclassified archive silently migrated")
	}
	if len(untouched.putV) != 0 || untouched.beginRecovery != 0 {
		t.Fatal("format failure changed restored state")
	}
	// A sys: row is rejected by the producer rather than persisted as graph data.
	if _, err := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("sys:security:roles")}}, cfg, nil, nil).backupOnce(t.Context()); err == nil {
		t.Fatal("system row accepted by graph backup")
	}
}
