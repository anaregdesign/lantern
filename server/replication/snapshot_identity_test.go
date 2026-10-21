package replication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
)

func newSnapshotIdentityTestScratch(t *testing.T, maxBytes uint64, allowCloseFailure ...bool) *snapshotScratch {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	scratch, err := newSnapshotScratch(t.Context(), GraphSnapshotSpoolConfig{
		Directory: directory, MaxDiskBytes: maxBytes, MaxFrameBytes: min(32<<10, int(maxBytes)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scratch.Close(); err != nil && (len(allowCloseFailure) == 0 || !allowCloseFailure[0]) {
			t.Error(err)
		}
		if used, files := scratch.stats(); used != 0 || files != 0 {
			t.Errorf("scratch owner cleanup retained bytes=%d files=%d", used, files)
		}
	})
	return scratch
}

func assertSnapshotIdentityReleased(t *testing.T, scratch *snapshotScratch) {
	t.Helper()
	if used, files := scratch.stats(); used != snapshotScratchMetadataBytes || files != 0 {
		t.Fatalf("identity files retained after Finish: bytes=%d files=%d", used, files)
	}
}

func TestSnapshotIdentityExactKeysAndMixedTypes(t *testing.T) {
	scratch := newSnapshotIdentityTestScratch(t, 8<<20)
	index, err := newSnapshotIdentityIndex(t.Context(), scratch)
	if err != nil {
		t.Fatal(err)
	}
	// These tuples share concatenated bytes but are different edge identities.
	for _, edge := range [][2]string{{"a", "bc"}, {"ab", "c"}, {"a\x00b", "c"}, {"a", "b\x00c"}} {
		if err := index.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		tail, head string
		id         graphcache.ContribID
		tombstone  bool
	}{
		{tail: "a", head: "bc", id: graphcache.ContribID{1}},
		{tail: "a", head: "bc", id: graphcache.ContribID{2}, tombstone: true},
		{tail: "ab", head: "c", id: graphcache.ContribID{1}, tombstone: true},
		// These IDs have identical first 16 bytes but differ in the final
		// eight bytes; truncating ContribID would create a false conflict.
		{tail: "full", head: "id", id: graphcache.ContribID{1, 23: 1}},
		{tail: "full", head: "id", id: graphcache.ContribID{1, 23: 2}, tombstone: true},
	} {
		if err := index.AddContribution(item.tail, item.head, item.id, item.tombstone); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Finish(); err != nil {
		t.Fatal("distinct exact identities rejected", err)
	}
	assertSnapshotIdentityReleased(t, scratch)
	if err := index.Finish(); err != nil {
		t.Fatal("successful Finish is not idempotent", err)
	}
	if err := index.AddEdge("new", "edge"); err == nil {
		t.Fatal("finished index accepted another identity")
	}
}

func TestSnapshotIdentityDigestCollisionUsesExactArenaKeys(t *testing.T) {
	scratch := newSnapshotIdentityTestScratch(t, 8<<20)
	index, err := newSnapshotIdentityIndex(t.Context(), scratch)
	if err != nil {
		t.Fatal(err)
	}
	// The common prefix spans multiple bounded comparison reads. Force the
	// descriptor accelerator to collide; the exact arena bytes must decide.
	prefix := strings.Repeat("same-prefix-", 2048)
	for _, edge := range [][2]string{
		{prefix + "a", "head"},
		{prefix + "b", "head"},
		{"a", prefix + "bc"},
		{"ab", prefix + "c"},
	} {
		if err := index.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []graphcache.ContribID{{1, 23: 1}, {1, 23: 2}} {
		if err := index.AddContribution("collision", "full-id", id, false); err != nil {
			t.Fatal(err)
		}
	}
	for item := range index.n {
		index.records[item].digest = [32]byte{0x9d}
	}
	if err := index.Finish(); err != nil {
		t.Fatal("digest collision merged different exact keys", err)
	}
	assertSnapshotIdentityReleased(t, scratch)
}

func TestSnapshotIdentityRejectsConflictsAcrossMergeLevels(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		edge                  bool
		firstTomb, secondTomb bool
	}{
		{name: "duplicate edge", edge: true},
		{name: "duplicate contribution tombstone", firstTomb: true, secondTomb: true},
		{name: "live then tombstone", secondTomb: true},
		{name: "tombstone then live", firstTomb: true},
		{name: "duplicate live contribution"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := newSnapshotIdentityTestScratch(t, 8<<20)
			index, err := newSnapshotIdentityIndex(t.Context(), scratch)
			if err != nil {
				t.Fatal(err)
			}
			index.chunkLimit = 2
			add := func(tombstone bool) error {
				if tc.edge {
					return index.AddEdge("conflicting tail", "conflicting head")
				}
				return index.AddContribution("conflicting tail", "conflicting head", graphcache.ContribID{7}, tombstone)
			}
			if err := add(tc.firstTomb); err != nil {
				t.Fatal(err)
			}
			// Separate the two matching records by more than one binary carry
			// level. Each filler is distinct and does not overlap either key.
			for item := 40; item > 0; item-- {
				if err := index.AddEdge(fmt.Sprintf("filler-%03d", item), "head"); err != nil {
					t.Fatal("distinct filler rejected", err)
				}
			}
			err = add(tc.secondTomb)
			if err == nil {
				err = index.Finish()
			}
			if connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("cross-run conflict = %v, want Snapshot protocol error", err)
			}
			if finishErr := index.Finish(); finishErr == nil {
				t.Fatal("conflict did not remain sticky through Finish")
			}
			assertSnapshotIdentityReleased(t, scratch)
		})
	}
}

