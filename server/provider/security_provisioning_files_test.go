package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvisioningFilesRefuseSymlinksAndExistingEntries(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "actual")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := createPrivateDirectory(filepath.Join(link, "output")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if err := createPrivateDirectory(target); err == nil {
		t.Fatal("existing directory accepted as fresh")
	}
	path := filepath.Join(target, "key")
	if err := writePrivateNewFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateNewFile(path, []byte("replacement")); err == nil {
		t.Fatal("existing private file overwritten")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "first" {
		t.Fatal("retained bytes changed")
	}
	if err := os.Chmod(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(target); err == nil {
		t.Fatal("public directory accepted")
	}
}
