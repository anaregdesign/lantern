package client

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestModuleBoundaryGate(t *testing.T) {
	const modulePrefix = "github.com/anaregdesign/lantern/"
	forbidden := []string{modulePrefix + "core", modulePrefix + "server"}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", entry.Name(), err)
			}
			for _, prefix := range forbidden {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					t.Errorf("%s imports forbidden Lantern module %q", entry.Name(), path)
				}
			}
		}
	}

	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, module := range forbidden {
		if strings.Contains(string(goMod), module) {
			t.Errorf("go.mod references forbidden Lantern module %q", module)
		}
	}
}
