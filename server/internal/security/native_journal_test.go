package security

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestNativeJournalCapacityAbortDoesNotPublishOrWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.wal")
	options := nativeTestOptions(t, path)
	native, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, native)
	ctx := context.Background()
	if _, err := native.Store().ReconcileBootstrap(ctx, 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := native.journal.metadata.Snapshot()
	native.journal.maxBytes = int64(len(before)) // Force a definite pre-I/O cap.
	if _, err := native.Store().Commit(ctx, 1, [16]byte{2}, nativeTestSuspendedImage()); !errors.Is(err, ErrSystemJournalCapacity) {
		t.Fatal("capacity boundary was bypassed", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || native.journal.metadata.Snapshot().Digest != old.Digest {
		t.Fatal("capacity failure changed WAL or materialized image")
	}
	if _, healthy := native.Store().Current(); healthy {
		t.Fatal("exhausted staged control runtime continued serving")
	}
}

func TestNativeJournalFailurePoisonsStoreAndKeepsMaterializedCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.wal")
	native, err := CreateNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, native)
	ctx := context.Background()
	if _, err := native.Store().ReconcileBootstrap(ctx, 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	before := native.journal.metadata.Snapshot()
	// Interrupt the owned durable path. The Store must not publish the next
	// image or keep old grants healthy when the persistence proof is lost.
	if err := native.journal.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Store().Commit(ctx, 1, [16]byte{2}, nativeTestSuspendedImage()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatal("closed durable path did not fence Store", err)
	}
	if _, healthy := native.Store().Current(); healthy || native.journal.metadata.Snapshot().Digest != before.Digest {
		t.Fatal("unproven publication remained usable")
	}
}

func TestNativeJournalCancelledAndTypedBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.wal")
	native, err := CreateNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, native)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := native.Store().ReconcileBootstrap(ctx, 0, [16]byte{1}, testImage()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled transaction reached persistence", err)
	}
	if seq, _ := native.journal.log.LastSeq(); seq != 0 || native.journal.metadata.Snapshot().Revision != 0 {
		t.Fatal("cancelled transaction advanced state")
	}
	if _, err := encodeSystemRevision(mutationlog.MutationOp("generic graph mutation")); !errors.Is(err, ErrInvalidRevision) {
		t.Fatal("generic operation entered system lane")
	}
}

func TestNativeJournalRecoversUnpublishedSuffixWithinCapacity(t *testing.T) {
	for _, capCatchUp := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "cap before tip catch-up"}[capCatchUp], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "system.wal")
			options := nativeTestOptions(t, path)
			native, err := CreateNativeStore(options)
			if err != nil {
				t.Fatal(err)
			}
			cleanupNativeStore(t, native)
			if _, err := native.Store().ReconcileBootstrap(context.Background(), 0, [16]byte{1}, testImage()); err != nil {
				t.Fatal(err)
			}
			one, _ := native.Store().Current()
			snapshot, err := CompileImage(nativeTestSuspendedImage(), options.Limits)
			if err != nil {
				t.Fatal(err)
			}
			two, err := SignRevision(options.Generation, 2, one.Digest(), [16]byte{2}, snapshot, options.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			decode := func(raw []byte) (mutationlog.MutationOp, error) {
				return DecodeRevision(raw, options.PublicKey, options.Limits)
			}
			bare, err := mutationlog.ResumeFileWAL(path, encodeSystemRevision, decode, func(mutationlog.Entry) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			// Simulate process loss after the frame Sync and before tip Sync.
			if err := bare.Write(mutationlog.Entry{Seq: 2, Op: two}); err != nil {
				t.Fatal(err)
			}
			if err := bare.Close(); err != nil {
				t.Fatal(err)
			}
			tipBefore, err := os.ReadFile(path + ".tip")
			if err != nil {
				t.Fatal(err)
			}
			resuming := nativeTestOptions(t, path)
			if capCatchUp {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				resuming.MaxJournalBytes = info.Size() + int64(len(tipBefore))
			}
			recovered, err := ResumeNativeStore(resuming)
			if capCatchUp {
				if !errors.Is(err, ErrSystemJournalCapacity) || recovered != nil {
					t.Fatal("unpublished tip catch-up exceeded capacity", err)
				}
				tipAfter, err := os.ReadFile(path + ".tip")
				if err != nil || !bytes.Equal(tipBefore, tipAfter) {
					t.Fatal("over-capacity recovery mutated the journal")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cleanupNativeStore(t, recovered)
			current, healthy := recovered.Store().Current()
			if !healthy || current.Digest() != two.Digest() || recovered.journal.metadata.Snapshot().Revision != 2 {
				t.Fatal("unpublished complete suffix was not recovered")
			}
		})
	}
}
