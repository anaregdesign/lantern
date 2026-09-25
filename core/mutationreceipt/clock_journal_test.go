package mutationreceipt

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func clockJournalFixture(t *testing.T) (string, Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipt.wal")
	config := Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 4, MaxBytes: 1000}
	return path, config
}

func clockJournalPolicy(t *testing.T, config Config) [32]byte {
	t.Helper()
	store, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return store.PolicyFingerprint()
}

func TestSyncClockJournalDirectory(t *testing.T) {
	if err := syncClockJournalDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := syncClockJournalDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("sync missing clock journal directory succeeded")
	}
}

func TestClockJournalRestoresAbortedAndLookupHighWater(t *testing.T) {
	path, config := clockJournalFixture(t)
	policy := clockJournalPolicy(t, config)
	j, err := CreateClockJournal(path, config.Epoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewWithClockHighWaterSink(config, j.Advance)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin(testStart)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	id := testIntent(t, 1, testStart, GroupID{1}, 0, 1).ID
	forward := testStart.Add(time.Hour + time.Millisecond)
	if status, _, err := store.Lookup(id, forward); err != nil || status != NoLongerProvable {
		t.Fatalf("forward Lookup = %v, %v", status, err)
	}
	if got := j.HighWaterMillis(); got != forward.UnixMilli() {
		t.Fatalf("durable high-water = %d, want %d", got, forward.UnixMilli())
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := ResumeClockJournal(path, config.Epoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if got := recovered.HighWaterMillis(); got != forward.UnixMilli() {
		t.Fatalf("recovered high-water = %d, want %d", got, forward.UnixMilli())
	}
	if stale, err := NewWithClockHighWaterSink(config, recovered.Advance); stale != nil ||
		!errors.Is(err, ErrHighWaterPersistence) || !errors.Is(err, ErrClockJournalRollback) {
		t.Fatalf("Store with forgotten journal high-water = %p, %v", stale, err)
	}
	config.ClockHighWater = time.UnixMilli(recovered.HighWaterMillis())
	restarted, err := NewWithClockHighWaterSink(config, recovered.Advance)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := restarted.Lookup(id, testStart); err != nil || status != NoLongerProvable {
		t.Fatalf("backward-clock Lookup = %v, %v", status, err)
	}
	if err := recovered.Advance(forward.UnixMilli() + 1); err != nil {
		t.Fatalf("append after resume: %v", err)
	}
}

func TestClockJournalSurvivesProcessExit(t *testing.T) {
	config := Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 4, MaxBytes: 1000}
	policy := clockJournalPolicy(t, config)
	if path := os.Getenv("LANTERN_TEST_CLOCK_JOURNAL_CHILD_PATH"); path != "" {
		j, err := ResumeClockJournal(path, config.Epoch, policy)
		if err != nil || j.Advance(testStart.UnixMilli()) != nil {
			os.Exit(2)
		}
		os.Exit(0) // simulate process exit without Close after a synced advance
	}
	path := filepath.Join(t.TempDir(), "receipt.wal")
	j, err := CreateClockJournal(path, config.Epoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClockJournalSurvivesProcessExit$")
	child.Env = append(os.Environ(), "LANTERN_TEST_CLOCK_JOURNAL_CHILD_PATH="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child clock advance: %v: %s", err, output)
	}
	recovered, err := ResumeClockJournal(path, config.Epoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if got := recovered.HighWaterMillis(); got != testStart.UnixMilli() {
		t.Fatalf("post-exit high-water = %d, want %d", got, testStart.UnixMilli())
	}
}

func TestClockJournalRejectsMissingExistingAndMismatchedBinding(t *testing.T) {
	path, config := clockJournalFixture(t)
	policy := clockJournalPolicy(t, config)
	if j, err := ResumeClockJournal(path, config.Epoch, policy); j != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing sidecar = %p, %v", j, err)
	}
	j, err := CreateClockJournal(path, config.Epoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Advance(testStart.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := j.Advance(testStart.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path + ".clock")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := CreateClockJournal(path, config.Epoch, policy); duplicate != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing sidecar create = %p, %v", duplicate, err)
	}
	if changed, err := ResumeClockJournal(path, Epoch{2}, policy); changed != nil || !errors.Is(err, ErrClockJournalBinding) {
		t.Fatalf("changed epoch = %p, %v", changed, err)
	}
	policy[0] ^= 1
	if changed, err := ResumeClockJournal(path, config.Epoch, policy); changed != nil || !errors.Is(err, ErrClockJournalBinding) {
		t.Fatalf("changed policy = %p, %v", changed, err)
	}
	policy[0] ^= 1
	otherPath := filepath.Join(filepath.Dir(path), "other.wal")
	if err := os.WriteFile(otherPath+".clock", before, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := ResumeClockJournal(otherPath, config.Epoch, policy); changed != nil || !errors.Is(err, ErrClockJournalBinding) {
		t.Fatalf("copied sidecar at another WAL path = %p, %v", changed, err)
	}
	after, err := os.ReadFile(path + ".clock")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected binding modified sidecar: %v", err)
	}
}

func TestClockJournalRejectsTornAndCorruptMetadata(t *testing.T) {
	path, config := clockJournalFixture(t)
	policy := clockJournalPolicy(t, config)
	j, err := CreateClockJournal(path, config.Epoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Advance(testStart.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := j.Advance(testStart.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(path + ".clock")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
		want error
	}{
		{"torn record", func(b []byte) []byte { return b[:len(b)-1] }, ErrClockJournalTornTail},
		{"record checksum", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }, ErrClockJournalCorrupt},
		{"header checksum", func(b []byte) []byte { b[clockJournalHeaderSize-1] ^= 1; return b }, ErrClockJournalCorrupt},
		{"duplicate sequence", func(b []byte) []byte {
			record := b[clockJournalHeaderSize+clockJournalRecordSize:]
			binary.BigEndian.PutUint64(record[:8], 1)
			binary.BigEndian.PutUint32(record[16:], clockJournalChecksum(record[:16]))
			return b
		}, ErrClockJournalCorrupt},
		{"backward high-water", func(b []byte) []byte {
			record := b[clockJournalHeaderSize+clockJournalRecordSize:]
			binary.BigEndian.PutUint64(record[8:16], uint64(testStart.Add(-time.Minute).UnixMilli()))
			binary.BigEndian.PutUint32(record[16:], clockJournalChecksum(record[:16]))
			return b
		}, ErrClockJournalCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			corrupt := tc.edit(append([]byte(nil), valid...))
			if err := os.WriteFile(path+".clock", corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := ResumeClockJournal(path, config.Epoch, policy); got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("corrupt sidecar = %p, %v; want %v", got, err, tc.want)
			}
		})
	}
}

type clockJournalFaultWriter struct {
	*os.File
	short   bool
	syncErr error
}

func (w *clockJournalFaultWriter) Write(b []byte) (int, error) {
	if w.short {
		return w.File.Write(b[:len(b)/2])
	}
	return w.File.Write(b)
}

func (w *clockJournalFaultWriter) Sync() error {
	if w.syncErr != nil {
		return w.syncErr
	}
	return w.File.Sync()
}

func TestClockJournalIndeterminateWritePoisonsWriter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		short   bool
		syncErr error
		want    error
	}{
		{"short write", true, nil, ErrClockJournalTornTail},
		{"sync error", false, io.ErrClosedPipe, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, config := clockJournalFixture(t)
			policy := clockJournalPolicy(t, config)
			j, err := CreateClockJournal(path, config.Epoch, policy)
			if err != nil {
				t.Fatal(err)
			}
			j.file = &clockJournalFaultWriter{File: j.file.(*os.File), short: tc.short, syncErr: tc.syncErr}
			if err := j.Advance(testStart.UnixMilli()); !errors.Is(err, ErrClockJournalUnusable) ||
				(tc.syncErr != nil && !errors.Is(err, tc.syncErr)) {
				t.Fatalf("indeterminate advance = %v", err)
			}
			if err := j.Advance(testStart.Add(time.Minute).UnixMilli()); !errors.Is(err, ErrClockJournalUnusable) {
				t.Fatalf("poisoned writer retried: %v", err)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := ResumeClockJournal(path, config.Epoch, policy)
			if tc.want != nil {
				if reopened != nil || !errors.Is(err, tc.want) {
					t.Fatalf("partial frame recovery = %p, %v", reopened, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if got := reopened.HighWaterMillis(); got != testStart.UnixMilli() {
					t.Fatalf("fully written uncertain frame = %d", got)
				}
			}
		})
	}
}
