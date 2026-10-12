package membershippublisher

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type failingFile struct {
	durableFile
	fail   string
	events *[]string
}

var errInjected = errors.New("errInjected IO failure")

func (f failingFile) Write(b []byte) (int, error) {
	*f.events = append(*f.events, "write")
	if f.fail == "write" {
		return 0, errInjected
	}
	if f.fail == "short" {
		return len(b) - 1, nil
	}
	return f.durableFile.Write(b)
}
func (f failingFile) Sync() error {
	*f.events = append(*f.events, "sync")
	if f.fail == "sync" {
		return errInjected
	}
	return f.durableFile.Sync()
}
func (f failingFile) Close() error {
	*f.events = append(*f.events, "close")
	e := f.durableFile.Close()
	if f.fail == "close" {
		return errInjected
	}
	return e
}

func TestDurableReplaceFaultsAndOrdering(t *testing.T) {
	for _, fail := range []string{"temp", "write", "sync", "close", "rename", "directory", ""} {
		t.Run(fail, func(t *testing.T) {
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			path := filepath.Join(dir, "target")
			if e := os.WriteFile(path, []byte("old"), 0600); e != nil {
				t.Fatal(e)
			}
			base := systemFiles()
			ops := base
			var events []string
			ops.temp = func(d, p string) (durableFile, error) {
				events = append(events, "temp")
				if fail == "temp" {
					return nil, errInjected
				}
				f, e := base.temp(d, p)
				return failingFile{f, fail, &events}, e
			}
			ops.rename = func(a, b string) error {
				events = append(events, "rename")
				if fail == "rename" {
					return errInjected
				}
				return base.rename(a, b)
			}
			ops.directory = func(d string) error {
				events = append(events, "directory")
				if fail == "directory" {
					return errInjected
				}
				return base.directory(d)
			}
			err := ops.replace(path, []byte("new"))
			if (err != nil) != (fail != "") {
				t.Fatal("lost persistence failure", err)
			}
			raw, e := readPrivate(path, 10)
			if e != nil {
				t.Fatal(e)
			}
			want := "old"
			if fail == "" || fail == "directory" {
				want = "new"
			}
			if string(raw) != want {
				t.Fatal("partial visible bytes", string(raw))
			}
			if fail == "" && !reflect.DeepEqual(events, []string{"temp", "write", "sync", "close", "rename", "directory"}) {
				t.Fatal(events)
			}
			files, _ := os.ReadDir(dir)
			if len(files) != 1 {
				t.Fatal("temporary file escaped cleanup")
			}
		})
	}
}
