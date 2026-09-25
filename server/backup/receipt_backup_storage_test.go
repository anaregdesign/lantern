package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var errInjectedReceiptBackupFS = errors.New("injected receipt backup filesystem failure")

type receiptBackupFaultPlan struct {
	operation  string
	target     string
	occurrence int
	after      bool
	cancel     context.CancelFunc
	mu         sync.Mutex
	seen       int
}

func (p *receiptBackupFaultPlan) inject(operation, path string, after bool) error {
	if p.operation != operation || p.after != after || !strings.Contains(filepath.Base(path), p.target) {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen++
	if p.seen != p.occurrence {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
		return nil
	}
	return errInjectedReceiptBackupFS
}

type receiptBackupFaultFile struct {
	receiptBackupSyncFile
	path string
	plan *receiptBackupFaultPlan
}

func (f *receiptBackupFaultFile) Write(raw []byte) (int, error) {
	if err := f.plan.inject("write", f.path, false); err != nil {
		return 0, err
	}
	n, err := f.receiptBackupSyncFile.Write(raw)
	if err == nil {
		err = f.plan.inject("write", f.path, true)
	}
	return n, err
}

func (f *receiptBackupFaultFile) Sync() error {
	if err := f.plan.inject("file sync", f.path, false); err != nil {
		return err
	}
	err := f.receiptBackupSyncFile.Sync()
	if err == nil {
		err = f.plan.inject("file sync", f.path, true)
	}
	return err
}

func (f *receiptBackupFaultFile) Close() error {
	err := f.receiptBackupSyncFile.Close()
	if err == nil {
		err = f.plan.inject("close", f.path, true)
	}
	return err
}

func installReceiptBackupFaultPlan(b *Backupper, plan *receiptBackupFaultPlan) {
	base := b.fs
	b.fs.mkdirAll = func(path string, mode os.FileMode) error {
		if err := plan.inject("mkdir", path, false); err != nil {
			return err
		}
		return base.mkdirAll(path, mode)
	}
	b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, error) {
		if err := plan.inject("create", path, false); err != nil {
			return nil, err
		}
		file, err := base.openExclusive(path, flag, mode)
		if err != nil {
			return nil, err
		}
		return &receiptBackupFaultFile{receiptBackupSyncFile: file, path: path, plan: plan}, nil
	}
	b.fs.renameNoReplace = func(oldPath, newPath string) (bool, error) {
		if err := plan.inject("rename", newPath, false); err != nil {
			return false, err
		}
		published, err := base.renameNoReplace(oldPath, newPath)
		if err == nil {
			err = plan.inject("rename", newPath, true)
		}
		return published, err
	}
	b.fs.syncDir = func(path string) error {
		if err := plan.inject("directory sync", path, false); err != nil {
			return err
		}
		err := base.syncDir(path)
		if err == nil {
			err = plan.inject("directory sync", path, true)
		}
		return err
	}
}

