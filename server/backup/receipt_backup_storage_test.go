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
var errInjectedReceiptBackupCleanupRemove = errors.New("injected receipt backup cleanup remove failure")
var errInjectedReceiptBackupCleanupSync = errors.New("injected receipt backup cleanup sync failure")

type receiptBackupFaultPlan struct {
	operation, target string
	occurrence, seen  int
	after             bool
	cancel            context.CancelFunc
	mu                sync.Mutex
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
	b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, bool, error) {
		if err := plan.inject("create", path, false); err != nil {
			return nil, false, err
		}
		file, created, err := base.openExclusive(path, flag, mode)
		if err != nil {
			return nil, created, err
		}
		if err := plan.inject("create", path, true); err != nil {
			_ = file.Close()
			return nil, created, err
		}
		return &receiptBackupFaultFile{receiptBackupSyncFile: file, path: path, plan: plan}, created, nil
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
		{"member create", "create", receiptBackupSetArchiveSuffix, 1, false, false},
		{"ambiguous member create", "create", receiptBackupSetArchiveSuffix, 1, true, false},
		{"member write", "write", receiptBackupSetArchiveSuffix, 1, false, false},
		{"member sync", "file sync", receiptBackupSetArchiveSuffix, 1, false, false},
		{"member close", "close", receiptBackupSetArchiveSuffix, 1, true, false},
		{"member directory sync", "directory sync", "", 1, false, false},
		{"manifest create", "create", receiptBackupSetManifestSuffix, 1, false, false},
		{"ambiguous manifest create", "create", receiptBackupSetManifestSuffix, 1, true, false},
		{"manifest write", "write", receiptBackupSetManifestSuffix, 1, false, false},
		{"manifest sync", "file sync", receiptBackupSetManifestSuffix, 1, false, false},
		{"manifest close", "close", receiptBackupSetManifestSuffix, 1, true, false},
		{"final directory sync", "directory sync", "", 2, false, false},
		{"cancel after member close", "close", receiptBackupSetArchiveSuffix, 1, true, true},
		{"cancel after member directory sync", "directory sync", "", 1, true, true},
		{"cancel after manifest close", "close", receiptBackupSetManifestSuffix, 1, true, true},
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

func TestReceiptBackupSetCleanupRequiresDurableManifestRetirement(t *testing.T) {
	tests := []struct {
		name          string
		cleanupErr    error
		removeFailure bool
	}{
		{"manifest remove fails", errInjectedReceiptBackupCleanupRemove, true},
		{"manifest removal sync fails", errInjectedReceiptBackupCleanupSync, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := wholeStateArchiveFixture(t)
			source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
			b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "cleanup-owner", 0, source, archive.Policy)
			b.now = func() time.Time { return time.Unix(0, 800).UTC() }
			baseFS := b.fs
			var syncCalls int
			b.fs.syncDir = func(path string) error {
				syncCalls++
				switch syncCalls {
				case 2:
					return errInjectedReceiptBackupFS
				case 3:
					if !tc.removeFailure {
						return tc.cleanupErr
					}
				}
				return baseFS.syncDir(path)
			}
			if tc.removeFailure {
				b.fs.remove = func(path string) error {
					if strings.HasSuffix(path, receiptBackupSetManifestSuffix) {
						return tc.cleanupErr
					}
					return baseFS.remove(path)
				}
			}

			_, err := b.BackupNow(t.Context())
			if !errors.Is(err, errInjectedReceiptBackupFS) || !errors.Is(err, tc.cleanupErr) {
				t.Fatalf("backup/cleanup errors = %v", err)
			}
			base := filepath.Join(b.cfg.Dir, receiptBackupSetBase(b.cfg.InstanceID, 800))
			manifestPath := base + receiptBackupSetManifestSuffix
			memberPaths := []string{
				base + receiptBackupSetArchiveSuffix,
				base + receiptBackupSetWALCutSuffix,
			}
			for _, path := range memberPaths {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("member removed before durable manifest retirement: %v", err)
				}
			}
			if tc.removeFailure {
				if _, err := b.loadReceiptBackupSet(manifestPath); err != nil {
					t.Fatalf("failed marker removal did not preserve a complete set: %v", err)
				}
			} else if _, err := os.Stat(manifestPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("manifest after failed cleanup sync = %v, want absent", err)
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
			b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, bool, error) {
				if path == target {
					if err := os.WriteFile(path, foreign, receiptBackupSetFilePermissions); err != nil {
						return nil, false, err
					}
				}
				return baseFS.openExclusive(path, flag, mode)
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
			entries, err := os.ReadDir(b.cfg.Dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if filepath.Join(b.cfg.Dir, entry.Name()) != target {
					t.Fatalf("raced attempt left nonforeign path %q", entry.Name())
				}
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
	b.fs.openExclusive = func(path string, flag int, mode os.FileMode) (receiptBackupSyncFile, bool, error) {
		record("open:" + filepath.Base(path))
		file, created, err := baseFS.openExclusive(path, flag, mode)
		if err != nil {
			return nil, created, err
		}
		return &receiptBackupRecordingFile{receiptBackupSyncFile: file, path: path, record: record}, created, nil
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
	archiveOpen := index("open", receiptBackupSetArchiveSuffix)
	archiveWrite := index("write", receiptBackupSetArchiveSuffix)
	archiveSync := index("file-sync", receiptBackupSetArchiveSuffix)
	archiveClose := index("close", receiptBackupSetArchiveSuffix)
	cutOpen := index("open", receiptBackupSetWALCutSuffix)
	cutWrite := index("write", receiptBackupSetWALCutSuffix)
	cutSync := index("file-sync", receiptBackupSetWALCutSuffix)
	cutClose := index("close", receiptBackupSetWALCutSuffix)
	manifestOpen := index("open", receiptBackupSetManifestSuffix)
	manifestWrite := index("write", receiptBackupSetManifestSuffix)
	manifestSync := index("file-sync", receiptBackupSetManifestSuffix)
	manifestClose := index("close", receiptBackupSetManifestSuffix)
	var directorySyncs []int
	for i, event := range events {
		if event == "directory-sync" {
			directorySyncs = append(directorySyncs, i)
		}
	}
	if archiveOpen < 0 || archiveWrite <= archiveOpen ||
		archiveSync <= archiveWrite || archiveClose <= archiveSync ||
		cutOpen <= archiveClose || cutWrite <= cutOpen ||
		cutSync <= cutWrite || cutClose <= cutSync ||
		len(directorySyncs) != 2 ||
		directorySyncs[0] <= cutClose ||
		manifestOpen <= directorySyncs[0] ||
		manifestWrite <= manifestOpen || manifestSync <= manifestWrite ||
		manifestClose <= manifestSync ||
		directorySyncs[1] <= manifestClose {
		t.Fatalf("receipt backup-set durability order = %v", events)
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
