package mutationlog

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFileWALTipJournalSurvivesProcessExit(t *testing.T) {
	const childKey = "LANTERN_TEST_FILEWAL_TIP_CHILD"
	if path := os.Getenv(childKey); path != "" {
		binding := sha256.Sum256([]byte("fresh process epoch and policy"))
		log, _, err := CreateLeasedLogWithFileWALTip(path, Options{}, fileWALStringEncode, binding)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.CommitWithPublication("first", fileWALEntry(1, "first").HLC, nil); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // No Close: only prior fsync calls establish durability.
	}
	path := filepath.Join(t.TempDir(), "crash.wal")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileWALTipJournalSurvivesProcessExit$")
	child.Env = append(os.Environ(), childKey+"="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child commit: %v: %s", err, out)
	}
	lease, err := AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	binding := sha256.Sum256([]byte("fresh process epoch and policy"))
	tip, err := ResumeFileWALTipJournal(lease.Path(), binding)
	if err != nil {
		t.Fatal(err)
	}
	defer tip.Close()
	log, owner, err := ResumeLogFromFileWALWithTip(lease.Path(), Options{}, fileWALStringEncode,
		fileWALStringDecode, fileWALCutValidEntry, func(Entry) error { return nil }, tip)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if seq, ok := log.LastSeq(); !ok || seq != 1 {
		t.Fatalf("fresh-process restored frontier = %d, %v", seq, ok)
	}
}

