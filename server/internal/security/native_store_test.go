package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func TestNativeStoreMachineDigestsSurviveSignedRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.wal")
	native, err := CreateNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, token, now := machineBootstrapFixture(t)
	if _, err := native.Store().ApplyBootstrap(t.Context(), bootstrap); err != nil {
		t.Fatal(err)
	}
	current, _ := native.Store().Current()
	encoded := current.Encode()
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || bytes.Contains(raw, []byte(token)) {
		t.Fatal("raw machine credential persisted", err)
	}
	resumed, err := ResumeNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, resumed)
	current, _ = resumed.Store().Current()
	if _, _, valid := current.Snapshot().MachineAccess(token, now); !valid {
		t.Fatal("restart lost authenticated credential binding")
	}
	options := nativeTestOptions(t, filepath.Join(t.TempDir(), "replica.wal"))
	options.PrivateKey = nil
	replica, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, replica)
	if err := replica.Store().Apply(t.Context(), encoded); err != nil {
		t.Fatal(err)
	}
	cut, _ := replica.Store().Current()
	if identity, _, valid := cut.Snapshot().MachineAccess(token, now); !valid || identity.Kind != MachinePrincipal {
		t.Fatal("signed apply lost Principal kind")
	}
}

func TestNativeStoreDurableOwnershipAndRestart(t *testing.T) {
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
	result, err := native.Store().Commit(ctx, 1, [16]byte{2}, nativeTestSuspendedImage())
	if err != nil {
		t.Fatal(err)
	}
	current, healthy := native.Store().Current()
	metadata := native.journal.metadata.Snapshot()
	if !healthy || metadata.Revision != 2 || metadata.Digest != current.Digest() || !bytes.Equal(metadata.Value, current.Encode()) {
		t.Fatal("WAL, Store and reserved image disagree")
	}
	if _, found := options.Graph.GetVertex(SystemRevisionKey); found || options.Graph.VertexCount() != 0 ||
		len(options.Graph.SnapshotGraph().Vertices) != 0 {
		t.Fatal("system state appeared in public data storage")
	}
	if _, err := ResumeNativeStore(nativeTestOptions(t, path)); err == nil {
		t.Fatal("second process owner opened live path")
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	if _, healthy := native.Store().Current(); healthy {
		t.Fatal("closed storage owner left authority healthy")
	}
	resumed, err := ResumeNativeStore(nativeTestOptions(t, path))
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, resumed)
	status, err := resumed.Store().ChangeStatus([16]byte{2})
	if err != nil || status != result {
		t.Fatal("WAL restart lost exact original commit proof", status, err)
	}
	replay, err := resumed.Store().Commit(ctx, 1, [16]byte{2}, nativeTestSuspendedImage())
	if err != nil || !replay.Replayed || replay.Digest != result.Digest {
		t.Fatalf("restart lost idempotent result: %+v %v", replay, err)
	}
	current, healthy = resumed.Store().Current()
	if !healthy || current.Sequence() != 2 || resumed.journal.metadata.Snapshot().Digest != current.Digest() {
		t.Fatal("recovery did not install complete final revision")
	}
	if _, active := current.Snapshot().AccessFor(Identity{Kind: MachinePrincipal, MachineName: "former_reader"}); active {
		t.Fatal("recovery resurrected suspended grants")
	}
	if _, err := resumed.Store().Commit(ctx, 2, [16]byte{3}, nativeTestSuspendedImage()); err != nil {
		t.Fatal("resumed commit did not continue exact frontier", err)
	}
}

func TestNativeStoreRejectsDamagedOrMismatchedRecovery(t *testing.T) {
	for _, failure := range []string{"torn", "corrupt", "prefix_rollback", "missing_tip", "wrong_generation", "wrong_key"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "system.wal")
			native, err := CreateNativeStore(nativeTestOptions(t, path))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := native.Store().ReconcileBootstrap(ctx, 0, [16]byte{1}, testImage()); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := native.Store().Commit(ctx, 1, [16]byte{2}, nativeTestSuspendedImage()); err != nil {
				t.Fatal(err)
			}
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			full, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			options := nativeTestOptions(t, path)
			switch failure {
			case "torn":
				full = full[:len(full)-1]
			case "corrupt":
				full[len(full)-1] ^= 1
			case "prefix_rollback":
				full = first
			case "missing_tip":
				if err := os.Remove(path + ".tip"); err != nil {
					t.Fatal(err)
				}
			case "wrong_generation":
				options.Generation = [16]byte{9}
			case "wrong_key":
				options.PrivateKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
				options.PublicKey = options.PrivateKey.Public().(ed25519.PublicKey)
			}
			if err := os.WriteFile(path, full, 0o600); err != nil {
				t.Fatal(err)
			}
			if resumed, err := ResumeNativeStore(options); err == nil || resumed != nil {
				t.Fatal("returned partially certified security state")
			}
			_, record, reserved := options.Graph.SnapshotSystemMetadata()
			if !reserved || options.Graph.VertexCount() != 0 || record.Revision != 0 || len(record.Value) != 0 {
				t.Fatal("rejected recovery published a partial candidate image")
			}
		})
	}
}

