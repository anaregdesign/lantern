package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/tarp"
)

func TestGraphStoreRecoveryPoint(t *testing.T) {
	ctx := t.Context()
	node := hlc.NodeID{7}
	db, err := graphstore.Open(ctx, filepath.Join(t.TempDir(), "graph"), node)
	if err != nil {
		t.Fatal(err)
	}
	graph := graphcache.NewGraphCache[string, *pb.Vertex](0)
	rows, err := graphstorage.Rows()
	if err != nil {
		t.Fatal(err)
	}
	stage := func(ctx context.Context, generation uint64, s *tarp.Snapshot, b graphstore.Writer, keys graphcache.StorageKeys[string], writer graphcache.StorageRowWriter[string, *pb.Vertex]) error {
		if writer == nil {
			writer = graph
		}
		return writer.StoreRows(ctx, rows, s, b, generation, keys)
	}
	rt, err := dataruntime.NewTarpGraphServingRuntime(t.Context(), graph, db, mutationlog.Options{Capacity: 16}, stage, keyspace.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	svc := rt.NewExecutor(nil)
	cfg := Config{NamespaceFormat: keyspace.Version, Dir: t.TempDir(), InstanceID: "nodeA", Interval: time.Hour, Retain: 2, RestoreOnStart: true, RestoreRequired: true}
	b := NewGraphStore(svc, db, cfg, nil, nil)
	put := func(value string) {
		t.Helper()
		_, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "data:key", Value: &pb.Vertex_String_{String_: value}}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	point := func() (string, [32]byte) {
		t.Helper()
		stats, err := b.BackupNow(ctx)
		if err != nil || stats.Bytes <= 0 {
			t.Fatalf("backup %+v %v", stats, err)
		}
		points, err := b.graphPoints(ctx)
		if err != nil || len(points) == 0 {
			t.Fatal(points, err)
		}
		dir := filepath.Join(cfg.Dir, points[0].name)
		raw, err := os.ReadFile(filepath.Join(dir, cutFile))
		if err != nil {
			t.Fatal(err)
		}
		return dir, sha256.Sum256(raw)
	}
	put("first")
	old, firstHash := point()
	put("second")
	newest, newHash := point()
	if _, err = b.PreviewRetention(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if firstHash == newHash {
		t.Fatal("same digest for different cuts")
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err = RestoreGraphStore(ctx, old, target, cfg.InstanceID, node, newHash); err == nil {
		t.Fatal("wrong independent digest accepted")
	}
	if err = RestoreGraphStore(ctx, newest, target, cfg.InstanceID, hlc.NodeID{8}, newHash); !errors.Is(err, graphstore.ErrNodeMismatch) {
		t.Fatal(err)
	}
	if err = RestoreGraphStoreOnStartup(ctx, target, node, cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := graphstore.OpenExisting(ctx, target, node)
	if err != nil {
		t.Fatal(err)
	}
	v, err := restored.Read()
	if err != nil {
		t.Fatal(err)
	}
	row, ok, err := rows.GetVertex(v.Snapshot, v.Generation, "data:key")
	if err != nil || !ok || row.Value.GetString_() != "second" {
		t.Fatalf("restored vertex %+v ok=%v err=%v", row, ok, err)
	}
	w, err := dataruntime.ReadTarpBackupWitness(node, v.Generation, v.Snapshot, cfg.NamespaceFormat)
	if err != nil || w.LocalSeq != 2 {
		t.Fatal(w, err)
	}
	v.Close()
	restored.Close()
	if err = RestoreGraphStore(ctx, newest, target, cfg.InstanceID, node, newHash); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(newest, cutFile))
	if err != nil {
		t.Fatal(err)
	}
	var tampered graphStoreCut
	if err = json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Rows[0] ^= 1
	corrupt := bytes.Replace(raw, []byte(`"Revision":`), []byte(`"Revision":1`), 1)
	if err := os.WriteFile(filepath.Join(newest, cutFile), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if preview, err := b.PreviewRetention(ctx, 1); err == nil && preview.PruneBackups > 0 {
		t.Fatal("retention counted an accidentally corrupted cut")
	}
	bad, err := marshalGraphCut(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(newest, cutFile), bad, 0600); err != nil {
		t.Fatal(err)
	}
	rejected := filepath.Join(t.TempDir(), "rejected")
	if err = RestoreGraphStore(ctx, newest, rejected, cfg.InstanceID, node, sha256.Sum256(bad)); err == nil {
		t.Fatal("mismatch with restored graph accepted")
	}
	if _, err = os.Lstat(rejected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(newest, cutFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	put("third")
	point()
	if _, err = os.Lstat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retention failed", err)
	}
	preview, err := b.PreviewRetention(ctx, 1)
	if err != nil || !preview.Complete || preview.ValidBackups != 2 || preview.PruneBackups != 1 || preview.PruneBytes == 0 {
		t.Fatalf("preview %+v, %v", preview, err)
	}
}