func TestFileWALTipJournalRejectsValidPrefixTruncationAndReplacement(t *testing.T) {
	path, lease, wal, journal, binding := fileWALTipFixture(t)
	for _, entry := range []Entry{fileWALEntry(1, "first"), fileWALEntry(2, "second")} {
		if err := wal.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if seq, _, ok := journal.Frontier(); !ok || seq != 2 {
		t.Fatalf("published tip = %d, verified %v", seq, ok)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	firstEnd := len(fileWALMagic) + fileWALFrameHeader + fileWALBodyHeader + len("first")
	if err := os.WriteFile(path, full[:firstEnd], 0o600); err != nil {
		t.Fatal(err)
	}
	reopen := func() *FileWALTipJournal {
		t.Helper()
		j, err := ResumeFileWALTipJournal(lease.Path(), binding)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	truncated := reopen()
	if err := truncated.VerifyAndCatchUp(lease.Path(), fileWALStringDecode, fileWALCutValidEntry); !errors.Is(err, ErrFileWALCutUnavailable) {
		t.Fatalf("valid-prefix truncation = %v", err)
	}
	_ = truncated.Close()
	if err := os.WriteFile(path, full, 0o600); err != nil {
		t.Fatal(err)
	}
	// Replace a complete, valid-CRC first frame with another frame of the
	// same size and sequence. The final WAL remains syntactically valid.
	replacement := append([]byte(nil), full...)
	newFirst := encodeFileWALFrame(fileWALEntry(1, "other"), []byte("other"))
	copy(replacement[len(fileWALMagic):firstEnd], newFirst)
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	changed := reopen()
	if err := changed.VerifyAndCatchUp(lease.Path(), fileWALStringDecode, fileWALCutValidEntry); !errors.Is(err, ErrFileWALTipMismatch) {
		t.Fatalf("same-size WAL replacement = %v", err)
	}
	_ = changed.Close()
}

func TestFileWALTipJournalCatchesUpValidUnpublishedSuffix(t *testing.T) {
	path, lease, wal, journal, binding := fileWALTipFixture(t)
	if err := wal.Write(fileWALEntry(1, "first")); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after WAL fsync and before the next tip sync.
	bare, err := ResumeFileWAL(lease.Path(), fileWALStringEncode, fileWALStringDecode, func(Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := bare.Write(fileWALEntry(2, "second")); err != nil {
		t.Fatal(err)
	}
	if err := bare.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeFileWALTipJournal(lease.Path(), binding)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if err := resumed.VerifyAndCatchUp(lease.Path(), fileWALStringDecode, fileWALCutValidEntry); err != nil {
		t.Fatal(err)
	}
	cut, err := InspectFileWALCut(path, 2, fileWALStringDecode, fileWALCutValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	if seq, chain, ok := resumed.Frontier(); !ok || seq != 2 || chain != cut.ObservedChainSHA256 {
		t.Fatalf("caught-up tip = (%d, %x, %v), want seq 2 chain %x", seq, chain, ok, cut.ObservedChainSHA256)
	}
	appendable, err := ResumeFileWAL(lease.Path(), fileWALStringEncode, fileWALStringDecode, func(Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer appendable.Close()
	if err := appendable.BindTipJournal(resumed); err != nil {
		t.Fatal(err)
	}
	if err := appendable.Write(fileWALEntry(3, "third")); err != nil {
		t.Fatal(err)
	}
}

func TestFileWALTipJournalFailurePoisonsWAL(t *testing.T) {
	_, _, wal, journal, _ := fileWALTipFixture(t)
	journal.mu.Lock()
	if err := journal.file.Close(); err != nil {
		journal.mu.Unlock()
		t.Fatal(err)
	}
	journalingFailure := errors.New("tip write failed")
	journal.file = failingTipWriter{err: journalingFailure}
	journal.mu.Unlock()
	if err := wal.Write(fileWALEntry(1, "first")); !errors.Is(err, ErrFileWALUnusable) || !errors.Is(err, journalingFailure) {
		t.Fatalf("tip write failure = %v", err)
	}
	if err := wal.Write(fileWALEntry(1, "retry")); !errors.Is(err, ErrFileWALUnusable) {
		t.Fatalf("write after uncertain tip = %v", err)
	}
	if _, _, verified := journal.Frontier(); verified {
		t.Fatal("failed journal remains verified")
	}
}

func TestFileWALTipJournalFailurePreventsLogPublication(t *testing.T) {
	_, _, wal, journal, _ := fileWALTipFixture(t)
	log := New(Options{WAL: wal})
	defer log.Close()
	journal.mu.Lock()
	if err := journal.file.Close(); err != nil {
		journal.mu.Unlock()
		t.Fatal(err)
	}
	journalingFailure := errors.New("tip sync failed")
	journal.file = failingTipWriter{err: journalingFailure}
	journal.mu.Unlock()
	published := false
	if _, err := log.CommitWithPublication("first", fileWALEntry(1, "first").HLC, func(Entry) {
		published = true
	}); !errors.Is(err, ErrWALIndeterminate) || !errors.Is(err, journalingFailure) {
		t.Fatalf("commit with failed tip = %v", err)
	}
	if published {
		t.Fatal("graph publication ran despite missing tip")
	}
	if _, ok := log.LastSeq(); ok {
		t.Fatal("Log published an unjournaled sequence")
	}
	if _, err := log.CommitWithPublication("retry", fileWALEntry(1, "retry").HLC, nil); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("commit after tip failure = %v", err)
	}
}

func TestFileWALTipJournalRejectsMissingTornAndWrongBinding(t *testing.T) {
	path, lease, wal, journal, binding := fileWALTipFixture(t)
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := sha256.Sum256([]byte("wrong epoch"))
	if j, err := ResumeFileWALTipJournal(lease.Path(), wrong); j != nil || !errors.Is(err, ErrFileWALTipBinding) {
		t.Fatalf("wrong binding = %p, %v", j, err)
	}
	if err := os.Truncate(path+".tip", int64(fileWALTipHeaderSize-1)); err != nil {
		t.Fatal(err)
	}
	if j, err := ResumeFileWALTipJournal(lease.Path(), binding); j != nil || !errors.Is(err, ErrFileWALTipCorrupt) {
		t.Fatalf("torn header = %p, %v", j, err)
	}
	if err := os.Remove(path + ".tip"); err != nil {
		t.Fatal(err)
	}
	if j, err := ResumeFileWALTipJournal(lease.Path(), binding); j != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing journal = %p, %v", j, err)
	}
}

func TestFileWALTipJournalRejectsShortWrite(t *testing.T) {
	_, _, wal, journal, _ := fileWALTipFixture(t)
	journal.mu.Lock()
	if err := journal.file.Close(); err != nil {
		journal.mu.Unlock()
		t.Fatal(err)
	}
	journal.file = shortTipWriter{}
	journal.mu.Unlock()
	if err := wal.Write(fileWALEntry(1, "first")); !errors.Is(err, ErrFileWALTipUnusable) || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("tip short write = %v", err)
	}
}

func TestFileWALTipJournalRejectsSyncFailure(t *testing.T) {
	_, _, wal, journal, _ := fileWALTipFixture(t)
	journal.mu.Lock()
	if err := journal.file.Close(); err != nil {
		journal.mu.Unlock()
		t.Fatal(err)
	}
	syncFailure := errors.New("tip sync failed")
	journal.file = syncFailingTipWriter{err: syncFailure}
	journal.mu.Unlock()
	if err := wal.Write(fileWALEntry(1, "first")); !errors.Is(err, ErrFileWALUnusable) || !errors.Is(err, syncFailure) {
		t.Fatalf("tip sync failure = %v", err)
	}
}

type syncFailingTipWriter struct{ err error }

func (w syncFailingTipWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w syncFailingTipWriter) Sync() error                 { return w.err }
func (w syncFailingTipWriter) Close() error                { return nil }

type shortTipWriter struct{}

func (shortTipWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func (shortTipWriter) Sync() error                 { return nil }
func (shortTipWriter) Close() error                { return nil }