func TestNativeStoreFreshProcessCommitSurvivesExit(t *testing.T) {
	const childKey = "LANTERN_TEST_NATIVE_SECURITY_CHILD"
	if path := os.Getenv(childKey); path != "" {
		native, err := CreateNativeStore(nativeTestOptions(t, path))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := native.Store().ReconcileBootstrap(context.Background(), 0, [16]byte{1}, testImage()); err != nil {
			t.Fatal(err)
		}
		if _, err := native.Store().Commit(context.Background(), 1, [16]byte{2}, nativeTestSuspendedImage()); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // No Close: synchronous WAL/tip durability is the evidence.
	}
	path := filepath.Join(t.TempDir(), "system.wal")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeStoreFreshProcessCommitSurvivesExit$")
	child.Env = append(os.Environ(), childKey+"="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	options := nativeTestOptions(t, path)
	options.PrivateKey = nil // A replica recovers the original writer's state.
	native, err := ResumeNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	cleanupNativeStore(t, native)
	current, healthy := native.Store().Current()
	if !healthy || current.Sequence() != 2 || native.journal.metadata.Snapshot().Digest != current.Digest() {
		t.Fatal("fresh process did not recover durably committed state")
	}
	if _, err := native.Store().Commit(ctx, 2, [16]byte{3}, testImage()); !errors.Is(err, ErrReadOnlyWriter) {
		t.Fatal("recovered replica became a writer")
	}
}

func TestNativeStoreRejectsSignedUnlinkedSuffix(t *testing.T) {
	for _, failure := range []string{"fork", "gap", "change_id_reuse", "bootstrap_lock"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "system.wal")
			options := nativeTestOptions(t, path)
			native, err := CreateNativeStore(options)
			if err != nil {
				t.Fatal(err)
			}
			cleanupNativeStore(t, native)
			if _, err := native.Store().ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
				t.Fatal(err)
			}
			one, _ := native.Store().Current()
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			image := testImage()
			sequence, previous, changeID := uint64(2), one.Digest(), [16]byte{2}
			switch failure {
			case "fork":
				previous = [32]byte{9}
			case "gap":
				sequence = 3
			case "change_id_reuse":
				changeID = [16]byte{1}
			case "bootstrap_lock":
				image.Roles[0].Rules = append(image.Roles[0].Rules, dataRule(Allow, VertexRead, ""))
			}
			snapshot, err := CompileImage(image, options.Limits)
			if err != nil {
				t.Fatal(err)
			}
			suffix, err := SignRevision(options.Generation, sequence, previous, changeID, snapshot, options.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			decode := func(raw []byte) (mutationlog.MutationOp, error) {
				return DecodeRevision(raw, options.PublicKey, options.Limits)
			}
			bare, err := mutationlog.ResumeFileWAL(path, encodeSystemRevision, decode, func(mutationlog.Entry) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := bare.Write(mutationlog.Entry{Seq: 2, Op: suffix}); err != nil {
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
			if recovered, err := ResumeNativeStore(resuming); err == nil || recovered != nil {
				t.Fatal("valid signature bypassed transaction continuity")
			}
			_, record, reserved := resuming.Graph.SnapshotSystemMetadata()
			tipAfter, err := os.ReadFile(path + ".tip")
			if !reserved || record.Revision != 0 || err != nil || !bytes.Equal(tipBefore, tipAfter) {
				t.Fatal("rejected signed suffix was published or attested")
			}
		})
	}
}

func TestNativeStoreRejectsOldAuthenticationImageBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.wal")
	options := nativeTestOptions(t, path)
	native, err := CreateNativeStore(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.Store().ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	old := []byte("lantern-system-journal-v1\x00" + keyspace.Version + "\x00")
	old = append(old, options.Generation[:]...)
	old = append(old, options.PublicKey...)
	manifest, err := readNativeManifest(path, systemJournalBinding(options))
	if err != nil {
		t.Fatal(err)
	}
	manifest.binding = sha256.Sum256(old)
	if err := writeNativeManifest(path, manifest, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path + ".tip")
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := ResumeNativeStore(nativeTestOptions(t, path)); !errors.Is(err, ErrInvalidRevision) || resumed != nil {
		t.Fatal("old session-family binding admitted", err)
	}
	after, err := os.ReadFile(path + ".tip")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected old family advanced durable floor", err)
	}
}
