package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
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
}

func (f *fakeService) BackupSnapshot(_ context.Context, _ *pb.BackupSnapshotRequest, stream service.Sender[pb.BackupSnapshotResponse]) error {
	for _, fr := range f.frames {
		if err := stream.Send(fr); err != nil {
			return err
		}
	}
	return nil
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
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	cfg := testConfig(t.TempDir())
	cfg.Interval = 20 * time.Millisecond
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("synthetic")}}, cfg, nil, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
	defer cancel()
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

func TestBackupper_DisabledNoOps(t *testing.T) {
	b := New(&fakeService{}, Config{Enabled: false, Dir: t.TempDir(), RestoreOnStart: false}, nil, nil)
	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("disabled Run should return nil immediately, got %v", err)
	}
	stats, err := b.RestoreOnStartup(context.Background())
	if err != nil || stats != (Stats{}) {
		t.Fatalf("disabled restore should be a no-op, got stats=%+v err=%v", stats, err)
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
		{"lantern_backup_set_members", testutil.ToFloat64(b.metrics.setMembers), 2},
		{"lantern_backup_set_bytes", testutil.ToFloat64(b.metrics.setBytes), float64(stats.Bytes)},
		{"lantern_backup_last_success_timestamp_seconds", testutil.ToFloat64(b.metrics.lastSuccess), 1000},
	} {
		if check.got != check.want {
			t.Fatalf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}
