package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/privatefile"
)

// Input never crosses stdout, argv or a broad-permission temporary file.
func writePrivateInput(path string, input io.Reader) error {
	if !filepath.IsAbs(path) {
		return privatefile.ErrUnsafe
	}
	raw, err := io.ReadAll(io.LimitReader(input, (1<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		return errors.New("invalid private fixture input")
	}
	file, err := privatefile.Create(path, os.O_WRONLY)
	if err != nil {
		return err
	}
	n, err := file.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