func TestReceiptBackupSetFaultsAndCancellationNeverCommitPartialSet(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		target     string
		occurrence int
		after      bool
		cancel     bool
	}{
		{"mkdir", "mkdir", "", 1, false, false},
		{"member create", "create", receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix, 1, false, false},
		{"member write", "write", receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix, 1, false, false},
		{"member sync", "file sync", receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix, 1, false, false},
		{"member close", "close", receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix, 1, true, false},
		{"member rename", "rename", receiptBackupSetWALCutSuffix, 1, false, false},
		{"ambiguous member rename", "rename", receiptBackupSetWALCutSuffix, 1, true, false},
		{"member directory sync", "directory sync", "", 1, false, false},
		{"manifest create", "create", receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, 1, false, false},
		{"manifest write", "write", receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, 1, false, false},
		{"manifest sync", "file sync", receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, 1, false, false},
		{"manifest close", "close", receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, 1, true, false},
		{"manifest rename", "rename", receiptBackupSetManifestSuffix, 1, false, false},
		{"ambiguous manifest rename", "rename", receiptBackupSetManifestSuffix, 1, true, false},
		{"final directory sync", "directory sync", "", 2, false, false},
		{"cancel after member close", "close", receiptBackupSetArchiveSuffix + receiptBackupSetTempSuffix, 1, true, true},
		{"cancel after member rename", "rename", receiptBackupSetWALCutSuffix, 1, true, true},
		{"cancel after member directory sync", "directory sync", "", 1, true, true},
		{"cancel after manifest close", "close", receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix, 1, true, true},
		{"cancel after manifest rename", "rename", receiptBackupSetManifestSuffix, 1, true, true},
		{"cancel after final directory sync", "directory sync", "", 2, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := wholeStateArchiveFixture(t)
			source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
			b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "fault-owner", 0, source, archive.Policy)
			if err := os.MkdirAll(b.cfg.Dir, receiptBackupSetDirectoryPerms); err != nil {
				t.Fatal(err)
			}
			preserved := []string{
				receiptBackupSetBase("foreign-owner", 1) + receiptBackupSetArchiveSuffix,
				"unrecognized.keep",
			}
			for _, name := range preserved {
				if err := os.WriteFile(
					filepath.Join(b.cfg.Dir, name),
					[]byte("preserve"),
					receiptBackupSetFilePermissions,
				); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			plan := &receiptBackupFaultPlan{
				operation: tc.operation, target: tc.target, occurrence: tc.occurrence, after: tc.after,
			}
			if tc.cancel {
				plan.cancel = cancel
			}
			installReceiptBackupFaultPlan(b, plan)
			_, err := b.BackupNow(ctx)
			if tc.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled backup error = %v, want context.Canceled", err)
				}
			} else if !errors.Is(err, errInjectedReceiptBackupFS) {
				t.Fatalf("faulted backup error = %v, want injected error", err)
			}
			if plan.seen < tc.occurrence {
				t.Fatalf("fault stage was not reached: seen %d, want %d", plan.seen, tc.occurrence)
			}
			if source.calls.Load() != 1 {
				t.Fatalf("faulted backup source calls = %d, want 1", source.calls.Load())
			}
			sets, collectErr := b.collectReceiptBackupSets()
			if collectErr != nil || len(sets) != 0 {
				t.Fatalf("failed backup left complete sets %+v: %v", sets, collectErr)
			}
			entries, readErr := os.ReadDir(b.cfg.Dir)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatal(readErr)
			}
			for _, entry := range entries {
				if _, _, _, own := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID); own {
					t.Fatalf("failed attempt left owned path %q", entry.Name())
				}
			}
			for _, name := range preserved {
				if _, err := os.Stat(filepath.Join(b.cfg.Dir, name)); err != nil {
					t.Fatalf("failed attempt removed foreign path %q: %v", name, err)
				}
			}
		})
	}
}

func TestReceiptBackupSetNeverClobbersRacedDestination(t *testing.T) {
	for _, targetSuffix := range []string{receiptBackupSetArchiveSuffix, receiptBackupSetManifestSuffix} {
		t.Run(targetSuffix, func(t *testing.T) {
			archive := wholeStateArchiveFixture(t)
			source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
			b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "raced-owner", 0, source, archive.Policy)
			b.now = func() time.Time { return time.Unix(0, 700).UTC() }
			target := filepath.Join(b.cfg.Dir, receiptBackupSetBase(b.cfg.InstanceID, 700)+targetSuffix)
			foreign := []byte("foreign destination")
			baseFS := b.fs
			b.fs.renameNoReplace = func(oldPath, newPath string) (bool, error) {
				if newPath == target {
					if err := os.WriteFile(newPath, foreign, receiptBackupSetFilePermissions); err != nil {
						return false, err
					}
				}
				return baseFS.renameNoReplace(oldPath, newPath)
			}

			if _, err := b.BackupNow(t.Context()); !errors.Is(err, os.ErrExist) {
				t.Fatalf("raced destination error = %v, want os.ErrExist", err)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("foreign destination was removed: %v", err)
			}
			if !bytes.Equal(got, foreign) {
				t.Fatalf("foreign destination was overwritten: %q", got)
			}
			sets, err := b.collectReceiptBackupSets()
			if err != nil || len(sets) != 0 {
				t.Fatalf("raced attempt left complete sets %+v: %v", sets, err)
			}
		})
	}
}

type receiptBackupRecordingFile struct {
	receiptBackupSyncFile
	path   string
	record func(string)
}

func (f *receiptBackupRecordingFile) Write(raw []byte) (int, error) {
	f.record("write:" + filepath.Base(f.path))
	return f.receiptBackupSyncFile.Write(raw)
}

