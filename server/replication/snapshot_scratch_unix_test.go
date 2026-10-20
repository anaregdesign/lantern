//go:build linux || darwin

package replication

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestSnapshotScratchPrivateDirectory(t *testing.T) {
	t.Run("FinalPermissions", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 64<<10)
		if err := os.Chmod(config.Directory, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := config.Validate(); err == nil {
			t.Fatal("nonprivate directory accepted")
		}
	})
	t.Run("NoSymlinkComponents", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 64<<10)
		real := filepath.Join(config.Directory, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(real, "child"), 0o700); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(config.Directory, "alias")
		if err := os.Symlink(real, alias); err != nil {
			t.Fatal(err)
		}
		for _, directory := range []string{alias, filepath.Join(alias, "child")} {
			config.Directory = directory
			if err := config.Validate(); err == nil {
				t.Fatalf("symlink path accepted: %q", directory)
			}
		}
	})
	t.Run("WritableAncestor", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 64<<10)
		child := filepath.Join(config.Directory, "private")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(config.Directory, 0o770); err != nil {
			t.Fatal(err)
		}
		config.Directory = child
		if err := config.Validate(); err == nil {
			t.Fatal("group-writable ancestor accepted")
		}
	})
}

func TestSnapshotScratchLeaseOwnership(t *testing.T) {
	config := snapshotScratchTestConfig(t, 64<<10)
	first := snapshotScratchTestOwner(t, t.Context(), config)
	if _, err := newSnapshotScratch(t.Context(), config); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("concurrent owner not refused: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	assertSnapshotScratchOnlyOwner(t, config.Directory)
	ownerBefore, err := os.Stat(filepath.Join(config.Directory, snapshotScratchOwnerName))
	if err != nil {
		t.Fatal(err)
	}
	second := snapshotScratchTestOwner(t, t.Context(), config)
	ownerAfter, err := os.Stat(filepath.Join(config.Directory, snapshotScratchOwnerName))
	if err != nil || !os.SameFile(ownerBefore, ownerAfter) {
		t.Fatalf("lease pathname was replaced between installs: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotScratchCrashOrphanRecovery(t *testing.T) {
	config := snapshotScratchTestConfig(t, 64<<10)
	for _, slot := range []int{0, snapshotScratchSlots - 1} {
		if err := os.WriteFile(filepath.Join(config.Directory, snapshotScratchSlotName(slot)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scratch := snapshotScratchTestOwner(t, t.Context(), config)
	assertSnapshotScratchStats(t, scratch, snapshotScratchMetadataBytes, 0)
	assertSnapshotScratchOnlyOwner(t, config.Directory)
	file, err := scratch.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("new install")); err != nil {
		t.Fatal(err)
	}
	assertSnapshotScratchOnlyOwner(t, config.Directory)
}

func TestSnapshotScratchUnsafeArtifactRefusal(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		make func(*testing.T, string)
	}{
		{"NonemptyReservedSlot", snapshotScratchSlotName(0), func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("not a legitimate empty crash window"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"PublicSlot", snapshotScratchSlotName(0), func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"DirectorySlot", snapshotScratchSlotName(0), func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"UnknownArtifact", ".other-owner", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"SlotOutsideReservedRange", snapshotScratchSlotName(snapshotScratchSlots), func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"NonemptyOwner", snapshotScratchOwnerName, func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("unowned"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"PublicOwner", snapshotScratchOwnerName, func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := snapshotScratchTestConfig(t, 64<<10)
			path := filepath.Join(config.Directory, test.path)
			test.make(t, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newSnapshotScratch(t.Context(), config); err == nil {
				t.Fatal("unsafe artifact accepted")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() {
				t.Fatalf("refused artifact was changed/deleted: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			reopened := snapshotScratchTestOwner(t, t.Context(), config)
			if err := reopened.Close(); err != nil {
				t.Fatal("failed acquisition retained lease", err)
			}
		})
	}
	t.Run("UnknownArtifactPreservesEvenValidOrphan", func(t *testing.T) {
		config := snapshotScratchTestConfig(t, 64<<10)
		for _, name := range []string{snapshotScratchSlotName(0), "unknown"} {
			if err := os.WriteFile(filepath.Join(config.Directory, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := newSnapshotScratch(t.Context(), config); err == nil {
			t.Fatal("unknown artifact accepted")
		}
		if _, err := os.Stat(filepath.Join(config.Directory, snapshotScratchSlotName(0))); err != nil {
			t.Fatal("orphan deleted before namespace ownership validation", err)
		}
	})
	for _, kind := range []string{"Symlink", "Hardlink"} {
		for _, name := range []string{snapshotScratchOwnerName, snapshotScratchSlotName(0)} {
			t.Run(kind+name, func(t *testing.T) {
				config := snapshotScratchTestConfig(t, 64<<10)
				target := filepath.Join(t.TempDir(), "unrelated")
				if err := os.WriteFile(target, []byte("must remain unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(config.Directory, name)
				var err error
				if kind == "Symlink" {
					err = os.Symlink(target, path)
				} else {
					err = os.Link(target, path)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := newSnapshotScratch(t.Context(), config); err == nil {
					t.Fatal("linked artifact accepted")
				}
				if data, err := os.ReadFile(target); err != nil || string(data) != "must remain unchanged" {
					t.Fatalf("linked target changed: %q %v", data, err)
				}
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("refused linked artifact removed", err)
				}
			})
		}
	}
}

const snapshotScratchCrashChildEnv = "LANTERN_TEST_SNAPSHOT_SCRATCH_CRASH_CHILD"

func TestSnapshotScratchAbruptProcessExit(t *testing.T) {
	config := snapshotScratchTestConfig(t, 64<<10)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSnapshotScratchCrashChild$", "--", config.Directory)
	command.Env = append(os.Environ(), snapshotScratchCrashChildEnv+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper failed: %v\n%s", err, output)
	}
	// Waiting for process exit joins its descriptors. No payload pathname was
	// ever retained; this proves managed named cleanup, not physical disk usage.
	assertSnapshotScratchOnlyOwner(t, config.Directory)
	scratch := snapshotScratchTestOwner(t, t.Context(), config)
	assertSnapshotScratchStats(t, scratch, snapshotScratchMetadataBytes, 0)
	if err := scratch.Close(); err != nil {
		t.Fatal("process exit retained ownership", err)
	}
	// The other legitimate crash point is immediately after creating an empty
	// reserved name and before unlink. Recover only that exact empty artifact.
	if err := os.WriteFile(filepath.Join(config.Directory, snapshotScratchSlotName(0)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered := snapshotScratchTestOwner(t, t.Context(), config)
	assertSnapshotScratchStats(t, recovered, snapshotScratchMetadataBytes, 0)
	assertSnapshotScratchOnlyOwner(t, config.Directory)
}

func TestSnapshotScratchCrashChild(t *testing.T) {
	if os.Getenv(snapshotScratchCrashChildEnv) != "1" {
		t.Skip("subprocess-only abrupt exit fixture")
	}
	config := GraphSnapshotSpoolConfig{Directory: os.Args[len(os.Args)-1], MaxDiskBytes: 64 << 10, MaxFrameBytes: 4096}
	scratch, err := newSnapshotScratch(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	file, err := scratch.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("unlinked payload, deliberately no Close")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.Directory, snapshotScratchSlotName(0))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("payload slot still named: %v", err)
	}
	os.Exit(0) // Deliberately bypass owner/file cleanup to exercise OS reclamation.
}
