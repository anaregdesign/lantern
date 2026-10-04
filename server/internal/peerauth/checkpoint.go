package peerauth

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

const checkpointMagic = "LNPMS001"

func readCheckpoint(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() <= int64(len(checkpointMagic)) || info.Size() > MaxManifestBytes+int64(len(checkpointMagic)) {
		return nil, ErrMembership
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrMembership
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxManifestBytes+int64(len(checkpointMagic))+1))
	if err != nil || len(raw) <= len(checkpointMagic) || len(raw) > MaxManifestBytes+len(checkpointMagic) || string(raw[:len(checkpointMagic)]) != checkpointMagic {
		return nil, ErrMembership
	}
	return raw[len(checkpointMagic):], nil
}

func writeCheckpoint(path string, raw []byte, fresh bool) error {
	var f *os.File
	var err error
	if fresh {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	} else {
		f, err = os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".next-")
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
	if n, err := f.Write(payload); err != nil {
		return err
	} else if n != len(payload) {
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !fresh {
		if err := os.Rename(temporary, path); err != nil {
			return err
		}
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
