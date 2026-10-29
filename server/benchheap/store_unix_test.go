//go:build linux || darwin

package benchheap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBenchHeapTrustedInputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Publication is exercised with actual no-replace syscalls by tagged cmd
	// tests. This store test only supplies an unused callback for input custody.
	store, err := OpenStore(physical, func(int, string, string) error { return ErrInput })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.WriteFile(physical+"/input", []byte(`{"value":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Value int `json:"value"`
	}
	if err := store.Read("input", &value, 64); err != nil || value.Value != 1 {
		t.Fatal("valid exact-owned input rejected", err)
	}
	if err := os.Symlink("input", physical+"/link"); err != nil {
		t.Fatal(err)
	}
	if store.Read("link", &value, 64) == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Link(physical+"/input", physical+"/alias"); err != nil {
		t.Fatal(err)
	}
	if store.Read("input", &value, 64) == nil {
		t.Fatal("aliased input accepted")
	}
	if store.Read("../input", &value, 64) == nil {
		t.Fatal("path escape accepted")
	}
}

func TestBenchHeapPublicationCustody(t *testing.T) {
	for _, name := range []string{"regular", "temp_symlink", "output_symlink", "temp_replaced"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			physical, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			// The production callback uses the platform no-replace syscall. This
			// callback injects entry replacement exactly at the rename boundary.
			store, err := OpenStore(physical, func(_ int, old, next string) error {
				if name == "temp_replaced" {
					if err := os.Remove(filepath.Join(physical, old)); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(physical, old), []byte("replacement"), 0600); err != nil {
						return err
					}
				}
				return os.Rename(filepath.Join(physical, old), filepath.Join(physical, next))
			})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			switch name {
			case "temp_symlink":
				if err := os.Symlink("never-created", filepath.Join(physical, ".output.tmp")); err != nil {
					t.Fatal(err)
				}
			case "output_symlink":
				if err := os.Symlink("never-created", filepath.Join(physical, "output")); err != nil {
					t.Fatal(err)
				}
			}
			err = store.Publish("output", struct {
				Value int `json:"value"`
			}{1})
			if name == "regular" {
				if err != nil {
					t.Fatal(err)
				}
				var value struct {
					Value int `json:"value"`
				}
				if err := store.Read("output", &value, 64); err != nil || value.Value != 1 {
					t.Fatal("published file invalid", err)
				}
			} else if err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if _, err := os.Lstat(filepath.Join(physical, "never-created")); !os.IsNotExist(err) {
				t.Fatal("dangling symlink target created", err)
			}
		})
	}
}
