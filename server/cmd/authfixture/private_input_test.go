package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/core/privatefile"
)

func TestWritePrivateInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	if err := writePrivateInput(path, strings.NewReader("fixture")); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Check(file); err != nil {
		t.Fatal("fixture input was exposed", err)
	}
	_ = file.Close()
	if err := writePrivateInput(path, strings.NewReader("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatal("fixture input was replaced", err)
	}
	for name, input := range map[string]string{"empty": "", "large": strings.Repeat("x", (1<<20)+1)} {
		output := filepath.Join(t.TempDir(), name)
		if err := writePrivateInput(output, strings.NewReader(input)); err == nil {
			t.Fatal("invalid private input accepted", name)
		}
		if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid input left a file", name, err)
		}
	}
	if err := writePrivateInput("relative", strings.NewReader("fixture")); !errors.Is(err, privatefile.ErrUnsafe) {
		t.Fatal("relative private input path accepted", err)
	}
}
