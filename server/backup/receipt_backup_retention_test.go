package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReceiptBackupSetRetentionIsolationOrphansAndMonotonicIDs(t *testing.T) {
	t.Run("retention is exact and instance scoped", func(t *testing.T) {
		dir := t.TempDir()
		archive := wholeStateArchiveFixture(t)
		capture := producerBackupCapture(archive)
		sourceA := &receiptBackupSetSource{capture: capture}
		sourceB := &receiptBackupSetSource{capture: capture}
		a := newReceiptBackupSetTestBackupper(t, dir, "instance-a", 2, sourceA, archive.Policy)
		b := newReceiptBackupSetTestBackupper(t, dir, "instance-b", 1, sourceB, archive.Policy)
		a.now = func() time.Time { return time.Unix(0, 100).UTC() }
		b.now = func() time.Time { return time.Unix(0, 100).UTC() }
		for i := 0; i < 4; i++ {
			if _, err := a.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		aSets, err := a.collectReceiptBackupSets()
		if err != nil {
			t.Fatal(err)
		}
		bSets, err := b.collectReceiptBackupSets()
		if err != nil {
			t.Fatal(err)
		}
		if len(aSets) != 2 || len(bSets) != 1 {
			t.Fatalf("retained sets A/B = %d/%d, want 2/1", len(aSets), len(bSets))
		}
		for _, set := range aSets {
			if strings.Contains(filepath.Base(set.manifestPath), receiptBackupSetScope("instance-b")) {
				t.Fatal("instance A selected instance B's committed set")
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3*(receiptBackupSetExpectedMemberCount+1) {
			t.Fatalf("shared directory file count = %d, want 9", len(entries))
		}
	})

	t.Run("retain zero keeps every valid set", func(t *testing.T) {
		archive := wholeStateArchiveFixture(t)
		source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
		b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "keep-all", 0, source, archive.Policy)
		b.now = func() time.Time { return time.Unix(0, 200).UTC() }
		for i := 0; i < 4; i++ {
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		sets, err := b.collectReceiptBackupSets()
		if err != nil || len(sets) != 4 {
			t.Fatalf("retain zero sets = %d, %v, want 4", len(sets), err)
		}
	})

	t.Run("invalid commit marker is neither counted nor pruned", func(t *testing.T) {
		archive := wholeStateArchiveFixture(t)
		source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
		b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "valid-only", 1, source, archive.Policy)
		b.now = func() time.Time { return time.Unix(0, 300).UTC() }
		invalid := filepath.Join(
			b.cfg.Dir,
			receiptBackupSetBase(b.cfg.InstanceID, 999)+receiptBackupSetManifestSuffix,
		)
		invalidMembers := []string{
			strings.TrimSuffix(invalid, receiptBackupSetManifestSuffix) + receiptBackupSetArchiveSuffix,
			strings.TrimSuffix(invalid, receiptBackupSetManifestSuffix) + receiptBackupSetWALCutSuffix,
		}
		if err := os.MkdirAll(b.cfg.Dir, receiptBackupSetDirectoryPerms); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(invalid, []byte("{}"), receiptBackupSetFilePermissions); err != nil {
			t.Fatal(err)
		}
		for _, path := range invalidMembers {
			if err := os.WriteFile(path, []byte("unproven"), receiptBackupSetFilePermissions); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 3; i++ {
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		sets, err := b.collectReceiptBackupSets()
		if err != nil || len(sets) != 1 {
			t.Fatalf("valid retained sets = %d, %v, want 1", len(sets), err)
		}
		if _, err := os.Stat(invalid); err != nil {
			t.Fatalf("invalid committed marker was pruned: %v", err)
		}
		for _, path := range invalidMembers {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("unproven invalid-set member was removed: %v", err)
			}
		}
	})

	t.Run("unproven orphans are preserved and ignored", func(t *testing.T) {
		dir := t.TempDir()
		archive := wholeStateArchiveFixture(t)
		source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
		b := newReceiptBackupSetTestBackupper(t, dir, "orphan-owner", 0, source, archive.Policy)
		ownBase := receiptBackupSetBase(b.cfg.InstanceID, 77)
		foreignBase := receiptBackupSetBase("other-owner", 77)
		ownPaths := []string{
			ownBase + receiptBackupSetArchiveSuffix,
			ownBase + receiptBackupSetWALCutSuffix,
			ownBase + receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix,
			ownBase + receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix,
		}
		foreignPaths := []string{
			foreignBase + receiptBackupSetArchiveSuffix,
			foreignBase + receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix,
			"unrecognized.keep",
		}
		for _, name := range append(append([]string(nil), ownPaths...), foreignPaths...) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("orphan"), receiptBackupSetFilePermissions); err != nil {
				t.Fatal(err)
			}
		}
		b.now = func() time.Time { return time.Unix(0, 50).UTC() }
		if _, err := b.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, name := range append(append([]string(nil), ownPaths...), foreignPaths...) {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("unproven orphan path %q was removed: %v", name, err)
			}
		}
		sets, err := b.collectReceiptBackupSets()
		if err != nil || len(sets) != 1 {
			t.Fatalf("orphan filtering found %+v: %v", sets, err)
		}
	})

	t.Run("IDs survive repeated backward time and restart", func(t *testing.T) {
		dir := t.TempDir()
		archive := wholeStateArchiveFixture(t)
		capture := producerBackupCapture(archive)
		source := &receiptBackupSetSource{capture: capture}
		b := newReceiptBackupSetTestBackupper(t, dir, "clock-owner", 0, source, archive.Policy)
		times := []time.Time{
			time.Unix(0, 100), time.Unix(0, 100),
			time.Unix(0, 90), time.Unix(0, 90),
			time.Unix(0, 100), time.Unix(0, 100),
		}
		var timeIndex int
		b.now = func() time.Time {
			got := times[timeIndex]
			timeIndex++
			return got.UTC()
		}
		for i := 0; i < 3; i++ {
			if _, err := b.BackupNow(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		restartedSource := &receiptBackupSetSource{capture: capture}
		restarted := newReceiptBackupSetTestBackupper(t, dir, "clock-owner", 0, restartedSource, archive.Policy)
		restarted.now = func() time.Time { return time.Unix(0, 1).UTC() }
		if _, err := restarted.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
		sets, err := restarted.collectReceiptBackupSets()
		if err != nil || len(sets) != 4 {
			t.Fatalf("monotonic sets = %+v, %v", sets, err)
		}
		for i, want := range []uint64{103, 102, 101, 100} {
			if sets[i].id != want {
				t.Fatalf("set IDs = %+v, want descending 103..100", sets)
			}
		}
	})
}

func TestReceiptBackupSetRetentionRemovesMarkerBeforeMembers(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
	b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "prune-owner", 0, source, archive.Policy)
	b.now = func() time.Time { return time.Unix(0, 900).UTC() }
	for i := 0; i < 2; i++ {
		if _, err := b.BackupNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var events []string
	baseFS := b.fs
	b.fs.remove = func(path string) error {
		events = append(events, "remove:"+filepath.Base(path))
		return baseFS.remove(path)
	}
	b.fs.syncDir = func(path string) error {
		events = append(events, "directory-sync")
		return baseFS.syncDir(path)
	}
	b.cfg.Retain = 1
	if err := b.pruneReceiptBackupSets(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 ||
		!strings.HasSuffix(events[0], receiptBackupSetManifestSuffix) ||
		events[1] != "directory-sync" ||
		!strings.HasSuffix(events[2], receiptBackupSetArchiveSuffix) ||
		!strings.HasSuffix(events[3], receiptBackupSetWALCutSuffix) ||
		events[4] != "directory-sync" {
		t.Fatalf("retention durability order = %v", events)
	}
	sets, err := b.collectReceiptBackupSets()
	if err != nil || len(sets) != 1 {
		t.Fatalf("retention left %d valid sets: %v", len(sets), err)
	}
}
