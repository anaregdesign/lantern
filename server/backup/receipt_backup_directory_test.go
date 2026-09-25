package backup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type receiptBackupRepeatingReadDir struct {
	entry     os.DirEntry
	remaining int
}

func (d *receiptBackupRepeatingReadDir) ReadDir(n int) ([]os.DirEntry, error) {
	if d.remaining == 0 {
		return nil, io.EOF
	}
	count := min(n, d.remaining)
	entries := make([]os.DirEntry, count)
	for i := range entries {
		entries[i] = d.entry
	}
	d.remaining -= count
	return entries, nil
}

func (*receiptBackupRepeatingReadDir) Close() error {
	return nil
}

func canonicalReceiptBackupTestPath(t *testing.T, path string) string {
	t.Helper()
	components, err := receiptBackupDirectoryComponents(path)
	if err != nil {
		t.Fatal(err)
	}
	return components[len(components)-1]
}

func TestEnsureReceiptBackupDirectoryCreatesDurablePath(t *testing.T) {
	root := canonicalReceiptBackupTestPath(t, t.TempDir())
	target := filepath.Join(root, "one", "two", "backups")
	archive := wholeStateArchiveFixture(t)
	b := newReceiptBackupSetTestBackupper(
		t,
		target,
		"durable-directory-owner",
		0,
		&receiptBackupSetSource{capture: producerBackupCapture(archive)},
		archive.Policy,
	)
	baseFS := b.fs
	var events []string
	b.fs.mkdir = func(path string, mode os.FileMode) error {
		events = append(events, "mkdir:"+path)
		return baseFS.mkdir(path, mode)
	}
	b.fs.syncDir = func(path string) error {
		events = append(events, "sync:"+path)
		return baseFS.syncDir(path)
	}

	if err := b.ensureReceiptBackupDirectory(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"sync:" + filepath.Dir(root),
		"mkdir:" + filepath.Join(root, "one"),
		"sync:" + root,
		"mkdir:" + filepath.Join(root, "one", "two"),
		"sync:" + filepath.Join(root, "one"),
		"mkdir:" + target,
		"sync:" + filepath.Join(root, "one", "two"),
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("durable directory events = %v, want %v", events, want)
	}
	for _, path := range []string{
		filepath.Join(root, "one"),
		filepath.Join(root, "one", "two"),
		target,
	} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("created path %s = %+v, %v", path, info, err)
		}
	}
	if err := b.ensureReceiptBackupDirectory(); err != nil {
		t.Fatal(err)
	}
	want = append(want, "sync:"+filepath.Join(root, "one", "two"))
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("existing durable path was not re-certified: %v", events)
	}
}

func TestReceiptBackupSetPublishesIntoNewNestedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "one", "two", "backups")
	archive := wholeStateArchiveFixture(t)
	b := newReceiptBackupSetTestBackupper(
		t,
		dir,
		"new-directory-owner",
		0,
		&receiptBackupSetSource{capture: producerBackupCapture(archive)},
		archive.Policy,
	)
	b.now = func() time.Time { return time.Unix(0, 50).UTC() }
	if _, err := b.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SetID != 50 {
		t.Fatalf("first nested-directory set ID = %d, want 50", evidence.SetID)
	}
}

func TestEnsureReceiptBackupDirectoryFailsClosedAtParentSyncBoundaries(t *testing.T) {
	for syncIndex := 0; syncIndex < 4; syncIndex++ {
		for _, after := range []bool{false, true} {
			position := "before"
			if after {
				position = "after"
			}
			t.Run(position+" parent sync "+string(rune('1'+syncIndex)), func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, "one", "two", "backups")
				archive := wholeStateArchiveFixture(t)
				source := &receiptBackupSetSource{capture: producerBackupCapture(archive)}
				b := newReceiptBackupSetTestBackupper(
					t,
					target,
					"directory-fault-owner",
					0,
					source,
					archive.Policy,
				)
				baseFS := b.fs
				var syncCalls int
				b.fs.syncDir = func(path string) error {
					if syncCalls != syncIndex {
						syncCalls++
						return baseFS.syncDir(path)
					}
					syncCalls++
					if !after {
						return errInjectedReceiptBackupFS
					}
					if err := baseFS.syncDir(path); err != nil {
						return err
					}
					return errInjectedReceiptBackupFS
				}

				stats, err := b.BackupNow(t.Context())
				if !errors.Is(err, errInjectedReceiptBackupFS) || stats != (Stats{}) {
					t.Fatalf("backup at parent-sync fault = %+v, %v", stats, err)
				}
				if syncCalls != syncIndex+1 {
					t.Fatalf("parent sync calls = %d, want %d", syncCalls, syncIndex+1)
				}
				walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if strings.HasPrefix(entry.Name(), receiptBackupSetPrefix) {
						t.Fatalf("directory durability failure created backup path %s", path)
					}
					return nil
				})
				if walkErr != nil {
					t.Fatal(walkErr)
				}

				restarted := newReceiptBackupSetTestBackupper(
					t,
					target,
					"directory-fault-owner",
					0,
					source,
					archive.Policy,
				)
				if _, err := restarted.BackupNow(t.Context()); err != nil {
					t.Fatalf("retry after parent-sync fault: %v", err)
				}
				if _, err := LoadLatestReceiptBackupSet(target, restarted.cfg.InstanceID); err != nil {
					t.Fatalf("discover retry after parent-sync fault: %v", err)
				}
			})
		}
	}
}