func TestSnapshotIdentityDistinctRunsFinishAndRelease(t *testing.T) {
	for _, count := range []int{0, 65} {
		t.Run(fmt.Sprintf("records-%d", count), func(t *testing.T) {
			scratch := newSnapshotIdentityTestScratch(t, 8<<20)
			index, err := newSnapshotIdentityIndex(t.Context(), scratch)
			if err != nil {
				t.Fatal(err)
			}
			index.chunkLimit = 2
			for item := count; item > 0; item-- {
				if err := index.AddEdge(fmt.Sprintf("tail-%03d", item), "head"); err != nil {
					t.Fatal(err)
				}
				if _, files := scratch.stats(); files > 8 {
					t.Fatalf("online binary merge retained too many files: %d", files)
				}
			}
			if err := index.Finish(); err != nil {
				t.Fatal("distinct multi-level identities rejected", err)
			}
			assertSnapshotIdentityReleased(t, scratch)
		})
	}
}

func TestSnapshotIdentityCanceledConstructionAndFinishRelease(t *testing.T) {
	t.Run("before construction", func(t *testing.T) {
		scratch := newSnapshotIdentityTestScratch(t, 8<<20)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := newSnapshotIdentityIndex(ctx, scratch); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled construction = %v", err)
		}
		assertSnapshotIdentityReleased(t, scratch)
	})
	t.Run("after spill", func(t *testing.T) {
		scratch := newSnapshotIdentityTestScratch(t, 8<<20)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		index, err := newSnapshotIdentityIndex(ctx, scratch)
		if err != nil {
			t.Fatal(err)
		}
		index.chunkLimit = 2
		for item := range 9 {
			if err := index.AddEdge(fmt.Sprintf("tail-%d", item), "head"); err != nil {
				t.Fatal(err)
			}
		}
		if used, files := scratch.stats(); used == 0 || files < 2 {
			t.Fatalf("cancellation control did not own actual spilled bytes: %d/%d", used, files)
		}
		cancel()
		if err := index.Finish(); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Finish = %v", err)
		}
		assertSnapshotIdentityReleased(t, scratch)
	})
}

