package security

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRotationBoundsHistoryAndReplicaReplay(t *testing.T) {
	for _, replica := range []bool{false, true} {
		t.Run(map[bool]string{false: "writer", true: "replica"}[replica], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "system.wal")
			options := nativeTestOptions(t, path)
			options.MaxJournalBytes = 160 << 10
			if replica {
				options.PrivateKey = nil
			}
			native, err := CreateNativeStore(options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = native.Close() }()
			writer, _, _ := testStore(t, true)
			// The original authority must equal the operator-pinned key on both nodes.
			writer.publicKey = bytes.Clone(options.PublicKey)
			writer.privateKey = bytes.Clone(nativeTestOptions(t, path).PrivateKey)
			writer.generation = options.Generation
			image := testImage()
			if replica {
				if _, err := writer.ReconcileBootstrap(context.Background(), 0, [16]byte{1}, image); err != nil {
					t.Fatal(err)
				}
				one, _ := writer.Current()
				if err := native.Store().Apply(context.Background(), one.Encode()); err != nil {
					t.Fatal(err)
				}
			} else if _, err := native.Store().ReconcileBootstrap(context.Background(), 0, [16]byte{1}, image); err != nil {
				t.Fatal(err)
			}
			oldSelector, err := os.ReadFile(path + ".current")
			if err != nil {
				t.Fatal(err)
			}
			image = nativeTestSuspendedImage()
			for expected := uint64(1); expected < 300; expected++ {
				id := [16]byte{byte(expected + 1), byte((expected + 1) >> 8)}
				if replica {
					if _, err := writer.Commit(context.Background(), expected, id, image); err != nil {
						t.Fatal(expected, err)
					}
					current, _ := writer.Current()
					if err := native.Store().Apply(context.Background(), current.Encode()); err != nil {
						t.Fatal(expected, err)
					}
				} else if _, err := native.Store().Commit(context.Background(), expected, id, image); err != nil {
					t.Fatal(expected, err)
				}
			}
			if native.journal.base == 0 {
				t.Fatal("journal never rotated")
			}
			current, _ := native.Store().Current()
			expectedDigest := current.digest
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			var bytesOnDisk int64
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					t.Fatal(err)
				}
				bytesOnDisk += info.Size()
			}
			if bytesOnDisk > options.MaxJournalBytes {
				t.Fatal("system lane exceeded disk budget", bytesOnDisk)
			}
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			options.Graph = nativeTestOptions(t, path).Graph
			reopened, err := ResumeNativeStore(options)
			if err != nil {
				t.Fatal(err)
			}
			current, healthy := reopened.Store().Current()
			if !healthy || current.sequence != 300 || current.digest != expectedDigest || len(reopened.Store().changes) != retainedChanges {
				t.Fatal("checkpoint recovery lost coherent frontier")
			}
			if !replica {
				replay, err := reopened.Store().Commit(context.Background(), 299, [16]byte{44, 1}, image)
				if err != nil || !replay.Replayed || replay.Revision != 300 {
					t.Fatal("rotation lost retry evidence", replay, err)
				}
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
			// Obsolete bytes are absent before later changes can publish. Restoring an
			// old selector cannot reopen the superseded policy, even with a valid hash.
			if err := os.WriteFile(path+".current", oldSelector, 0600); err != nil {
				t.Fatal(err)
			}
			options.Graph = nativeTestOptions(t, path).Graph
			if rollback, err := ResumeNativeStore(options); err == nil {
				_ = rollback.Close()
				t.Fatal("stale selector recovered old authority")
			}
		})
	}
}

func TestNativeRotationFailureRecoversExactlyAcknowledgedCut(t *testing.T) {
	for _, point := range []rotationFaultPoint{rotationAfterCheckpoint, rotationAfterManifest, rotationAfterCleanup} {
		t.Run(string(rune('0'+point)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "system.wal")
			options := nativeTestOptions(t, path)
			options.MaxJournalBytes = 160 << 10
			native, err := CreateNativeStore(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := native.Store().ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected rotation fault")
			native.journal.rotationFault = func(at rotationFaultPoint) error {
				if at == point {
					return fault
				}
				return nil
			}
			last := uint64(1)
			image := nativeTestSuspendedImage()
			for expected := uint64(1); expected < 100; expected++ {
				id := [16]byte{byte(expected + 1)}
				if _, err := native.Store().Commit(t.Context(), expected, id, image); err != nil {
					if !errors.Is(err, fault) {
						t.Fatal("failed before injected point", expected, err)
					}
					break
				}
				last = expected + 1
			}
			if _, healthy := native.Store().Current(); healthy {
				t.Fatal("uncertain rotation left authority usable")
			}
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			options.Graph = nativeTestOptions(t, path).Graph
			reopened, err := ResumeNativeStore(options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			current, healthy := reopened.Store().Current()
			if !healthy || current.sequence != last {
				t.Fatal("rotation recovery lost acknowledged state", last, current.sequence)
			}
			if _, err := reopened.Store().Commit(t.Context(), last, [16]byte{byte(last + 1)}, image); err != nil {
				t.Fatal("recovered change could not continue", err)
			}
		})
	}
}