func (f *receiptBackupRecordingFile) Sync() error {
	f.record("file-sync:" + filepath.Base(f.path))
	return f.receiptBackupSyncFile.Sync()
}

func (f *receiptBackupRecordingFile) Close() error {
	f.record("close:" + filepath.Base(f.path))
	return f.receiptBackupSyncFile.Close()
}

func TestReceiptBackupSetCommitsManifestLastWithDurableOrdering(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
	b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "ordered-owner", 0, source, archive.Policy)
	baseFS := b.fs
	var events []string
	record := func(event string) { events = append(events, event) }
	b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, error) {
		record("open:" + filepath.Base(path))
		file, err := baseFS.openExclusive(path, flag, mode)
		if err != nil {
			return nil, err
		}
		return &receiptBackupRecordingFile{receiptBackupSyncFile: file, path: path, record: record}, nil
	}
	b.fs.renameNoReplace = func(oldPath, newPath string) (bool, error) {
		record("rename:" + filepath.Base(newPath))
		return baseFS.renameNoReplace(oldPath, newPath)
	}
	b.fs.syncDir = func(path string) error {
		record("directory-sync")
		return baseFS.syncDir(path)
	}
	if _, err := b.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	index := func(operation, suffix string) int {
		for i, event := range events {
			if strings.HasPrefix(event, operation+":") && strings.HasSuffix(event, suffix) {
				return i
			}
		}
		return -1
	}
	archiveClose := index("close", receiptBackupSetArchiveSuffix+receiptBackupSetTempSuffix)
	archiveRename := index("rename", receiptBackupSetArchiveSuffix)
	cutClose := index("close", receiptBackupSetWALCutSuffix+receiptBackupSetTempSuffix)
	cutRename := index("rename", receiptBackupSetWALCutSuffix)
	manifestOpen := index("open", receiptBackupSetManifestSuffix+receiptBackupSetTempSuffix)
	manifestClose := index("close", receiptBackupSetManifestSuffix+receiptBackupSetTempSuffix)
	manifestRename := index("rename", receiptBackupSetManifestSuffix)
	var directorySyncs []int
	for i, event := range events {
		if event == "directory-sync" {
			directorySyncs = append(directorySyncs, i)
		}
	}
	if archiveClose < 0 || archiveRename <= archiveClose ||
		cutClose < 0 || cutRename <= cutClose ||
		len(directorySyncs) != 2 ||
		directorySyncs[0] <= archiveRename || directorySyncs[0] <= cutRename ||
		manifestOpen <= directorySyncs[0] ||
		manifestClose <= manifestOpen ||
		manifestRename <= manifestClose ||
		directorySyncs[1] <= manifestRename {
		t.Fatalf("receipt backup-set durability order = %v", events)
	}
}

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
		if err := os.MkdirAll(b.cfg.Dir, receiptBackupSetDirectoryPerms); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(invalid, []byte("{}"), receiptBackupSetFilePermissions); err != nil {
			t.Fatal(err)
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
	})

	t.Run("only own safe orphans are removed", func(t *testing.T) {
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
		for _, name := range ownPaths {
			if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("own orphan %q remains: %v", name, err)
			}
		}
		for _, name := range foreignPaths {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("foreign/unrecognized path %q was removed: %v", name, err)
			}
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

func TestReceiptBackupSetSerializesConcurrentAttempts(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
	start := make(chan struct{})
	source.hook = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
			return nil
		}
	}
	b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "serialized-owner", 0, source, archive.Policy)
	b.now = func() time.Time { return time.Unix(0, 500).UTC() }
	const attempts = 8
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := b.BackupNow(t.Context())
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if source.calls.Load() != attempts || source.max.Load() != 1 {
		t.Fatalf("serialized source calls/max active = %d/%d, want %d/1",
			source.calls.Load(), source.max.Load(), attempts)
	}
	sets, err := b.collectReceiptBackupSets()
	if err != nil || len(sets) != attempts {
		t.Fatalf("serialized committed sets = %d, %v, want %d", len(sets), err, attempts)
	}
	for i := 1; i < len(sets); i++ {
		if sets[i-1].id != sets[i].id+1 {
			t.Fatalf("concurrent IDs are not contiguous and unique: %+v", sets)
		}
	}
}
