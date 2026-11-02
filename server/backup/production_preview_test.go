package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestRetentionPreviewOwnedValidCatalogAndNoMutation(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		t.Run(map[bool]string{false: "graph", true: "receipt"}[receipt], func(t *testing.T) {
			cfg := testConfig(t.TempDir())
			cfg.Retain = 0
			var b *Backupper
			if receipt {
				archive := wholeStateArchiveFixture(t)
				var err error
				b, err = NewReceipt(&fakeService{}, &receiptBackupSetSource{capture: producerBackupCapture(archive)}, archive.Policy, cfg, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				b = New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("test")}}, cfg, nil, nil)
			}
			for range 3 {
				if _, err := b.BackupNow(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			peerConfig := cfg
			peerConfig.InstanceID += "-other"
			peer := New(&fakeService{}, peerConfig, nil, nil)
			if receipt {
				var err error
				peer, err = NewReceipt(&fakeService{}, b.receiptSource, b.receiptPolicy, peerConfig, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := peer.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			newer := time.Now().Add(time.Hour)
			invalidName := b.fileName(newer)
			if receipt {
				invalidName = receiptBackupSetBase(cfg.InstanceID, uint64(newer.UnixNano())) + receiptBackupSetManifestSuffix
			}
			if err := os.WriteFile(filepath.Join(cfg.Dir, invalidName), []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg.Dir, "foreign-unowned.lbk"), []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := receiptBackupDirectoryState(t, cfg.Dir)
			preview, err := b.PreviewRetention(t.Context(), 1)
			if err != nil || !preview.Complete || preview.ValidBackups != 3 || preview.PruneBackups != 2 ||
				preview.ValidBytes <= preview.PruneBytes || !preview.AcknowledgementNeeded || preview.Fingerprint == "" {
				t.Fatalf("owned preview = %+v, %v", preview, err)
			}
			keepAll, err := b.PreviewRetention(t.Context(), 0)
			if err != nil || keepAll.PruneBackups != 0 || keepAll.PruneBytes != 0 || keepAll.AcknowledgementNeeded ||
				keepAll.Fingerprint != preview.Fingerprint {
				t.Fatalf("keep-all preview = %+v, %v", keepAll, err)
			}
			next := ProductionSnapshot{Generation: 1, Config: ProductionConfig{Enabled: true, Interval: time.Second + 13, Retain: 1}}
			if err := b.ApplyProduction(next); err != nil {
				t.Fatal(err)
			}
			if after := receiptBackupDirectoryState(t, cfg.Dir); !reflect.DeepEqual(before, after) {
				t.Fatal("preview or policy application modified backup files")
			}
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, err := b.PreviewRetention(t.Context(), 1)
			if err != nil || after.Generation != 1 || after.ValidBackups != 1 || after.PruneBackups != 0 {
				t.Fatalf("future pruning = %+v, %v", after, err)
			}
			if _, err := os.Stat(filepath.Join(cfg.Dir, invalidName)); err != nil {
				t.Fatalf("invalid recovery file was deleted: %v", err)
			}
			peerPreview, err := peer.PreviewRetention(t.Context(), 1)
			if err != nil || peerPreview.ValidBackups != 1 {
				t.Fatalf("prefix-related peer was pruned: %+v, %v", peerPreview, err)
			}
		})
	}
}

func TestRetentionPreviewBoundsAndFailures(t *testing.T) {
	empty, err := New(&fakeService{}, Config{}, nil, nil).PreviewRetention(t.Context(), 0)
	if err != nil || !empty.Complete || len(empty.Fingerprint) != 64 || empty.ValidBackups != 0 {
		t.Fatalf("known empty catalog = %+v, %v", empty, err)
	}
	b := New(&fakeService{frames: []*pb.BackupSnapshotResponse{vFrame("bounded")}}, testConfig(t.TempDir()), nil, nil)
	for range 2 {
		if _, err := b.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, limits := range []struct {
		bytes      int64
		candidates int
	}{{1, 10}, {productionPreviewMaxBytes, 1}} {
		preview, err := b.previewRetention(t.Context(), 1, limits.bytes, limits.candidates)
		if !errors.Is(err, ErrRetentionPreviewLimit) || preview.Complete || preview.Fingerprint != "" {
			t.Fatalf("bounded preview = %+v, %v", preview, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if preview, err := b.PreviewRetention(ctx, 1); !errors.Is(err, context.Canceled) || preview.Complete {
		t.Fatalf("cancelled preview = %+v, %v", preview, err)
	}
	if _, err := b.PreviewRetention(t.Context(), -1); err == nil {
		t.Fatal("invalid retain accepted")
	}
	baseFS := b.fs
	b.fs.readFile = func(path string, _ int64) ([]byte, error) {
		return nil, &os.PathError{Op: "read", Path: path, Err: os.ErrPermission}
	}
	if preview, err := b.PreviewRetention(t.Context(), 1); !errors.Is(err, os.ErrPermission) || preview.Complete {
		t.Fatalf("unreadable preview = %+v, %v", preview, err)
	}
	b.fs = baseFS
	if err := b.ApplyProduction(ProductionSnapshot{Generation: 1, Config: ProductionConfig{Interval: time.Hour, Retain: 0}}); err != nil {
		t.Fatalf("safe keep-all apply needs no preview: %v", err)
	}
}