func TestSnapshotIdentityScratchFailureStaysClosedAndRefunds(t *testing.T) {
	t.Run("quota at first run", func(t *testing.T) {
		// Owner lease, arena metadata and the first arena data block fit.
		// Even an empty run would exceed the one aggregate allowance.
		scratch := newSnapshotIdentityTestScratch(t, 3*snapshotScratchBlockBytes, true)
		index, err := newSnapshotIdentityIndex(t.Context(), scratch)
		if err != nil {
			t.Fatal(err)
		}
		index.chunkLimit = 1
		if err := index.AddEdge("tail", "head"); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("run metadata quota = %v", err)
		}
		if err := index.Finish(); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("quota failure did not stay sticky: %v", err)
		}
		if used, files := scratch.stats(); used != 0 || files != 0 {
			t.Fatalf("quota failure retained owner bytes=%d files=%d", used, files)
		}
	})
	t.Run("arena comparison read", func(t *testing.T) {
		scratch := newSnapshotIdentityTestScratch(t, 8<<20, true)
		index, err := newSnapshotIdentityIndex(t.Context(), scratch)
		if err != nil {
			t.Fatal(err)
		}
		for _, tail := range []string{"tail-a", "tail-b"} {
			if err := index.AddEdge(tail, "head"); err != nil {
				t.Fatal(err)
			}
		}
		for item := range index.n {
			index.records[item].digest = [32]byte{0x9d}
		}
		failure := errors.New("controlled identity arena read failure")
		scratch.readAt = func(*os.File, []byte, int64) (int, error) { return 0, failure }
		if err := index.Finish(); !errors.Is(err, failure) {
			t.Fatalf("arena read failure was hidden by comparison: %v", err)
		}
		if err := index.Finish(); !errors.Is(err, failure) {
			t.Fatalf("arena read failure did not stay sticky: %v", err)
		}
		if used, files := scratch.stats(); used != 0 || files != 0 {
			t.Fatalf("read failure retained owner bytes=%d files=%d", used, files)
		}
	})
}

func TestSnapshotIdentityRejectsAuxiliaryCorruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  bool
	}{
		{name: "spilled descriptor payload", run: true},
		{name: "arena beyond first comparison buffer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := newSnapshotIdentityTestScratch(t, 8<<20)
			index, err := newSnapshotIdentityIndex(t.Context(), scratch)
			if err != nil {
				t.Fatal(err)
			}
			// A single identity has no adjacent-identity comparison that could
			// accidentally expose corruption through a sort or duplicate check.
			tail, head := strings.Repeat("tail", 2048), "head"
			if tc.run {
				index.chunkLimit = 1
			}
			if err := index.AddEdge(tail, head); err != nil {
				t.Fatal(err)
			}
			file, offset := index.arena, int64(len(tail)+len(head)-1)
			if tc.run {
				if index.levels[0] == nil {
					t.Fatal("descriptor corruption control did not spill a run")
				}
				file = index.levels[0].file
				// Change an accelerator byte, leaving all decoded lengths and
				// identity flags valid and the saved checksum unchanged.
				offset = 56
			}
			usedBefore, filesBefore := scratch.stats()
			var changed [1]byte
			if n, err := file.ReadAt(changed[:], offset); n != len(changed) || err != nil {
				t.Fatalf("read corruption control: bytes=%d err=%v", n, err)
			}
			changed[0] ^= 0x80
			if n, err := file.WriteAt(changed[:], offset); n != len(changed) || err != nil {
				t.Fatalf("write corruption control: bytes=%d err=%v", n, err)
			}
			if used, files := scratch.stats(); used != usedBefore || files != filesBefore {
				t.Fatalf("same-size corruption changed quota charge: before=%d/%d after=%d/%d", usedBefore, filesBefore, used, files)
			}
			if err := index.Finish(); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("corrupt auxiliary identity bytes = %v, want Snapshot protocol error", err)
			}
			if err := index.Finish(); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("auxiliary corruption did not stay sticky: %v", err)
			}
			assertSnapshotIdentityReleased(t, scratch)
		})
	}
}
