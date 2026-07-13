package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeService satisfies the backup Service: BackupSnapshot emits a fixed
// frame list; PutVertices / PutEdges record what restore replayed.
type fakeService struct {
	frames             []*pb.BackupSnapshotResponse
	putV               []*pb.Vertex
	putE               []*pb.Edge
	restoreVertexCount func(*pb.PutVerticesRequest) int32
	restoreEdgeCount   func(*pb.PutEdgesRequest) int32
	beginRecovery      int
	completeRecovery   int
	completeErr        error
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
	written := int32(len(req.GetVertices()))
	if f.restoreVertexCount != nil {
		written = f.restoreVertexCount(req)
	}
	return &pb.PutVerticesResponse{Written: written}, nil
}

func (f *fakeService) PutEdges(_ context.Context, req *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error) {
	f.putE = append(f.putE, req.GetEdges()...)
	written := int32(len(req.GetEdges()))
	if f.restoreEdgeCount != nil {
		written = f.restoreEdgeCount(req)
	}
	return &pb.PutEdgesResponse{Written: written}, nil
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

func TestBackupper_RestoreCountsActualWrites(t *testing.T) {
	dir := t.TempDir()
	src := &fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("live"), vFrame("expired"), eFrame("live", "tail", 1)}}
	if _, err := New(src, testConfig(dir), nil, nil).backupOnce(context.Background()); err != nil {
		t.Fatalf("backupOnce: %v", err)
	}
	dst := &fakeService{
		restoreVertexCount: func(*pb.PutVerticesRequest) int32 { return 1 },
		restoreEdgeCount:   func(*pb.PutEdgesRequest) int32 { return 0 },
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
