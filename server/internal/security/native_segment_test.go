package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSegmentCleanupPreservesSelectedAndLease(t *testing.T) {
	anchor := filepath.Join(t.TempDir(), "system.wal")
	selected := anchor + ".segment-00000000000000000000000000000001"
	obsolete := anchor + ".segment-00000000000000000000000000000002"
	keep := []string{selected, selected + ".tip", anchor + ".lease", anchor + ".current", anchor + ".segment-unrelated", filepath.Join(filepath.Dir(anchor), "other.wal")}
	remove := []string{anchor, anchor + ".tip", obsolete, obsolete + ".tip", anchor + ".current-tmp-crash"}
	for _, path := range append(append([]string{}, keep...), remove...) {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupNativeSegments(anchor, selected); err != nil {
		t.Fatal(err)
	}
	for _, path := range keep {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("removed unrelated or active state", path, err)
		}
	}
	for _, path := range remove {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("obsolete file retained", path, err)
		}
	}
}

func TestNativeGenesisRefusesPartialOrLostSelector(t *testing.T) {
	for _, suffix := range []string{"", ".tip", ".current", ".segment-00000000000000000000000000000001", ".current-tmp-crash"} {
		t.Run(suffix, func(t *testing.T) {
			anchor := filepath.Join(t.TempDir(), "system.wal")
			if err := os.WriteFile(anchor+suffix, []byte("old or partial state"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := requireFreshNativeFamily(anchor); err == nil {
				t.Fatal("missing selector permitted silent genesis")
			}
			if native, err := CreateNativeStore(nativeTestOptions(t, anchor)); err == nil {
				_ = native.Close()
				t.Fatal("fresh runtime overwrote partial state")
			}
		})
	}
}
