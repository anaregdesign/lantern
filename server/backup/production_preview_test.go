package backup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRetentionPreviewOwnedValidCatalogAndNoMutation(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Retain = 0
	b := testBackupper(t, cfg)
	foreign := testBackupper(t, Config{Dir: cfg.Dir, InstanceID: "foreign"})
	if _, err := foreign.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := b.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(cfg.Dir, graphPointPrefix(cfg.InstanceID)+graphPointID(2)), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.PreviewRetention(t.Context(), 1)
	if err != nil || !p.Complete || p.ValidBackups != 3 || p.PruneBackups != 2 || p.PruneBytes == 0 || p.Fingerprint == "" {
		t.Fatal(p, err)
	}
	after, err := os.ReadDir(cfg.Dir)
	if err != nil || len(before) != len(after) {
		t.Fatal("mutated catalog", err)
	}
	for _, limits := range []struct {
		bytes int64
		count int
	}{{1, 10}, {1 << 30, 1}} {
		p, err := b.previewRetention(t.Context(), 1, limits.bytes, limits.count)
		if !errors.Is(err, ErrRetentionPreviewLimit) || p.Complete {
			t.Fatal(p, err)
		}
	}
}
