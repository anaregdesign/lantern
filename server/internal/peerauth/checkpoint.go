package peerauth

import (
	"io"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/privatefile"
)

const checkpointMagic = "LNPMS001"

func readCheckpoint(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= int64(len(checkpointMagic)) || info.Size() > MaxManifestBytes+int64(len(checkpointMagic)) {
		return nil, ErrMembership
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || privatefile.Check(f) != nil {
		return nil, ErrMembership
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxManifestBytes+int64(len(checkpointMagic))+1))
	if err != nil || len(raw) <= len(checkpointMagic) || len(raw) > MaxManifestBytes+len(checkpointMagic) || string(raw[:len(checkpointMagic)]) != checkpointMagic {
		return nil, ErrMembership
	}
	return raw[len(checkpointMagic):], nil
}

func writeCheckpoint(path string, raw []byte, fresh bool) error {
	return writeCheckpointWithHook(path, raw, fresh, nil)
}

func writeCheckpointWithHook(path string, raw []byte, fresh bool, hook func(string) error) error {
	check := func(stage string) error {
		if hook != nil {
			return hook(stage)
		}
		return nil
	}
	var f *os.File
	var err error
	if fresh {
		f, err = privatefile.Create(path, os.O_WRONLY)
	} else {
		f, err = privatefile.CreateTemp(filepath.Dir(path), filepath.Base(path)+".next-")
	}
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer func() {
		_ = f.Close()
		if temporary != path {
			_ = os.Remove(temporary)
		}
	}()
	payload := append([]byte(checkpointMagic), raw...)
	if err := check("write"); err != nil {
		return err
	}
	if n, err := f.Write(payload); err != nil {
		return err
	} else if n != len(payload) {
		return io.ErrShortWrite
	}
	if err := check("sync"); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !fresh {
		if err := check("rename"); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			return err
		}
	}
	if err := check("directory_sync"); err != nil {
		return err
	}
	return privatefile.SyncDirectory(filepath.Dir(path))
}
