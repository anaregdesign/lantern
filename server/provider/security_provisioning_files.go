package provider

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/privatefile"
)

func privateNewDirectory(path string) bool {
	if !cleanAbsolutePath(path) {
		return false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || parent != filepath.Dir(path) {
		return false
	}
	_, err = os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func createPrivateDirectory(path string) error {
	if !privateNewDirectory(path) || os.Mkdir(path, 0700) != nil {
		return errSecurityProvisioning
	}
	return syncSecurityDirectory(filepath.Dir(path))
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return createPrivateDirectory(path)
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errSecurityProvisioning
	}
	return nil
}

func writePrivateNewFile(path string, value []byte) (err error) {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errSecurityProvisioning
	}
	defer func() {
		if f.Close() != nil {
			err = errSecurityProvisioning
		}
	}()
	if privatefile.Check(f) != nil {
		return errSecurityProvisioning
	}
	if _, e = f.Write(value); e != nil {
		return errSecurityProvisioning
	}
	if f.Sync() != nil {
		return errSecurityProvisioning
	}
	return syncSecurityDirectory(filepath.Dir(path))
}

func syncSecurityDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return errSecurityProvisioning
	}
	defer d.Close()
	if d.Sync() != nil {
		return errSecurityProvisioning
	}
	return nil
}

// Explicit native initialization alone creates the final private state parent.
// A retained directory, even empty, denotes an interrupted or existing setup.
func prepareSecurityStateDirectory(root string) error {
	if !cleanAbsolutePath(root) {
		return errSecurityProvisioning
	}
	parent := filepath.Dir(root)
	resolved, err := filepath.EvalSymlinks(parent)
	if errors.Is(err, os.ErrNotExist) {
		// Only one derived node level is created. The deployment supplies its
		// persistence root; initialization does not construct arbitrary trees.
		return errSecurityProvisioning
	}
	if err != nil || resolved != parent {
		return errSecurityProvisioning
	}
	return createPrivateDirectory(root)
}
