//go:build lantern_bench_heap && darwin

package main

import (
	"os"
	"testing"
)

func TestBenchHeapAtomicNoReplace(t *testing.T) { testBenchHeapAtomicNoReplace(t) }
func testBenchHeapAtomicNoReplace(t *testing.T) {
	directory := t.TempDir()
	dir, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := os.WriteFile(directory+"/source", []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory+"/existing", []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if benchHeapPublish(int(dir.Fd()), "source", "existing") == nil {
		t.Fatal("existing output overwritten")
	}
	raw, err := os.ReadFile(directory + "/existing")
	if err != nil || string(raw) != "old" {
		t.Fatal("old evidence changed", err)
	}
	if err := benchHeapPublish(int(dir.Fd()), "source", "fresh"); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(directory + "/fresh")
	if err != nil || string(raw) != "new" {
		t.Fatal("new evidence missing", err)
	}
}