func TestEnsureReceiptBackupDirectoryRejectsNonDirectoryComponents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func(*testing.T, string, string)
	}{
		{
			name: "file",
			create: func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			create: func(t *testing.T, path, target string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(target, "backups"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			real := filepath.Join(root, "real")
			if err := os.Mkdir(real, 0o755); err != nil {
				t.Fatal(err)
			}
			component := filepath.Join(root, "component")
			tc.create(t, component, real)
			archive := wholeStateArchiveFixture(t)
			b := newReceiptBackupSetTestBackupper(
				t,
				filepath.Join(component, "backups"),
				"invalid-directory-owner",
				0,
				&receiptBackupSetSource{capture: producerBackupCapture(archive)},
				archive.Policy,
			)
			if err := b.ensureReceiptBackupDirectory(); err == nil {
				t.Fatal("non-directory backup path component was accepted")
			}
		})
	}
}

func TestReceiptBackupDirectoryScansAreBounded(t *testing.T) {
	entryDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(entryDir, "foreign"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(entryDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		run  func(*Backupper) error
	}{
		{
			name: "latest discovery",
			run: func(b *Backupper) error {
				_, err := b.loadLatestReceiptBackupSet()
				return err
			},
		},
		{
			name: "ID allocation",
			run: func(b *Backupper) error {
				_, err := b.nextReceiptBackupSetID(time.Unix(0, 1))
				return err
			},
		},
		{
			name: "retention",
			run:  func(b *Backupper) error { return b.pruneReceiptBackupSets() },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := wholeStateArchiveFixture(t)
			b := newReceiptBackupSetTestBackupper(
				t,
				t.TempDir(),
				"bounded-scan-owner",
				1,
				&receiptBackupSetSource{capture: producerBackupCapture(archive)},
				archive.Policy,
			)
			b.fs.openDir = func(string) (receiptBackupReadDir, error) {
				return &receiptBackupRepeatingReadDir{
					entry:     entries[0],
					remaining: receiptBackupDirectoryMaxEntries + 1,
				}, nil
			}
			if err := tc.run(b); !errors.Is(err, errReceiptBackupDirectoryLimit) {
				t.Fatalf("entry-limit error = %v, want %v", err, errReceiptBackupDirectoryLimit)
			}
		})
	}
}

func TestReceiptBackupDirectoryScanReportsReadAndCloseErrors(t *testing.T) {
	entryDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(entryDir, "foreign"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(entryDir)
	if err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("injected directory read failure")
	closeErr := errors.New("injected directory close failure")
	archive := wholeStateArchiveFixture(t)
	b := newReceiptBackupSetTestBackupper(
		t,
		t.TempDir(),
		"scan-errors-owner",
		0,
		&receiptBackupSetSource{capture: producerBackupCapture(archive)},
		archive.Policy,
	)
	b.fs.openDir = func(string) (receiptBackupReadDir, error) {
		return &receiptBackupStaticReadDir{
			entries:  entries,
			readErr:  readErr,
			closeErr: closeErr,
		}, nil
	}
	var visited int
	err = b.scanReceiptBackupDirectory(func(os.DirEntry) error {
		visited++
		return nil
	})
	if visited != 1 {
		t.Fatalf("visited entries = %d, want 1", visited)
	}
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("scan error = %v, want joined read and close errors", err)
	}
}

func TestReceiptBackupDirectoryScanIgnoresManyForeignAndOrphanEntries(t *testing.T) {
	dir := t.TempDir()
	archive := wholeStateArchiveFixture(t)
	b := newReceiptBackupSetTestBackupper(
		t,
		dir,
		"many-entries-owner",
		0,
		&receiptBackupSetSource{capture: producerBackupCapture(archive)},
		archive.Policy,
	)
	valid := writeReceiptBackupSetAt(t, b, 100)
	for i := 0; i < 512; i++ {
		if err := os.WriteFile(
			filepath.Join(dir, "foreign-"+receiptBackupSetIDString(uint64(i+1))),
			[]byte("foreign"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	orphanBase := filepath.Join(dir, receiptBackupSetBase(b.cfg.InstanceID, 900))
	for _, suffix := range []string{
		receiptBackupSetArchiveSuffix,
		receiptBackupSetWALCutSuffix + receiptBackupSetTempSuffix,
		receiptBackupSetRetiredCatalogSuffix,
	} {
		if err := os.WriteFile(orphanBase+suffix, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SetID != valid.id {
		t.Fatalf("many-entry discovery selected %d, want %d", evidence.SetID, valid.id)
	}
	obsoleteV1Name, obsoleteV1Raw := historicalReceiptBackupSetManifest(t, b.cfg.InstanceID, 950)
	if err := os.WriteFile(
		filepath.Join(dir, obsoleteV1Name),
		obsoleteV1Raw,
		receiptBackupSetFilePermissions,
	); err != nil {
		t.Fatal(err)
	}
	obsoleteV2Name := obsoleteReceiptBackupSetMarker(
		receiptBackupSetUnsupportedPrefixes[1],
		b.cfg.InstanceID,
		960,
	)
	if err := os.WriteFile(
		filepath.Join(dir, obsoleteV2Name),
		[]byte(`{"format":"lantern-receipt-backup-set","version":2}`),
		receiptBackupSetFilePermissions,
	); err != nil {
		t.Fatal(err)
	}
	next, err := b.nextReceiptBackupSetID(time.Unix(0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if next != 961 {
		t.Fatalf("ID after owned obsolete markers = %d, want 961", next)
	}
}
