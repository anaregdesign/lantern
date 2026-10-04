package security

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeManifestPinsOneClassifiedSegment(t *testing.T) {
	anchor := filepath.Join(t.TempDir(), "system.wal")
	binding := [32]byte{7}
	initial := nativeManifest{binding: binding}
	if err := writeNativeManifest(anchor, initial, true); err != nil {
		t.Fatal(err)
	}
	if err := writeNativeManifest(anchor, initial, true); !errors.Is(err, os.ErrExist) {
		t.Fatal("fresh selector replaced existing state", err)
	}
	selected := nativeManifest{binding: binding, segment: [16]byte{1}, base: 99, checkpoint: [32]byte{8}}
	if err := writeNativeManifest(anchor, selected, false); err != nil {
		t.Fatal(err)
	}
	read, err := readNativeManifest(anchor, binding)
	if err != nil || read != selected || read.path(anchor) == anchor {
		t.Fatal(read, err)
	}
	if _, err := readNativeManifest(anchor, [32]byte{9}); err == nil {
		t.Fatal("generation/authority mismatch accepted")
	}
	raw := selected.encode()
	for _, damaged := range [][]byte{nil, raw[:len(raw)-1], append(bytes.Clone(raw), 0)} {
		if _, err := decodeNativeManifest(damaged, binding); err == nil {
			t.Fatal("damaged selector accepted")
		}
	}
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(anchor+".current", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readNativeManifest(anchor, binding); err == nil {
		t.Fatal("corrupt selector accepted")
	}
}
