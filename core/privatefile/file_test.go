package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAndCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	file, err := Create(path, os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := Check(file); err != nil {
		t.Fatal("new file is not private", err)
	}
	if _, err := file.WriteString("original"); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := Create(path, os.O_RDWR); !errors.Is(err, os.ErrExist) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatal("exclusive creation replaced an existing file", err)
	}
	for _, flags := range []int{os.O_TRUNC, os.O_CREATE, os.O_EXCL, os.O_WRONLY | os.O_RDWR} {
		if _, err := Create(path, flags); !errors.Is(err, ErrUnsafe) {
			t.Fatal("unsupported flags were accepted", flags, err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := Check(reopened); err != nil {
		t.Fatal("reopened private file rejected", err)
	}
	if err := Check(file); !errors.Is(err, ErrUnsafe) {
		t.Fatal("closed handle accepted", err)
	}
	if err := Check(nil); !errors.Is(err, ErrUnsafe) {
		t.Fatal("nil handle accepted", err)
	}
}

func TestCreateTemp(t *testing.T) {
	dir := t.TempDir()
	first, err := CreateTemp(dir, "next-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := CreateTemp(dir, "next-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if first.Name() == second.Name() || Check(first) != nil || Check(second) != nil {
		t.Fatal("temporary files are not distinct and private")
	}
	if _, err := CreateTemp(dir, "../escape"); !errors.Is(err, ErrUnsafe) {
		t.Fatal("temporary prefix escaped its directory", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = directory.Close() }()
	if err := Check(directory); !errors.Is(err, ErrUnsafe) {
		t.Fatal("directory accepted as a regular private file", err)
	}
}
